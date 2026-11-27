package demand

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/agentsdk/testutil"
	"github.com/airlockrun/agentsdk/wire"
)

const regionalPhrase = "касперский для бизнеса"
const regionPayload = `{"results":[{"region":"213","count":"0","share":"0.3","affinityIndex":"2.1"},{"region":"225","count":"12","share":0.5,"affinityIndex":"1.2"},{"region":"3"}]}`
const regionTree = `{"regions":[{"id":"225","label":"Россия","children":[{"id":"213","label":"Москва"}]}]}`

type regionCallerFunc func(context.Context, string, any) (*agentsdk.MCPToolCallResponse, error)

func (f regionCallerFunc) CallTool(ctx context.Context, name string, args any) (*agentsdk.MCPToolCallResponse, error) {
	return f(ctx, name, args)
}

func regionUserContext(t *testing.T) context.Context {
	t.Helper()
	user := wire.CallerUser{ID: "00000000-0000-0000-0000-000000000001", PlatformMember: true}
	return testutil.WithCaller(t.Context(), wire.Caller{
		Kind: "user", Access: wire.AccessUser, User: &user, Initiator: &user,
		Origin: wire.CallerOrigin{Interface: "mcp", Execution: "request"},
	})
}

func regionResponse(payload string) *agentsdk.MCPToolCallResponse {
	raw, _ := json.Marshal(map[string]any{"content": []map[string]string{{"type": "text", "text": payload}}})
	var result agentsdk.MCPToolCallResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		panic(err)
	}
	return &result
}

func TestRegionalDemandEnrichesNamesPreservesRawDataAndCaches(t *testing.T) {
	var lookupCalls int
	var regionsDeadline time.Time
	provider := regionCallerFunc(func(ctx context.Context, name string, args any) (*agentsdk.MCPToolCallResponse, error) {
		deadline, hasDeadline := ctx.Deadline()
		if !hasDeadline || time.Until(deadline) > 55*time.Second {
			t.Fatal("provider operation must share a bounded 55-second request context")
		}
		switch name {
		case "regions":
			regionsDeadline = deadline
			want := map[string]any{"phrase": regionalPhrase, "regionMode": "regions"}
			if !reflect.DeepEqual(args, want) {
				t.Fatalf("region arguments changed: %#v", args)
			}
			return regionResponse(regionPayload), nil
		case "list_regions":
			if !deadline.Equal(regionsDeadline) {
				t.Fatal("directory lookup received a fresh deadline instead of the remaining request budget")
			}
			lookupCalls++
			raw, _ := json.Marshal(args)
			if string(raw) != "{}" {
				t.Fatalf("unexpected lookup arguments: %s", raw)
			}
			return regionResponse(regionTree), nil
		default:
			t.Fatalf("unexpected provider tool: %s", name)
			return nil, nil
		}
	})
	service := New(provider)
	if lookupCalls != 0 {
		t.Fatal("construction must not fetch the directory")
	}
	for range 2 {
		out, err := service.Regions(regionUserContext(t), RegionsInput{Phrase: regionalPhrase})
		if err != nil || out.Status != "ok" || out.SourceStatus != "historical_only" || out.RegionLookupStatus != "resolved" {
			t.Fatalf("unexpected regional result: %+v %v", out, err)
		}
		if len(out.Rows) != 2 || out.Rows[0].RegionID != "213" || out.Rows[0].RegionName != "Москва" || out.Rows[0].Label != "Москва" || out.Rows[0].Count == nil || *out.Rows[0].Count != 0 {
			t.Fatalf("region identity or true zero lost: %+v", out.Rows)
		}
		var original map[string]any
		decoder := json.NewDecoder(strings.NewReader(regionPayload))
		decoder.UseNumber()
		if err := decoder.Decode(&original); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(out.Data, original) {
			t.Fatalf("enrichment mutated original provider payload: %#v", out.Data)
		}
		if out.Source.WindowKnown {
			t.Fatal("enrichment must not invent a data window")
		}
	}
	if lookupCalls != 1 {
		t.Fatalf("directory must be cached, got %d lookups", lookupCalls)
	}
}

func TestRegionalDemandDirectoryFailurePreservesCountsAndRetriesLater(t *testing.T) {
	var lookups int
	service := New(regionCallerFunc(func(_ context.Context, name string, _ any) (*agentsdk.MCPToolCallResponse, error) {
		if name == "regions" {
			return regionResponse(regionPayload), nil
		}
		lookups++
		if lookups == 1 {
			return nil, errors.New("directory temporarily unavailable")
		}
		return regionResponse(regionTree), nil
	}))
	first, err := service.Regions(regionUserContext(t), RegionsInput{Phrase: regionalPhrase})
	if err != nil || first.Status != "ok" || first.RegionLookupStatus != "unavailable" || len(first.Data) == 0 || len(first.Rows) != 2 || first.Rows[0].Count == nil || *first.Rows[0].Count != 0 {
		t.Fatalf("directory failure replaced valid demand: %+v %v", first, err)
	}
	if first.Rows[0].Label != "Регион 213" || first.Rows[0].RegionName != "" || first.RegionLookupMessage == "" {
		t.Fatalf("unresolved name must remain explicit: %+v", first)
	}
	second, err := service.Regions(regionUserContext(t), RegionsInput{Phrase: regionalPhrase})
	if err != nil || second.RegionLookupStatus != "resolved" || lookups != 2 {
		t.Fatalf("failed lookup was incorrectly cached: %+v %v calls=%d", second, err, lookups)
	}
}

func TestRegionalDemandUnknownAndConflictingIDsRemainUnresolved(t *testing.T) {
	tree := `{"regions":[{"id":"213","label":"Москва","children":[{"id":"213","label":"Московская область"}]}]}`
	service := New(regionCallerFunc(func(_ context.Context, name string, _ any) (*agentsdk.MCPToolCallResponse, error) {
		if name == "regions" {
			return regionResponse(regionPayload), nil
		}
		return regionResponse(tree), nil
	}))
	out, err := service.Regions(regionUserContext(t), RegionsInput{Phrase: regionalPhrase})
	if err != nil || out.Status != "ok" || out.RegionLookupStatus != "partial" {
		t.Fatalf("unexpected partial result: %+v %v", out, err)
	}
	if out.Rows[0].RegionNameStatus != "ambiguous" || out.Rows[1].RegionNameStatus != "unknown" {
		t.Fatalf("conflicting and missing IDs must stay distinct: %+v", out.Rows)
	}
	for _, row := range out.Rows {
		if row.RegionName != "" || row.Label != "Регион "+row.RegionID || row.Count == nil {
			t.Fatalf("invented a name or lost a count: %+v", row)
		}
	}
}

func TestConcurrentRegionalCallsShareOneDirectoryFetch(t *testing.T) {
	var lookups atomic.Int32
	var regionCalls atomic.Int32
	release := make(chan struct{})
	const callers = 8
	service := New(regionCallerFunc(func(ctx context.Context, name string, _ any) (*agentsdk.MCPToolCallResponse, error) {
		if name == "regions" {
			if regionCalls.Add(1) == callers {
				close(release)
			}
			return regionResponse(regionPayload), nil
		}
		lookups.Add(1)
		select {
		case <-release:
			return regionResponse(regionTree), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}))
	ctx := regionUserContext(t)
	var wait sync.WaitGroup
	results := make(chan Result, callers)
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			out, err := service.Regions(ctx, RegionsInput{Phrase: regionalPhrase})
			if err != nil {
				t.Error(err)
			}
			results <- out
		}()
	}
	wait.Wait()
	close(results)
	for out := range results {
		if out.RegionLookupStatus != "resolved" {
			t.Fatalf("parallel caller lost the directory: %+v", out)
		}
	}
	if lookups.Load() != 1 {
		t.Fatalf("got %d parallel directory calls, want one", lookups.Load())
	}
}

func TestRegionalDemandDoesNotFetchDirectoryWithoutHumanCaller(t *testing.T) {
	var calls int
	service := New(regionCallerFunc(func(context.Context, string, any) (*agentsdk.MCPToolCallResponse, error) {
		calls++
		return regionResponse(regionTree), nil
	}))
	ctx := testutil.WithCaller(t.Context(), wire.Caller{
		Kind: "application", Access: wire.AccessAdmin,
		Origin: wire.CallerOrigin{Interface: "application", Execution: "background"},
	})
	if _, err := service.Regions(ctx, RegionsInput{Phrase: regionalPhrase}); err == nil || calls != 0 {
		t.Fatalf("unauthorized caller reached the provider: err=%v calls=%d", err, calls)
	}
}

func TestRegionalDemandDoesNotLookupAfterStatisticsFailure(t *testing.T) {
	var calls []string
	service := New(regionCallerFunc(func(_ context.Context, name string, _ any) (*agentsdk.MCPToolCallResponse, error) {
		calls = append(calls, name)
		var response agentsdk.MCPToolCallResponse
		_ = json.Unmarshal([]byte(`{"isError":true,"content":[{"type":"text","text":"Требуется WORDSTAT_API_KEY"}]}`), &response)
		return &response, nil
	}))
	out, err := service.Regions(regionUserContext(t), RegionsInput{Phrase: regionalPhrase})
	if err != nil || out.Status != "credentials_required" || out.RegionLookupStatus != "" || !reflect.DeepEqual(calls, []string{"regions"}) {
		t.Fatalf("lookup ran without statistics: %+v %v calls=%v", out, err, calls)
	}
}

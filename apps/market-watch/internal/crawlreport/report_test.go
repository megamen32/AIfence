package crawlreport

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVariantReconciliation(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "softmap")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	inventory := `{"source":"softmap","discovered_at":"2026-11-26T00:00:00Z","urls":["f"],"variant_urls":["a","a","b","c","d","e","g","h","j"]}`
	journal := `{"url":"e","at":"2026-11-25T23:59:00Z","state":"priced"}
{"url":"f","at":"2026-11-26T00:00:01Z","state":"priced","variants":[{"url":"a","status":"priced"},{"url":"j","status":"priced"}]}
{"url":"f","at":"2026-11-26T00:00:02Z","state":"priced","variants":[{"url":"a","status":"priced"},{"url":"b","status":"no_public_price"},{"url":"extra","status":"priced"}]}
{"url":"f","at":"2026-11-26T00:00:01Z","state":"priced","variants":[{"url":"j","status":"priced"}]}
{"url":"c","at":"2026-11-26T00:00:03Z","state":"unavailable"}
{"url":"d","at":"2026-11-26T00:00:04Z","state":"robots_excluded"}
{"url":"g","at":"2026-11-26T00:00:05Z","state":"unclassified"}
{"url":"h","at":"2026-11-26T00`
	if err := os.WriteFile(filepath.Join(dir, "inventory.json"), []byte(inventory), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pages.jsonl"), []byte(journal), 0600); err != nil {
		t.Fatal(err)
	}
	row, err := softmap(root)
	if err != nil || row.Total != 8 || row.Priced != 1 || row.NoPublicPrice != 1 || row.Unavailable != 1 || row.Excluded != 1 || row.Unresolved != 1 || row.Pending != 3 {
		t.Fatalf("%+v %v", row, err)
	}
}

// Scheduled imports only publish coverage for the matching deployment and
// only after all checkpoint producers are present.
func TestScheduledScope(t *testing.T) {
	root := t.TempDir()
	target := Target{CLI: "no-such-sdk-for-this-test", Origin: LocalOrigin, Agent: LocalAgent}
	if r, e := WithPrices(t.Context(), root, target); e != nil || r.AsOf != "" {
		t.Fatalf("legacy directory %+v %v", r, e)
	}
	for _, p := range []string{"softline/inventory.json", "softmap/progress.json", "softmagazin/progress.json"} {
		path := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("invalid fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, other := range []Target{{Origin: "http://localhost:42081", Agent: LocalAgent}, {Origin: LocalOrigin, Agent: "other"}} {
		if r, e := WithPrices(t.Context(), root, other); e != nil || r.AsOf != "" {
			t.Fatalf("unrelated %+v %v", r, e)
		}
	}
	if _, e := WithPrices(t.Context(), root, target); e == nil {
		t.Fatal("scheduled importer silently skipped invalid local checkpoints")
	}
	write := func(p, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, p), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("softline/inventory.json", `{"as_of":"2026-01-01T00:01:00Z","inventory_at":"2026-01-01T00:00:00Z","inventory_products":1,"states_by_inventory_url":{}}`)
	write("softmap/inventory.json", `{"source":"softmap","discovered_at":"2026-01-01T00:00:00Z","urls":["family"],"variant_urls":["license"]}`)
	write("softmap/pages.jsonl", "")
	if _, e := WithPrices(t.Context(), root, target); e == nil {
		t.Fatal("SDK failure treated as success")
	}
	target.CLI = filepath.Join(root, "test-sdk")
	write("test-sdk", `#!/bin/sh
test "$1" = request && test "$2" = POST && test "$3" = /coverage/import || exit 1
while [ "$#" -gt 0 ]; do
    case "$1" in --data) printf '%s' "$2"; exit 0 ;; esac
    shift
done
exit 1
`)
	if err := os.Chmod(target.CLI, 0700); err != nil {
		t.Fatal(err)
	}
	r, e := WithPrices(t.Context(), root, target)
	if e != nil || len(r.Sources) != 3 || r.Sources[0].Pending != 1 || r.Sources[2].Pending != 1 {
		t.Fatalf("scheduled route receipt %+v %v", r, e)
	}
}

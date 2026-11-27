package catalog

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestScheduleMixedLanesAndRepairWaiting(t *testing.T) {
	j, e := OpenJournal(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer j.Close()
	if e = j.InitSchedule(); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	var seeds []TaskSeed
	for i := 0; i < 40; i++ {
		seeds = append(seeds, TaskSeed{URL: fmt.Sprintf("family/%02d", i), Kind: "family"}, TaskSeed{URL: fmt.Sprintf("product/%02d", i), Kind: "product"})
	}
	seeds = append(seeds, TaskSeed{URL: "retry", Kind: "product", ErrorType: "network", At: now.Add(-time.Hour)}, TaskSeed{URL: "blocked", Kind: "product", ErrorType: "forbidden", At: now.Add(-time.Hour)}, TaskSeed{URL: "done", Kind: "product", Done: true})
	ev, e := j.Archive("softline", "repair", "repair", 200, "", now, []byte("<h1>broken</h1>"))
	if e != nil {
		t.Fatal(e)
	}
	seeds = append(seeds, TaskSeed{URL: "repair", Kind: "product", ErrorType: "parse_unclassified", At: now, EvidenceID: ev.ID})
	if e = j.SeedPlan("softline", "g", seeds); e != nil {
		t.Fatal(e)
	}
	task, e := j.Ready("softline", "g", []string{"family", "product"}, 16, now)
	if e != nil {
		t.Fatal(e)
	}
	seen := map[string]int{}
	foundRetry := false
	for _, v := range task {
		seen[v.Kind]++
		if v.URL == "retry" {
			foundRetry = true
		}
		if v.URL == "repair" || v.URL == "blocked" || v.URL == "done" {
			t.Fatal("terminal task admitted", v)
		}
	}
	if len(task) != 16 || seen["family"] == 0 || seen["product"] == 0 || !foundRetry {
		t.Fatal("unfair queue", seen, foundRetry)
	}
	f, e := j.Failure("softline", "repair", "g", "product")
	if e != nil || f.State != "repair" {
		t.Fatal(f, e)
	}
	// Do not spend attempts or change a future network retry on restart.
	if e = j.RecordFailure("softline", "retry", "g", "product", "network", "", 0, now, time.Time{}); e != nil {
		t.Fatal(e)
	}
	before, e := j.Failure("softline", "retry", "g", "product")
	if e != nil {
		t.Fatal(e)
	}
	if e = j.SeedPlan("softline", "g", seeds); e != nil {
		t.Fatal(e)
	}
	after, e := j.Failure("softline", "retry", "g", "product")
	if e != nil || after.Attempts != before.Attempts || !after.NextAttempt.Equal(before.NextAttempt) {
		t.Fatal("retry reset", after, e)
	}
	// Accepted rule explicitly reopens archived work, without HTTP.
	if _, e = j.db.Exec("UPDATE failed_tasks SET state='retry',attempts=0,next_attempt=0 WHERE url='repair'"); e != nil {
		t.Fatal(e)
	}
	ready, e := j.Ready("softline", "g", []string{"family", "product"}, 16, now)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, v := range ready {
		if v.URL == "repair" {
			found = true
			d := DownloadReady(WithJournal(context.Background(), j, "g", "product"), Source{ID: "softline"}, v, func(context.Context, Source, string) ([]byte, error) {
				t.Fatal("local replay performed HTTP")
				return nil, nil
			})
			if d.Err != nil || d.Evidence.ID != ev.ID || !d.At.Equal(now) {
				t.Fatal(d)
			}
		}
	}
	if !found {
		t.Fatal("approved local replay starved")
	}
}

func TestSchedulePageFamilyAndVariantLanes(t *testing.T) {
	j, e := OpenJournal(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer j.Close()
	if e = j.InitSchedule(); e != nil {
		t.Fatal(e)
	}
	var seeds []TaskSeed
	for i := 0; i < 50; i++ {
		seeds = append(seeds, TaskSeed{URL: fmt.Sprintf("f%d", i), Kind: "page", Lane: "family", Priority: 8}, TaskSeed{URL: fmt.Sprintf("v%d", i), Kind: "page", Lane: "product"})
	}
	if e = j.SeedPlan("softmagazin", "g", seeds); e != nil {
		t.Fatal(e)
	}
	tasks, e := j.Ready("softmagazin", "g", []string{"page"}, 24, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	seen := map[byte]int{}
	for _, task := range tasks {
		seen[task.URL[0]]++
	}
	if seen['f'] == 0 || seen['v'] == 0 {
		t.Fatal("variant phase barrier", seen)
	}
}

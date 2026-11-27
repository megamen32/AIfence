package market

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativePipelineParallelAndJoined(t *testing.T) {
	var tasks []AdapterTask
	for i := range 32 {
		tasks = append(tasks, AdapterTask{URL: fmt.Sprint(i)})
	}
	var active, peak atomic.Int32
	consumed := 0
	e := nativePipeline(t.Context(), tasks, func(ctx context.Context, t AdapterTask) nativeDownloaded {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Millisecond):
		}
		return nativeDownloaded{Task: t}
	}, func(d nativeDownloaded) nativeParsed { return nativeParsed{Download: d} }, func(p nativeParsed) error { consumed++; return nil })
	if e != nil || consumed != len(tasks) || peak.Load() < 2 || peak.Load() > nativeFetchWorkers || active.Load() != 0 {
		t.Fatal(consumed, peak.Load(), active.Load(), e)
	}
}

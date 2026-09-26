package metrics

import (
	"runtime"
	rtmetrics "runtime/metrics"
	"time"
)

// CollectRuntime registers the Go runtime, process and build-info series.
// It reads runtime/metrics, which does not stop the world.
func (r *Registry) CollectRuntime(version, commit string) {
	start := float64(time.Now().Unix())
	samples := []rtmetrics.Sample{
		{Name: "/memory/classes/heap/objects:bytes"},
		{Name: "/memory/classes/total:bytes"},
		{Name: "/gc/cycles/total:gc-cycles"},
	}
	r.Collect(func(e *Emitter) {
		read := append([]rtmetrics.Sample(nil), samples...)
		rtmetrics.Read(read)
		value := func(i int) float64 {
			if read[i].Value.Kind() == rtmetrics.KindUint64 {
				return float64(read[i].Value.Uint64())
			}
			return 0
		}
		e.Gauge("go_goroutines", "Goroutines that currently exist.", float64(runtime.NumGoroutine()))
		e.Gauge("go_memstats_heap_alloc_bytes", "Bytes of live and not-yet-swept heap objects.", value(0))
		e.Gauge("go_memstats_sys_bytes", "Bytes of memory mapped by the Go runtime.", value(1))
		e.Counter("go_gc_cycles_total", "Completed GC cycles.", value(2))
		e.Gauge("process_start_time_seconds", "Start time of the process since the Unix epoch.", start)
		e.Gauge("converge_build_info", "The running build; the value is always 1.", 1,
			"version", version, "commit", commit, "goversion", runtime.Version())
	})
}

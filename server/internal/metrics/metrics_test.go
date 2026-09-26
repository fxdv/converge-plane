package metrics

import (
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func render(r *Registry) string {
	var b strings.Builder
	r.Write(&b)
	return b.String()
}

func TestExposition(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounterVec("t_requests_total", "Requests.", "route", "code")
	c.With("/b", "200").Inc()
	c.With("/a", "500").Add(2)
	c.With("/a", "500").Inc()
	g := r.NewGauge("t_in_flight", "In flight.")
	g.Add(3)
	g.Add(-1)
	h := r.NewHistogramVec("t_seconds", "Latency.", []float64{0.1, 1}, "route")
	h.With("/a").Observe(0.05)
	h.With("/a").Observe(0.1)
	h.With("/a").Observe(5)
	r.Collect(func(e *Emitter) {
		e.Gauge("t_pool", "Pool.", 4, "state", "idle")
		e.Gauge("t_pool", "Pool.", 1, "state", "acquired")
	})

	want := `# HELP t_requests_total Requests.
# TYPE t_requests_total counter
t_requests_total{route="/a",code="500"} 3
t_requests_total{route="/b",code="200"} 1
# HELP t_in_flight In flight.
# TYPE t_in_flight gauge
t_in_flight 2
# HELP t_seconds Latency.
# TYPE t_seconds histogram
t_seconds_bucket{route="/a",le="0.1"} 2
t_seconds_bucket{route="/a",le="1"} 2
t_seconds_bucket{route="/a",le="+Inf"} 3
t_seconds_sum{route="/a"} 5.15
t_seconds_count{route="/a"} 3
# HELP t_pool Pool.
# TYPE t_pool gauge
t_pool{state="idle"} 4
t_pool{state="acquired"} 1
`
	if got := render(r); got != want {
		t.Fatalf("exposition:\n%s\nwant:\n%s", got, want)
	}
}

func TestLabelEscaping(t *testing.T) {
	r := NewRegistry()
	r.NewCounterVec("t_total", "Help with \\ and\nnewline.", "v").With("a\"b\\c\nd").Inc()
	got := render(r)
	if !strings.Contains(got, `t_total{v="a\"b\\c\nd"} 1`) {
		t.Fatalf("label not escaped:\n%s", got)
	}
	if !strings.Contains(got, `# HELP t_total Help with \\ and\nnewline.`) {
		t.Fatalf("help not escaped:\n%s", got)
	}
}

func TestDuplicateRegistrationPanics(t *testing.T) {
	r := NewRegistry()
	r.NewGauge("t_dup", "x")
	defer func() {
		if recover() == nil {
			t.Fatal("a second family with the same name registered")
		}
	}()
	r.NewCounterVec("t_dup", "x")
}

func TestConcurrentCounting(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounterVec("t_total", "x", "k")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				c.With("same").Inc()
			}
		}()
	}
	wg.Wait()
	if v := c.With("same").Value(); v != 8000 {
		t.Fatalf("count = %v, want 8000", v)
	}
}

func TestHandlerAndRuntime(t *testing.T) {
	r := NewRegistry()
	r.CollectRuntime("1.2.3", "abc")
	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
		t.Fatalf("content type = %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{"go_goroutines ", "go_memstats_heap_alloc_bytes ", "go_gc_cycles_total ",
		`converge_build_info{version="1.2.3",commit="abc",goversion="`} {
		if !strings.Contains(body, want) {
			t.Errorf("runtime collector missing %q", want)
		}
	}
}

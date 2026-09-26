// Package metrics is a small Prometheus exposition registry: counters,
// gauges and histograms with fixed label sets, plus collectors that read
// values owned elsewhere (the pool, the broadcaster) at scrape time. It
// speaks text format 0.0.4 and depends on nothing outside the standard
// library.
//
// Label values must come from bounded sets (route patterns, status codes,
// enum-like reasons), never from ids or user input: every distinct value
// is a series held for the life of the process.
package metrics

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Default is the process registry the server exposes.
var Default = NewRegistry()

// Registry holds metric families in registration order.
type Registry struct {
	mu         sync.Mutex
	families   []family
	names      map[string]bool
	collectors []func(*Emitter)
}

type family interface {
	write(w io.Writer)
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{names: map[string]bool{}}
}

func (r *Registry) register(name string, f family) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.names[name] {
		panic("metrics: duplicate registration of " + name)
	}
	r.names[name] = true
	r.families = append(r.families, f)
}

// Collect registers fn to emit samples at scrape time.
func (r *Registry) Collect(fn func(*Emitter)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.collectors = append(r.collectors, fn)
}

// Write renders every family in the text exposition format.
func (r *Registry) Write(w io.Writer) {
	r.mu.Lock()
	families := append([]family(nil), r.families...)
	collectors := append([]func(*Emitter){}, r.collectors...)
	r.mu.Unlock()
	for _, f := range families {
		f.write(w)
	}
	e := &Emitter{w: w, seen: map[string]bool{}}
	for _, fn := range collectors {
		fn(e)
	}
}

// Handler serves the registry for a Prometheus scrape.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		r.Write(w)
	})
}

// ---- counters ------------------------------------------------------------

// CounterVec is a family of monotonically increasing counters.
type CounterVec struct {
	name, help string
	labels     []string
	mu         sync.RWMutex
	series     map[string]*Counter
}

// Counter is one labelled series of a CounterVec.
type Counter struct {
	values []string
	bits   atomic.Uint64
}

// NewCounterVec registers a counter family with the given label names.
func (r *Registry) NewCounterVec(name, help string, labels ...string) *CounterVec {
	c := &CounterVec{name: name, help: help, labels: labels, series: map[string]*Counter{}}
	r.register(name, c)
	return c
}

// With returns the series for the label values, creating it on first use.
func (c *CounterVec) With(values ...string) *Counter {
	if len(values) != len(c.labels) {
		panic(fmt.Sprintf("metrics: %s wants %d label values, got %d", c.name, len(c.labels), len(values)))
	}
	key := strings.Join(values, "\xff")
	c.mu.RLock()
	s, ok := c.series[key]
	c.mu.RUnlock()
	if ok {
		return s
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok = c.series[key]; !ok {
		s = &Counter{values: append([]string(nil), values...)}
		c.series[key] = s
	}
	return s
}

// Inc adds one.
func (s *Counter) Inc() { s.Add(1) }

// Add adds v, which must not be negative.
func (s *Counter) Add(v float64) {
	for {
		old := s.bits.Load()
		next := math.Float64bits(math.Float64frombits(old) + v)
		if s.bits.CompareAndSwap(old, next) {
			return
		}
	}
}

// Value reports the current count.
func (s *Counter) Value() float64 { return math.Float64frombits(s.bits.Load()) }

func (c *CounterVec) write(w io.Writer) {
	header(w, c.name, c.help, "counter")
	c.mu.RLock()
	series := make([]*Counter, 0, len(c.series))
	for _, s := range c.series {
		series = append(series, s)
	}
	c.mu.RUnlock()
	sort.Slice(series, func(i, j int) bool { return lessValues(series[i].values, series[j].values) })
	for _, s := range series {
		sample(w, c.name, c.labels, s.values, s.Value())
	}
}

// ---- gauges --------------------------------------------------------------

// Gauge is an unlabelled value that goes up and down.
type Gauge struct {
	name, help string
	bits       atomic.Int64
}

// NewGauge registers an integer gauge.
func (r *Registry) NewGauge(name, help string) *Gauge {
	g := &Gauge{name: name, help: help}
	r.register(name, g)
	return g
}

// Add moves the gauge by delta.
func (g *Gauge) Add(delta int64) { g.bits.Add(delta) }

// Value reports the current value.
func (g *Gauge) Value() int64 { return g.bits.Load() }

func (g *Gauge) write(w io.Writer) {
	header(w, g.name, g.help, "gauge")
	sample(w, g.name, nil, nil, float64(g.Value()))
}

// ---- histograms ----------------------------------------------------------

// DurationBuckets are latency buckets in seconds, from a fast handler to a
// slow model call.
var DurationBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30}

// HistogramVec is a family of cumulative histograms.
type HistogramVec struct {
	name, help string
	labels     []string
	buckets    []float64
	mu         sync.RWMutex
	series     map[string]*Histogram
}

// Histogram is one labelled series of a HistogramVec.
type Histogram struct {
	values  []string
	buckets []float64
	mu      sync.Mutex
	counts  []uint64
	sum     float64
	count   uint64
}

// NewHistogramVec registers a histogram family with ascending buckets.
func (r *Registry) NewHistogramVec(name, help string, buckets []float64, labels ...string) *HistogramVec {
	h := &HistogramVec{name: name, help: help, labels: labels, buckets: buckets, series: map[string]*Histogram{}}
	r.register(name, h)
	return h
}

// With returns the series for the label values, creating it on first use.
func (h *HistogramVec) With(values ...string) *Histogram {
	if len(values) != len(h.labels) {
		panic(fmt.Sprintf("metrics: %s wants %d label values, got %d", h.name, len(h.labels), len(values)))
	}
	key := strings.Join(values, "\xff")
	h.mu.RLock()
	s, ok := h.series[key]
	h.mu.RUnlock()
	if ok {
		return s
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok = h.series[key]; !ok {
		s = &Histogram{values: append([]string(nil), values...), buckets: h.buckets, counts: make([]uint64, len(h.buckets))}
		h.series[key] = s
	}
	return s
}

// Observe records one value.
func (s *Histogram) Observe(v float64) {
	i := sort.SearchFloat64s(s.buckets, v)
	s.mu.Lock()
	defer s.mu.Unlock()
	if i < len(s.counts) {
		s.counts[i]++
	}
	s.sum += v
	s.count++
}

func (h *HistogramVec) write(w io.Writer) {
	header(w, h.name, h.help, "histogram")
	h.mu.RLock()
	series := make([]*Histogram, 0, len(h.series))
	for _, s := range h.series {
		series = append(series, s)
	}
	h.mu.RUnlock()
	sort.Slice(series, func(i, j int) bool { return lessValues(series[i].values, series[j].values) })
	labels := append(append([]string(nil), h.labels...), "le")
	for _, s := range series {
		s.mu.Lock()
		counts, sum, count := append([]uint64(nil), s.counts...), s.sum, s.count
		s.mu.Unlock()
		var cumulative uint64
		for i, b := range h.buckets {
			cumulative += counts[i]
			sample(w, h.name+"_bucket", labels, append(append([]string(nil), s.values...), formatFloat(b)), float64(cumulative))
		}
		sample(w, h.name+"_bucket", labels, append(append([]string(nil), s.values...), "+Inf"), float64(count))
		sample(w, h.name+"_sum", h.labels, s.values, sum)
		sample(w, h.name+"_count", h.labels, s.values, float64(count))
	}
}

// ---- scrape-time collection ----------------------------------------------

// Emitter writes collector samples; the first sample of a name writes its
// HELP and TYPE lines.
type Emitter struct {
	w    io.Writer
	seen map[string]bool
}

// Gauge emits one gauge sample. labels alternate name, value.
func (e *Emitter) Gauge(name, help string, v float64, labels ...string) {
	e.emit(name, help, "gauge", v, labels)
}

// Counter emits one counter sample read from elsewhere.
func (e *Emitter) Counter(name, help string, v float64, labels ...string) {
	e.emit(name, help, "counter", v, labels)
}

func (e *Emitter) emit(name, help, typ string, v float64, labels []string) {
	if !e.seen[name] {
		e.seen[name] = true
		header(e.w, name, help, typ)
	}
	names := make([]string, 0, len(labels)/2)
	values := make([]string, 0, len(labels)/2)
	for i := 0; i+1 < len(labels); i += 2 {
		names = append(names, labels[i])
		values = append(values, labels[i+1])
	}
	sample(e.w, name, names, values, v)
}

// ---- text format -----------------------------------------------------------

func header(w io.Writer, name, help, typ string) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", name, escapeHelp(help), name, typ)
}

func sample(w io.Writer, name string, labels, values []string, v float64) {
	var b strings.Builder
	b.WriteString(name)
	if len(labels) > 0 {
		b.WriteByte('{')
		for i, l := range labels {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(l)
			b.WriteString(`="`)
			b.WriteString(escapeLabel(values[i]))
			b.WriteByte('"')
		}
		b.WriteByte('}')
	}
	b.WriteByte(' ')
	b.WriteString(formatFloat(v))
	b.WriteByte('\n')
	_, _ = io.WriteString(w, b.String())
}

func formatFloat(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

var (
	labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	helpEscaper  = strings.NewReplacer(`\`, `\\`, "\n", `\n`)
)

func escapeLabel(s string) string { return labelEscaper.Replace(s) }
func escapeHelp(s string) string  { return helpEscaper.Replace(s) }

func lessValues(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

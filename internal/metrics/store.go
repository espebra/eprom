// Package metrics keeps a bounded, in-memory history of scraped Prometheus
// metrics and derives plottable series from the raw samples.
//
// Gauges and untyped metrics are stored as-is. Counters are converted to
// per-second rates. Summaries are stored as their reported quantiles.
// Histograms are converted to estimated quantiles over each scrape interval.
package metrics

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	dto "github.com/prometheus/client_model/go"
)

// HistogramQuantiles are the quantiles estimated for histogram metrics.
var HistogramQuantiles = []float64{0.5, 0.9, 0.99}

// Point is a single value at a given scrape sequence number. V is NaN when
// there is no meaningful value for that scrape (e.g. no histogram
// observations during the interval).
type Point struct {
	Seq uint64
	V   float64
}

// Series is one line in a panel.
type Series struct {
	Label  string
	Points []Point
}

// Last returns the most recent point, if any.
func (s *Series) Last() (Point, bool) {
	if len(s.Points) == 0 {
		return Point{}, false
	}
	return s.Points[len(s.Points)-1], true
}

// Panel holds all series derived from a single metric family.
type Panel struct {
	Name string
	Help string
	Type dto.MetricType
	// Unit is the base unit of the values: "bytes", "seconds" (a duration),
	// "timestamp" (stored as the age in seconds at scrape time), or "".
	Unit string
	// Rate is true when values are per-second rates derived from a counter.
	Rate bool

	series map[string]*Series
	order  []string
}

// Series returns the panel's series sorted by label.
func (p *Panel) Series() []*Series {
	out := make([]*Series, 0, len(p.order))
	for _, k := range p.order {
		out = append(out, p.series[k])
	}
	return out
}

// Title returns the HELP text, falling back to the metric name.
func (p *Panel) Title() string {
	if p.Help != "" {
		return p.Help
	}
	return p.Name
}

// TypeName returns a lower-case name of the metric type.
func (p *Panel) TypeName() string {
	return strings.ToLower(p.Type.String())
}

func (p *Panel) add(label string, seq uint64, v float64, history int) {
	s, ok := p.series[label]
	if !ok {
		s = &Series{Label: label}
		p.series[label] = s
		p.order = append(p.order, label)
		sort.Strings(p.order)
	}
	s.Points = append(s.Points, Point{Seq: seq, V: v})
	if len(s.Points) > history {
		s.Points = s.Points[len(s.Points)-history:]
	}
}

// prune drops series whose newest point is older than the oldest retained seq.
func (p *Panel) prune(oldest uint64) {
	kept := p.order[:0]
	for _, k := range p.order {
		s := p.series[k]
		if last, ok := s.Last(); ok && last.Seq >= oldest {
			kept = append(kept, k)
		} else {
			delete(p.series, k)
		}
	}
	p.order = kept
}

// state is the previous raw sample for a cumulative metric, needed to compute
// deltas between scrapes.
type state struct {
	seen    uint64
	value   float64   // counter value, or summary/histogram count
	sum     float64   // summary/histogram sum
	buckets []float64 // histogram cumulative bucket counts
	bounds  []float64 // histogram bucket upper bounds
}

// Store holds the history of all panels.
type Store struct {
	history  int
	seq      uint64
	lastTime time.Time
	panels   map[string]*Panel
	order    []string
	prev     map[string]*state
}

// NewStore creates a store that keeps up to history points per series.
func NewStore(history int) *Store {
	if history < 2 {
		history = 2
	}
	return &Store{
		history: history,
		panels:  map[string]*Panel{},
		prev:    map[string]*state{},
	}
}

// Seq returns the sequence number of the latest scrape (0 before any scrape).
func (s *Store) Seq() uint64 { return s.seq }

// Panels returns all panels sorted by metric name.
func (s *Store) Panels() []*Panel {
	out := make([]*Panel, 0, len(s.order))
	for _, k := range s.order {
		out = append(out, s.panels[k])
	}
	return out
}

// Gap records a failed scrape, so that it shows up as a gap in the charts.
func (s *Store) Gap() { s.seq++ }

// Ingest adds one scrape taken at time t.
func (s *Store) Ingest(families map[string]*dto.MetricFamily, t time.Time) {
	s.seq++
	dt := 0.0
	if !s.lastTime.IsZero() {
		dt = t.Sub(s.lastTime).Seconds()
	}
	s.lastTime = t

	for name, mf := range families {
		p := s.panel(name, mf)
		for _, m := range mf.GetMetric() {
			s.ingestMetric(p, mf.GetType(), m, dt)
		}
	}

	var oldest uint64
	if s.seq > uint64(s.history) {
		oldest = s.seq - uint64(s.history) + 1
	}
	for k, st := range s.prev {
		if st.seen < oldest {
			delete(s.prev, k)
		}
	}
	for _, p := range s.panels {
		p.prune(oldest)
	}
}

func (s *Store) panel(name string, mf *dto.MetricFamily) *Panel {
	p, ok := s.panels[name]
	if !ok {
		p = &Panel{Name: name, series: map[string]*Series{}}
		s.panels[name] = p
		s.order = append(s.order, name)
		sort.Strings(s.order)
	}
	p.Help = mf.GetHelp()
	p.Type = mf.GetType()
	p.Rate = p.Type == dto.MetricType_COUNTER
	p.Unit = InferUnit(name, mf.GetUnit())
	switch {
	case p.Rate && p.Unit == "seconds":
		// Seconds per second, e.g. CPU time: a plain ratio such as busy cores.
		p.Unit = ""
	case p.Unit == "timestamp" && p.Type != dto.MetricType_GAUGE && p.Type != dto.MetricType_UNTYPED:
		p.Unit = ""
	}
	return p
}

func (s *Store) ingestMetric(p *Panel, typ dto.MetricType, m *dto.Metric, dt float64) {
	labels := FormatLabels(m.GetLabel())
	key := p.Name + labels
	seq := s.seq

	switch typ {
	case dto.MetricType_GAUGE:
		p.add(labels, seq, s.gaugeValue(p, m.GetGauge().GetValue()), s.history)

	case dto.MetricType_UNTYPED:
		p.add(labels, seq, s.gaugeValue(p, m.GetUntyped().GetValue()), s.history)

	case dto.MetricType_COUNTER:
		v := m.GetCounter().GetValue()
		if prev, ok := s.prev[key]; ok && dt > 0 {
			p.add(labels, seq, rate(prev.value, v, dt), s.history)
		}
		s.prev[key] = &state{seen: seq, value: v}

	case dto.MetricType_SUMMARY:
		sm := m.GetSummary()
		if qs := sm.GetQuantile(); len(qs) > 0 {
			for _, q := range qs {
				p.add(join(labels, quantileLabel(q.GetQuantile())), seq, q.GetValue(), s.history)
			}
			return
		}
		// No quantiles reported: plot the average observation per interval.
		count, sum := float64(sm.GetSampleCount()), sm.GetSampleSum()
		if prev, ok := s.prev[key]; ok && dt > 0 {
			p.add(join(labels, "avg"), seq, average(prev, count, sum), s.history)
		}
		s.prev[key] = &state{seen: seq, value: count, sum: sum}

	case dto.MetricType_HISTOGRAM, dto.MetricType_GAUGE_HISTOGRAM:
		bounds, cum := histogramBuckets(m.GetHistogram())
		if len(bounds) == 0 {
			return
		}
		cur := &state{seen: seq, bounds: bounds, buckets: cum}
		if typ == dto.MetricType_GAUGE_HISTOGRAM {
			// Gauge histograms are a current distribution, not cumulative over time.
			s.addQuantiles(p, labels, bounds, cum)
			return
		}
		if prev, ok := s.prev[key]; ok && dt > 0 {
			s.addQuantiles(p, labels, bounds, bucketDelta(prev, cur))
		}
		s.prev[key] = cur
	}
}

// gaugeValue converts timestamps to their age at scrape time, which is more
// readable and graphs better than seconds since 1970. A zero timestamp
// usually means "never" and becomes a gap.
func (s *Store) gaugeValue(p *Panel, v float64) float64 {
	if p.Unit != "timestamp" {
		return v
	}
	if v <= 0 {
		return math.NaN()
	}
	return float64(s.lastTime.UnixNano())/1e9 - v
}

func (s *Store) addQuantiles(p *Panel, labels string, bounds, cum []float64) {
	for _, q := range HistogramQuantiles {
		p.add(join(labels, quantileLabel(q)), s.seq, BucketQuantile(q, bounds, cum), s.history)
	}
}

// rate computes a per-second rate, treating a decrease as a counter reset.
func rate(prev, cur, dt float64) float64 {
	if cur < prev {
		return cur / dt
	}
	return (cur - prev) / dt
}

func average(prev *state, count, sum float64) float64 {
	dc, ds := count-prev.value, sum-prev.sum
	if dc < 0 { // reset
		dc, ds = count, sum
	}
	if dc == 0 {
		return math.NaN()
	}
	return ds / dc
}

// histogramBuckets returns the bucket upper bounds and cumulative counts,
// always ending with a +Inf bucket.
func histogramBuckets(h *dto.Histogram) (bounds, cum []float64) {
	useFloat := h.SampleCountFloat != nil
	for _, b := range h.GetBucket() {
		c := float64(b.GetCumulativeCount())
		if useFloat || b.CumulativeCountFloat != nil {
			c = b.GetCumulativeCountFloat()
		}
		bounds = append(bounds, b.GetUpperBound())
		cum = append(cum, c)
	}
	if len(bounds) == 0 {
		return nil, nil
	}
	if !math.IsInf(bounds[len(bounds)-1], 1) {
		total := float64(h.GetSampleCount())
		if useFloat {
			total = h.GetSampleCountFloat()
		}
		bounds = append(bounds, math.Inf(1))
		cum = append(cum, total)
	}
	return bounds, cum
}

// bucketDelta returns the per-bucket increase between two scrapes. If the
// bucket layout changed or the histogram was reset, the current counts are
// returned as-is.
func bucketDelta(prev, cur *state) []float64 {
	if len(prev.bounds) != len(cur.bounds) {
		return cur.buckets
	}
	out := make([]float64, len(cur.buckets))
	for i := range cur.buckets {
		if prev.bounds[i] != cur.bounds[i] || cur.buckets[i] < prev.buckets[i] {
			return cur.buckets
		}
		out[i] = cur.buckets[i] - prev.buckets[i]
	}
	return out
}

// BucketQuantile estimates the q-quantile from cumulative bucket counts using
// linear interpolation within the bucket, like PromQL's histogram_quantile.
// bounds must be sorted ascending and end with +Inf. Returns NaN if there are
// no observations.
func BucketQuantile(q float64, bounds, cum []float64) float64 {
	n := len(bounds)
	if n == 0 || len(cum) != n {
		return math.NaN()
	}
	// Guard against non-monotonic counts caused by scraping mid-update.
	c := make([]float64, n)
	for i := range cum {
		c[i] = cum[i]
		if i > 0 && c[i] < c[i-1] {
			c[i] = c[i-1]
		}
	}
	total := c[n-1]
	if total <= 0 {
		return math.NaN()
	}
	rank := q * total
	i := sort.Search(n, func(i int) bool { return c[i] >= rank })
	if i >= n-1 {
		// Falls in the +Inf bucket: the best estimate is the highest finite bound.
		if n >= 2 {
			return bounds[n-2]
		}
		return math.NaN()
	}
	lower, below := 0.0, 0.0
	if i > 0 {
		lower, below = bounds[i-1], c[i-1]
	} else if bounds[0] <= 0 {
		return bounds[0]
	}
	inBucket := c[i] - below
	if inBucket == 0 {
		return bounds[i]
	}
	return lower + (bounds[i]-lower)*(rank-below)/inBucket
}

// InferUnit returns the base unit of a metric: "bytes", "seconds",
// "timestamp" (seconds since 1970) or "". The classic text format has no unit
// metadata, so the unit is taken from the metric name, which by convention
// ends with it, e.g. node_memory_free_bytes or node_network_receive_bytes_total.
// An explicitly given unit takes precedence, but the name still decides
// whether seconds are a timestamp.
func InferUnit(name, unit string) string {
	name = strings.TrimSuffix(name, "_total")
	if unit == "" {
		switch {
		case strings.HasSuffix(name, "_bytes"):
			unit = "bytes"
		case strings.HasSuffix(name, "_seconds"):
			unit = "seconds"
		case strings.HasSuffix(name, "_timestamp"):
			return "timestamp"
		}
	}
	if unit == "seconds" {
		base := strings.TrimSuffix(name, "_seconds")
		if strings.HasSuffix(base, "_time") || strings.HasSuffix(base, "_timestamp") {
			return "timestamp"
		}
	}
	return unit
}

// FormatLabels renders label pairs as {a="b", c="d"} sorted by name, or an
// empty string if there are none.
func FormatLabels(pairs []*dto.LabelPair) string {
	if len(pairs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(pairs))
	for _, lp := range pairs {
		parts = append(parts, fmt.Sprintf("%s=%q", lp.GetName(), lp.GetValue()))
	}
	sort.Strings(parts)
	return "{" + strings.Join(parts, ", ") + "}"
}

func quantileLabel(q float64) string {
	// Round away float noise, e.g. 0.9*100 = 90.00000000000001.
	return "p" + strconv.FormatFloat(math.Round(q*100*1e6)/1e6, 'f', -1, 64)
}

func join(labels, suffix string) string {
	if labels == "" {
		return suffix
	}
	return labels + " " + suffix
}

package metrics

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/espebra/eprom/internal/scrape"
)

func ingest(t *testing.T, s *Store, text string, at time.Time) {
	t.Helper()
	families, err := scrape.Parse(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	s.Ingest(families, at)
}

func values(t *testing.T, s *Store, name, label string) []float64 {
	t.Helper()
	for _, p := range s.Panels() {
		if p.Name != name {
			continue
		}
		for _, ser := range p.Series() {
			if ser.Label == label {
				var out []float64
				for _, pt := range ser.Points {
					out = append(out, pt.V)
				}
				return out
			}
		}
	}
	return nil
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestGaugeAndHelp(t *testing.T) {
	s := NewStore(10)
	t0 := time.Unix(0, 0)
	ingest(t, s, "# HELP temp Current temperature.\n# TYPE temp gauge\ntemp{room=\"a\"} 21.5\n", t0)
	ingest(t, s, "# HELP temp Current temperature.\n# TYPE temp gauge\ntemp{room=\"a\"} 22\n", t0.Add(time.Second))

	got := values(t, s, "temp", `{room="a"}`)
	if len(got) != 2 || got[0] != 21.5 || got[1] != 22 {
		t.Fatalf("got %v", got)
	}
	if title := s.Panels()[0].Title(); title != "Current temperature." {
		t.Fatalf("title %q", title)
	}
}

func TestCounterRateAndReset(t *testing.T) {
	s := NewStore(10)
	t0 := time.Unix(0, 0)
	for i, v := range []string{"100", "120", "160", "10"} {
		ingest(t, s, "# TYPE reqs_total counter\nreqs_total "+v+"\n", t0.Add(time.Duration(i)*2*time.Second))
	}
	got := values(t, s, "reqs_total", "")
	want := []float64{10, 20, 5} // 20/2s, 40/2s, reset: 10/2s
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if !approx(got[i], want[i]) {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestHistogramQuantiles(t *testing.T) {
	s := NewStore(10)
	t0 := time.Unix(0, 0)
	h := func(b1, b2, inf int) string {
		return "# TYPE lat histogram\n" +
			"lat_bucket{le=\"0.1\"} " + strconv.Itoa(b1) + "\n" +
			"lat_bucket{le=\"1\"} " + strconv.Itoa(b2) + "\n" +
			"lat_bucket{le=\"+Inf\"} " + strconv.Itoa(inf) + "\n" +
			"lat_sum 0\nlat_count " + strconv.Itoa(inf) + "\n"
	}
	ingest(t, s, h(0, 0, 0), t0)
	// 100 new observations, all between 0.1 and 1.
	ingest(t, s, h(0, 100, 100), t0.Add(time.Second))
	// No new observations.
	ingest(t, s, h(0, 100, 100), t0.Add(2*time.Second))

	p50 := values(t, s, "lat", "p50")
	if len(p50) != 2 || !approx(p50[0], 0.55) || !math.IsNaN(p50[1]) {
		t.Fatalf("p50 = %v", p50)
	}
	p99 := values(t, s, "lat", "p99")
	if len(p99) != 2 || !approx(p99[0], 0.1+0.9*0.99) {
		t.Fatalf("p99 = %v", p99)
	}
}

func TestSummary(t *testing.T) {
	s := NewStore(10)
	ingest(t, s, "# TYPE rpc summary\nrpc{quantile=\"0.5\"} 3\nrpc{quantile=\"0.99\"} 9\nrpc_sum 30\nrpc_count 10\n", time.Unix(0, 0))
	if got := values(t, s, "rpc", "p99"); len(got) != 1 || got[0] != 9 {
		t.Fatalf("p99 = %v", got)
	}
}

func TestHistoryBoundAndPrune(t *testing.T) {
	s := NewStore(3)
	t0 := time.Unix(0, 0)
	ingest(t, s, "# TYPE g gauge\ng{a=\"x\"} 1\n", t0)
	for i := 1; i <= 5; i++ {
		ingest(t, s, "# TYPE g gauge\ng{a=\"y\"} 1\n", t0.Add(time.Duration(i)*time.Second))
	}
	if got := values(t, s, "g", `{a="y"}`); len(got) != 3 {
		t.Fatalf("expected 3 retained points, got %d", len(got))
	}
	if got := values(t, s, "g", `{a="x"}`); got != nil {
		t.Fatalf("stale series not pruned: %v", got)
	}
}

func TestBucketQuantileInfBucket(t *testing.T) {
	bounds := []float64{1, 2, math.Inf(1)}
	if got := BucketQuantile(0.99, bounds, []float64{0, 0, 10}); got != 2 {
		t.Fatalf("got %v, want highest finite bound", got)
	}
}

func TestInferUnit(t *testing.T) {
	for name, want := range map[string]string{
		"node_memory_MemFree_bytes":          "bytes",
		"node_network_receive_bytes_total":   "bytes",
		"http_response_size_bytes":           "bytes",
		"http_requests_total":                "",
		"process_cpu_seconds_total":          "seconds",
		"http_request_duration_seconds":      "seconds",
		"process_start_time_seconds":         "timestamp",
		"job_last_success_timestamp_seconds": "timestamp",
		"backup_last_run_timestamp":          "timestamp",
		"go_memstats_bytes_allocated_count":  "",
	} {
		if got := InferUnit(name, ""); got != want {
			t.Errorf("InferUnit(%q) = %q, want %q", name, got, want)
		}
	}
	if got := InferUnit("foo", "bytes"); got != "bytes" {
		t.Errorf("explicit unit ignored: %q", got)
	}
}

func TestTimeUnits(t *testing.T) {
	s := NewStore(10)
	t0 := time.Unix(1_000_000, 0)
	text := "# TYPE process_start_time_seconds gauge\nprocess_start_time_seconds 999000\n" +
		"# TYPE never_timestamp_seconds gauge\nnever_timestamp_seconds 0\n" +
		"# TYPE cpu_seconds_total counter\ncpu_seconds_total %d\n"
	ingest(t, s, fmt.Sprintf(text, 0), t0)
	ingest(t, s, fmt.Sprintf(text, 1), t0.Add(2*time.Second))

	if got := values(t, s, "process_start_time_seconds", ""); len(got) != 2 || got[0] != 1000 || got[1] != 1002 {
		t.Fatalf("age = %v, want [1000 1002]", got)
	}
	if got := values(t, s, "never_timestamp_seconds", ""); len(got) != 2 || !math.IsNaN(got[0]) {
		t.Fatalf("zero timestamp = %v, want gaps", got)
	}
	for _, p := range s.Panels() {
		want := map[string]string{"process_start_time_seconds": "timestamp", "never_timestamp_seconds": "timestamp", "cpu_seconds_total": ""}[p.Name]
		if p.Unit != want {
			t.Errorf("%s: unit %q, want %q", p.Name, p.Unit, want)
		}
	}
}

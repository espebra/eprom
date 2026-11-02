package ui

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/espebra/eprom/internal/metrics"
	"github.com/espebra/eprom/internal/scrape"
)

func TestViewRendersPanels(t *testing.T) {
	store := metrics.NewStore(100)
	t0 := time.Unix(0, 0)
	for i := 0; i < 50; i++ {
		text := fmt.Sprintf("# HELP reqs_total Total requests served.\n# TYPE reqs_total counter\n"+
			"reqs_total{code=\"200\"} %d\nreqs_total{code=\"500\"} %d\n"+
			"# HELP temp Temperature.\n# TYPE temp gauge\ntemp %d\n", i*i, i, i%7)
		families, err := scrape.Parse(strings.NewReader(text))
		if err != nil {
			t.Fatal(err)
		}
		store.Ingest(families, t0.Add(time.Duration(i)*time.Second))
	}

	m := New(scrape.New("http://example/metrics", time.Second), store, Options{Interval: time.Second, ChartHeight: 6, MaxSeries: 4})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	view := m.View()

	lines := strings.Split(view, "\n")
	if len(lines) != 30 {
		t.Fatalf("view has %d lines, want 30", len(lines))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w > 80 {
			t.Errorf("line %d is %d wide: %q", i, w, ansi.Strip(l))
		}
	}
	plain := ansi.Strip(view)
	for _, want := range []string{"Total requests served.", "Temperature.", `code="500"`, "rate per second", "[1/2] Total requests served.", "[2/2] Temperature."} {
		if !strings.Contains(plain, want) {
			t.Errorf("view missing %q", want)
		}
	}
	t.Log("\n" + plain)
}

func TestHumanize(t *testing.T) {
	for v, want := range map[float64]string{
		0: "0", 1: "1", 1234: "1.234k", 999.97: "1000", 0.0025: "0.0025", 0.25: "0.25", 0.1234: "0.123", 2.5e9: "2.5G", -1500: "-1.5k",
	} {
		if got := humanize(v); got != want {
			t.Errorf("humanize(%v) = %q, want %q", v, got, want)
		}
	}
}

func TestChartZeroLabel(t *testing.T) {
	s := &metrics.Series{}
	for i := uint64(1); i <= 20; i++ {
		s.Points = append(s.Points, metrics.Point{Seq: i, V: float64(i%3) - 1}) // -1, 0, 1
	}
	rows := chart([]*metrics.Series{s}, 20, 60, 8, "")
	var labels []string
	for _, r := range rows {
		labels = append(labels, strings.TrimSpace(strings.SplitN(ansi.Strip(r), "├", 2)[0]))
	}
	got := strings.Join(labels, ",")
	if want := "1,,,0,,,,-1.214"; got != want {
		t.Fatalf("labels = %q, want %q", got, want)
	}
	for _, r := range rows {
		t.Log(ansi.Strip(r))
	}
}

func TestAlignZero(t *testing.T) {
	for _, tc := range []struct{ lo, hi float64 }{{-1, 1}, {-1, 100}, {-100, 1}, {-0.3, 7}, {-5, 5.5}} {
		for height := 2; height <= 12; height++ {
			lo, hi := alignZero(tc.lo, tc.hi, height)
			if lo > tc.lo || hi < tc.hi {
				t.Fatalf("alignZero(%v, %v, %d) = %v, %v narrowed the range", tc.lo, tc.hi, height, lo, hi)
			}
			n := float64(height*dotsY - 1)
			y := n * hi / (hi - lo)
			if off := int(math.Round(y)) % dotsY; math.Abs(y-math.Round(y)) > 1e-9 || (off != 1 && off != 2) {
				t.Fatalf("alignZero(%v, %v, %d): zero at dot %v, want a middle dot", tc.lo, tc.hi, height, y)
			}
		}
	}
}

func TestToggleAllSeries(t *testing.T) {
	store := metrics.NewStore(10)
	var b strings.Builder
	b.WriteString("# HELP g Many series.\n# TYPE g gauge\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "g{id=\"%02d\"} %d\n", i, i)
	}
	families, err := scrape.Parse(strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	store.Ingest(families, time.Unix(0, 0))

	m := New(scrape.New("http://example/metrics", time.Second), store, Options{Interval: time.Second, ChartHeight: 4, MaxSeries: 6, AllSeries: 25})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 60})
	count := func() int { return strings.Count(ansi.Strip(m.View()), " ● ") }

	if got := count(); got != 6 {
		t.Fatalf("default: %d series in legend, want 6", got)
	}
	if !strings.Contains(ansi.Strip(m.View()), "24 more series not shown (press a to show up to 25)") {
		t.Fatal("missing hint about the a key")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if got := count(); got != 25 {
		t.Fatalf("expanded: %d series in legend, want 25", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if got := count(); got != 6 {
		t.Fatalf("toggled back: %d series in legend, want 6", got)
	}
}

func TestFormatBytes(t *testing.T) {
	for v, want := range map[float64]string{
		0: "0B", 512: "512B", 1500: "1.5KB", 2.5e6: "2.5MB", 8.1e9: "8.1GB", 3e12: "3TB", 0.5: "0.5B", -2048: "-2.048KB",
	} {
		if got := formatValue(v, "bytes"); got != want {
			t.Errorf("formatValue(%v, bytes) = %q, want %q", v, got, want)
		}
	}
}

func TestBytesPanel(t *testing.T) {
	store := metrics.NewStore(10)
	t0 := time.Unix(0, 0)
	for i, v := range []string{"0", "3000000"} {
		families, err := scrape.Parse(strings.NewReader(
			"# HELP rx_bytes_total Bytes received.\n# TYPE rx_bytes_total counter\nrx_bytes_total " + v + "\n" +
				"# HELP mem_bytes Memory used.\n# TYPE mem_bytes gauge\nmem_bytes 4.2e9\n"))
		if err != nil {
			t.Fatal(err)
		}
		store.Ingest(families, t0.Add(time.Duration(i)*time.Second))
	}
	m := New(scrape.New("http://example/metrics", time.Second), store, Options{Interval: time.Second, ChartHeight: 4, MaxSeries: 6})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	plain := ansi.Strip(m.View())
	for _, want := range []string{"now 4.2GB  min 4.2GB", "4.62GB├", "now 3MB/s", "counter · rate per second · bytes"} {
		if !strings.Contains(plain, want) {
			t.Errorf("view missing %q:\n%s", want, plain)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	for v, want := range map[float64]string{
		0: "0s", 0.25: "250ms", 0.0000015: "1.5µs", 12.5: "12.5s", 59.99: "59.99s", 210: "3.5min",
		-740: "-12.3min", 15120: "4.2h", 267840: "3.1d", 86400 * 400: "400d",
	} {
		if got := formatValue(v, "seconds"); got != want {
			t.Errorf("formatValue(%v, seconds) = %q, want %q", v, got, want)
		}
	}
}

func TestTimestampPanel(t *testing.T) {
	store := metrics.NewStore(10)
	now := time.Unix(1_000_000, 0)
	families, err := scrape.Parse(strings.NewReader(
		"# HELP start_time_seconds Start time.\n# TYPE start_time_seconds gauge\nstart_time_seconds 990000\n" +
			"# HELP expiry_timestamp_seconds Cert expiry.\n# TYPE expiry_timestamp_seconds gauge\nexpiry_timestamp_seconds 1172800\n"))
	if err != nil {
		t.Fatal(err)
	}
	store.Ingest(families, now)
	m := New(scrape.New("http://example/metrics", time.Second), store, Options{Interval: time.Second, ChartHeight: 4, MaxSeries: 6})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	plain := ansi.Strip(m.View())
	for _, want := range []string{"now 2.78h ago", "now in 2d", "gauge · timestamp shown as age"} {
		if !strings.Contains(plain, want) {
			t.Errorf("view missing %q:\n%s", want, plain)
		}
	}
}

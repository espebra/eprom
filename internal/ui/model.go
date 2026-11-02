// Package ui implements the terminal user interface.
package ui

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	dto "github.com/prometheus/client_model/go"

	"github.com/espebra/eprom/internal/metrics"
	"github.com/espebra/eprom/internal/scrape"
)

// Options configures the UI.
type Options struct {
	Interval    time.Duration
	ChartHeight int // rows per chart
	MaxSeries   int // series drawn per panel
	AllSeries   int // series drawn per panel when expanded with 'a'
}

type scrapeMsg struct {
	families map[string]*dto.MetricFamily
	err      error
	at       time.Time
	took     time.Duration
}

type tickMsg struct{}

// Model is the Bubble Tea model.
type Model struct {
	opts    Options
	scraper *scrape.Scraper
	store   *metrics.Store

	width, height int
	offset        int // index of the first visible panel

	filter    string
	filtering bool
	expanded  bool // show up to AllSeries series per panel

	scrapes  int
	lastAt   time.Time
	lastTook time.Duration
	lastErr  error
}

// New creates the UI model.
func New(s *scrape.Scraper, store *metrics.Store, opts Options) *Model {
	if opts.ChartHeight < 2 {
		opts.ChartHeight = 2
	}
	if opts.MaxSeries < 1 {
		opts.MaxSeries = 1
	}
	if opts.AllSeries < opts.MaxSeries {
		opts.AllSeries = opts.MaxSeries
	}
	return &Model{opts: opts, scraper: s, store: store}
}

func (m *Model) Init() tea.Cmd { return m.scrape }

func (m *Model) scrape() tea.Msg {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), m.opts.Interval)
	defer cancel()
	families, err := m.scraper.Scrape(ctx)
	return scrapeMsg{families: families, err: err, at: start, took: time.Since(start)}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case scrapeMsg:
		m.lastErr = msg.err
		if msg.err == nil {
			m.store.Ingest(msg.families, msg.at)
			m.scrapes++
			m.lastAt, m.lastTook = msg.at, msg.took
		} else if m.scrapes > 0 {
			m.store.Gap()
		}
		// Schedule the next scrape on the next wall-clock multiple of the
		// interval, e.g. at the start of each whole second for -interval 1s.
		return m, tea.Every(m.opts.Interval, func(time.Time) tea.Msg { return tickMsg{} })

	case tickMsg:
		return m, m.scrape

	case tea.KeyMsg:
		return m, m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	if m.filtering {
		switch msg.Type {
		case tea.KeyEnter:
			m.filtering = false
		case tea.KeyEsc:
			m.filtering, m.filter = false, ""
		case tea.KeyBackspace:
			if r := []rune(m.filter); len(r) > 0 {
				m.filter = string(r[:len(r)-1])
			}
		case tea.KeyRunes, tea.KeySpace:
			m.filter += string(msg.Runes)
		case tea.KeyCtrlC:
			return tea.Quit
		}
		m.offset = 0
		return nil
	}

	n := len(m.visiblePanels())
	switch msg.String() {
	case "q", "ctrl+c":
		return tea.Quit
	case "j", "down":
		m.offset++
	case "k", "up":
		m.offset--
	case "pgdown", " ", "f":
		m.offset += m.panelsPerPage()
	case "pgup", "b":
		m.offset -= m.panelsPerPage()
	case "g", "home":
		m.offset = 0
	case "G", "end":
		m.offset = n - 1
	case "a":
		m.expanded = !m.expanded
	case "/":
		m.filtering = true
	case "esc":
		m.filter = ""
	}
	m.offset = max(0, min(m.offset, n-1))
	return nil
}

func (m *Model) seriesLimit() int {
	if m.expanded {
		return m.opts.AllSeries
	}
	return m.opts.MaxSeries
}

func (m *Model) panelsPerPage() int {
	// Approximate: title + subtitle + chart + a couple of legend lines + gap.
	return max(1, (m.height-2)/(m.opts.ChartHeight+5))
}

func (m *Model) visiblePanels() []*metrics.Panel {
	all := m.store.Panels()
	if m.filter == "" {
		return all
	}
	f := strings.ToLower(m.filter)
	out := all[:0:0]
	for _, p := range all {
		if strings.Contains(strings.ToLower(p.Name), f) || strings.Contains(strings.ToLower(p.Help), f) {
			out = append(out, p)
		}
	}
	return out
}

func (m *Model) View() string {
	if m.width == 0 {
		return "starting…"
	}
	panels := m.visiblePanels()
	m.offset = max(0, min(m.offset, len(panels)-1))

	body := m.height - 2 // header and footer
	var lines []string
	for i := m.offset; i < len(panels) && len(lines) < body; i++ {
		lines = append(lines, m.renderPanel(panels[i], i+1, len(panels))...)
		lines = append(lines, "")
	}
	if len(panels) == 0 {
		lines = append(lines, dimStyle.Render(m.emptyMessage()))
	}
	if len(lines) > body {
		lines = lines[:body]
	}
	for len(lines) < body {
		lines = append(lines, "")
	}

	out := make([]string, 0, m.height)
	out = append(out, m.header(len(panels)))
	out = append(out, lines...)
	out = append(out, m.footer())
	return strings.Join(out, "\n")
}

func (m *Model) emptyMessage() string {
	switch {
	case m.filter != "":
		return fmt.Sprintf("no metrics match %q", m.filter)
	case m.scrapes == 0 && m.lastErr == nil:
		return "fetching " + m.scraper.URL + "…"
	case m.scrapes == 0:
		return "no data yet"
	}
	return "endpoint returned no metrics"
}

func (m *Model) header(n int) string {
	status := "waiting for first scrape"
	if m.scrapes > 0 {
		status = fmt.Sprintf("scrapes %d · last %s (%s)", m.scrapes,
			m.lastAt.Format("15:04:05"), roundDuration(m.lastTook))
	}
	pos := "0/0"
	if n > 0 {
		pos = fmt.Sprintf("%d/%d", m.offset+1, n)
	}
	h := fmt.Sprintf(" eprom · %s · every %s · %s · metric %s ",
		m.scraper.URL, m.opts.Interval, status, pos)
	h = ansi.Truncate(h, m.width, "…")
	return headerStyle.Render(h + strings.Repeat(" ", max(0, m.width-ansi.StringWidth(h))))
}

func (m *Model) footer() string {
	if m.lastErr != nil {
		return errorStyle.Render(ansi.Truncate(" error: "+m.lastErr.Error(), m.width, "…"))
	}
	if m.filtering {
		return ansi.Truncate(" filter: "+m.filter+"█  (enter: apply, esc: clear)", m.width, "…")
	}
	help := fmt.Sprintf(" q quit · j/k scroll · space/b page · g/G top/bottom · / filter · a series (%d)", m.seriesLimit())
	if m.filter != "" {
		help += fmt.Sprintf(" · filter %q (esc clears)", m.filter)
	}
	return dimStyle.Render(ansi.Truncate(help, m.width, "…"))
}

func (m *Model) renderPanel(p *metrics.Panel, num, total int) []string {
	w := m.width
	series := p.Series()

	kind := p.TypeName()
	switch p.Type {
	case dto.MetricType_COUNTER:
		kind += " · rate per second"
	case dto.MetricType_HISTOGRAM:
		kind += " · estimated quantiles per interval"
	}
	switch p.Unit {
	case "":
	case "timestamp":
		kind += " · timestamp shown as age"
	default:
		kind += " · " + p.Unit
	}
	sub := fmt.Sprintf("%s · %s · %d series", p.Name, kind, len(series))

	lines := []string{
		ansi.Truncate(dimStyle.Render(fmt.Sprintf("[%d/%d] ", num, total))+titleStyle.Render(p.Title()), w, "…"),
		dimStyle.Render(ansi.Truncate(sub, w, "…")),
	}

	if len(series) == 0 {
		msg := "  waiting for data"
		if p.Type != dto.MetricType_GAUGE && p.Type != dto.MetricType_UNTYPED {
			msg += " (needs two scrapes)"
		}
		return append(lines, dimStyle.Render(msg))
	}

	shown := series[:min(len(series), m.seriesLimit())]
	lines = append(lines, chart(shown, m.store.Seq(), w, m.opts.ChartHeight, p.Unit)...)
	for i, s := range shown {
		lines = append(lines, legend(i, p, s, w))
	}
	if extra := len(series) - len(shown); extra > 0 {
		msg := fmt.Sprintf("  … %d more series not shown", extra)
		if !m.expanded && m.opts.AllSeries > m.opts.MaxSeries {
			msg += fmt.Sprintf(" (press a to show up to %d)", m.opts.AllSeries)
		}
		lines = append(lines, dimStyle.Render(msg))
	}
	return lines
}

func legend(i int, p *metrics.Panel, s *metrics.Series, width int) string {
	label := s.Label
	if label == "" {
		label = p.Name
	}
	cur, lo, hi := math.NaN(), math.Inf(1), math.Inf(-1)
	if last, ok := s.Last(); ok {
		cur = last.V
	}
	for _, pt := range s.Points {
		if isFinite(pt.V) {
			lo, hi = math.Min(lo, pt.V), math.Max(hi, pt.V)
		}
	}
	if math.IsInf(lo, 1) {
		lo, hi = math.NaN(), math.NaN()
	}
	f := func(v float64) string {
		s := formatValue(v, p.Unit)
		switch {
		case math.IsNaN(v):
		case p.Rate:
			s += "/s"
		case p.Unit == "timestamp" && v < 0:
			s = "in " + formatValue(-v, p.Unit)
		case p.Unit == "timestamp":
			s += " ago"
		}
		return s
	}
	// Names are dim; values take the series color.
	color := seriesStyle(i)
	stats := dimStyle.Render("  now ") + color.Render(f(cur)) +
		dimStyle.Render("  min ") + color.Render(f(lo)) +
		dimStyle.Render("  max ") + color.Render(f(hi))
	labelWidth := max(10, width-4-ansi.StringWidth(stats))
	return color.Render(" ● ") + ansi.Truncate(label, labelWidth, "…") + stats
}

// roundDuration rounds to milliseconds, or microseconds for fast scrapes.
func roundDuration(d time.Duration) time.Duration {
	if d < time.Millisecond {
		return d.Round(time.Microsecond)
	}
	return d.Round(time.Millisecond)
}

// Command eprom periodically scrapes a Prometheus metrics endpoint and plots
// the metrics in the terminal. History is kept in memory only.
package main

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/espebra/eprom/internal/metrics"
	"github.com/espebra/eprom/internal/scrape"
	"github.com/espebra/eprom/internal/ui"
)

// version is set at build time with -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	interval := flag.Duration("interval", 2*time.Second, "scrape interval")
	history := flag.Int("history", 1000, "number of samples kept per series")
	height := flag.Int("height", 8, "chart height in rows")
	maxSeries := flag.Int("series", 6, "maximum number of series drawn per metric")
	allSeries := flag.Int("all-series", 25, "maximum number of series drawn per metric after pressing 'a'")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [flags] <metrics-url>\n\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(), "Example: %s -interval 5s http://localhost:9100/metrics\n\nFlags:\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Println("eprom", version)
		return
	}
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	target, err := normalizeURL(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "eprom:", err)
		os.Exit(2)
	}
	if *interval <= 0 {
		fmt.Fprintln(os.Stderr, "eprom: -interval must be positive")
		os.Exit(2)
	}

	model := ui.New(scrape.New(target, *interval), metrics.NewStore(*history), ui.Options{
		Interval:    *interval,
		ChartHeight: *height,
		MaxSeries:   *maxSeries,
		AllSeries:   *allSeries,
	})
	if _, err := tea.NewProgram(model, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "eprom:", err)
		os.Exit(1)
	}
}

// normalizeURL adds a missing http:// scheme and validates the URL.
func normalizeURL(raw string) (string, error) {
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("URL %q has no host", raw)
	}
	return u.String(), nil
}

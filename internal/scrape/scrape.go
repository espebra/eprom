// Package scrape fetches and parses a Prometheus metrics endpoint.
package scrape

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

// acceptHeader asks for the classic text exposition format, which every
// Prometheus client library supports.
const acceptHeader = "text/plain;version=0.0.4;q=1,*/*;q=0.1"

// Scraper fetches metrics from a single URL.
type Scraper struct {
	URL    string
	Client *http.Client
}

// New returns a scraper for url with the given request timeout.
//
// Responses are fetched gzip-compressed when the server supports it: Go's
// transport adds "Accept-Encoding: gzip" and decompresses transparently, as
// long as the request does not set Accept-Encoding itself.
func New(url string, timeout time.Duration) *Scraper {
	return &Scraper{URL: url, Client: &http.Client{Timeout: timeout}}
}

// Scrape fetches and parses the endpoint.
func (s *Scraper) Scrape(ctx context.Context) (map[string]*dto.MetricFamily, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", acceptHeader)
	req.Header.Set("User-Agent", "eprom")

	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil, fmt.Errorf("unexpected status %s", resp.Status)
	}
	return Parse(resp.Body)
}

// Parse parses the Prometheus text exposition format.
func Parse(r io.Reader) (map[string]*dto.MetricFamily, error) {
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(r)
	if err != nil {
		return nil, fmt.Errorf("parse metrics: %w", err)
	}
	return families, nil
}

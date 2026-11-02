package scrape

import (
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestScrape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Accept"), "text/plain") {
			t.Errorf("unexpected Accept header %q", r.Header.Get("Accept"))
		}
		w.Write([]byte("# HELP up Whether the target is up.\n# TYPE up gauge\nup 1\n"))
	}))
	defer srv.Close()

	families, err := New(srv.URL, time.Second).Scrape(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := families["up"].GetHelp(); got != "Whether the target is up." {
		t.Fatalf("help = %q", got)
	}
}

func TestScrapeStatusError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	if _, err := New(srv.URL, time.Second).Scrape(context.Background()); err == nil {
		t.Fatal("expected error for 404")
	}
}

func TestScrapeGzip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			t.Errorf("gzip not requested, Accept-Encoding = %q", r.Header.Get("Accept-Encoding"))
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		gz.Write([]byte("# HELP up Whether the target is up.\n# TYPE up gauge\nup 1\n"))
		gz.Close()
	}))
	defer srv.Close()

	families, err := New(srv.URL, time.Second).Scrape(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := families["up"].GetMetric()[0].GetGauge().GetValue(); got != 1 {
		t.Fatalf("up = %v, want 1", got)
	}
}

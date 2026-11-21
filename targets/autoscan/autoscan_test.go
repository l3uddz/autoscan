package autoscan

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autoscan"
)

func TestScanDoesNotLogURLPassword(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	var buf bytes.Buffer
	orig := log.Logger
	log.Logger = zerolog.New(&buf)
	defer func() { log.Logger = orig }()

	proxyURL := strings.Replace(srv.URL, "http://", "http://user:secret@", 1)
	target, err := New(t.Context(), Config{URL: proxyURL, Verbosity: "trace"})
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}

	if err := target.Available(t.Context()); err == nil {
		t.Fatal("Available() returned nil error for a 404 response")
	}
	fail.Store(false)
	if err := target.Scan(t.Context(), autoscan.Scan{Folder: "/mnt/tv/Show"}); err != nil {
		t.Fatalf("Scan() returned error: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"Request failed", "Scan moved to target", "user:xxxxx@"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in log output, got %q", want, out)
		}
	}
	if strings.Contains(out, "secret") {
		t.Errorf("log output contains the URL password: %s", out)
	}
}

func TestConfigValidate(t *testing.T) {
	if err := (Config{URL: "http://autoscan:3030"}).Validate(); err != nil {
		t.Errorf("Validate() = %v for a valid configuration", err)
	}
	if err := (Config{URL: "autoscan:3030"}).Validate(); err == nil {
		t.Error("Validate() = nil for a URL without a scheme")
	}
	if err := (Config{URL: "http://autoscan:3030", Rewrite: []autoscan.Rewrite{{From: "("}}}).Validate(); err == nil {
		t.Error("Validate() = nil for an invalid rewrite")
	}
}

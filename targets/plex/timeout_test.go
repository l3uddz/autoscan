package plex

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan"
)

func TestRequestsTimeOut(t *testing.T) {
	// The server never answers until the test ends.
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-done
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(done) })

	api := newAPIClient(srv.URL, "token", zerolog.Nop())
	if api.client.Timeout != autoscan.TargetTimeout {
		t.Fatalf("HTTP client timeout = %v, want %v", api.client.Timeout, autoscan.TargetTimeout)
	}

	api.client.Timeout = 50 * time.Millisecond
	if _, err := api.Version(); !errors.Is(err, autoscan.ErrTargetUnavailable) {
		t.Errorf("Got error %v, want %v", err, autoscan.ErrTargetUnavailable)
	}
}

func TestVersionBodyTimeoutIsUnavailable(t *testing.T) {
	// The server sends the headers, then never sends the body.
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-done
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(done) })

	api := newAPIClient(srv.URL, "token", zerolog.Nop())
	api.client.Timeout = 50 * time.Millisecond

	if _, err := api.Version(); !errors.Is(err, autoscan.ErrTargetUnavailable) {
		t.Errorf("Got error %v, want %v", err, autoscan.ErrTargetUnavailable)
	}
}

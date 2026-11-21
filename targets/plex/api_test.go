package plex

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/targets/internal/media"
)

func TestScanRefreshesLibraryPath(t *testing.T) {
	var gotPath, gotQuery, gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("path")
		gotToken = r.Header.Get("X-Plex-Token")
	}))
	defer srv.Close()

	api, err := newAPIClient(srv.URL, "token", zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if err := api.Scan(t.Context(), "/mnt/tv/Show", []media.Library{{ID: 5}}); err != nil {
		t.Fatalf("Scan returned %v", err)
	}

	if gotPath != "/library/sections/5/refresh" || gotQuery != "/mnt/tv/Show" || gotToken != "token" {
		t.Errorf("Request = %s?path=%s with token %q, want /library/sections/5/refresh?path=/mnt/tv/Show with token \"token\"", gotPath, gotQuery, gotToken)
	}
}

func TestNewReadsVersionAndLibraries(t *testing.T) {
	var refreshed []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Write([]byte(`{"MediaContainer":{"Version":"1.32.0"}}`))
		case "/library/sections":
			w.Write([]byte(`{"MediaContainer":{"Directory":[{"key":"5","title":"TV","Location":[{"path":"/mnt/tv"}]},{"key":"6","title":"TV 4K","Location":[{"path":"/mnt/tv"}]}]}}`))
		default:
			refreshed = append(refreshed, r.URL.Path)
		}
	}))
	defer srv.Close()

	got, err := New(t.Context(), Config{URL: srv.URL, Token: "token"})
	if err != nil {
		t.Fatalf("New returned %v", err)
	}

	if err := got.Scan(t.Context(), autoscan.Scan{Folder: "/mnt/tv/Show"}); err != nil {
		t.Fatalf("Scan returned %v", err)
	}
	if want := []string{"/library/sections/5/refresh", "/library/sections/6/refresh"}; !reflect.DeepEqual(refreshed, want) {
		t.Errorf("refreshed %v, want %v", refreshed, want)
	}
}

func TestScanReportsFolderInNoLibrary(t *testing.T) {
	var refreshed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Write([]byte(`{"MediaContainer":{"Version":"1.32.0"}}`))
		case "/library/sections":
			w.Write([]byte(`{"MediaContainer":{"Directory":[{"key":"5","title":"TV","Location":[{"path":"/mnt/tv"}]}]}}`))
		default:
			refreshed = true
		}
	}))
	defer srv.Close()

	target, err := New(t.Context(), Config{URL: srv.URL, Token: "token"})
	if err != nil {
		t.Fatalf("New returned %v", err)
	}

	if err := target.Scan(t.Context(), autoscan.Scan{Folder: "/mnt/movies/Film"}); !errors.Is(err, autoscan.ErrNoLibrary) {
		t.Errorf("Scan returned %v, want %v", err, autoscan.ErrNoLibrary)
	}
	if refreshed {
		t.Error("Scan sent a refresh for a folder in no library")
	}
}

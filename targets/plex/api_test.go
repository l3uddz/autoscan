package plex

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"
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
	if err := api.Scan("/mnt/tv/Show", 5); err != nil {
		t.Fatalf("Scan returned %v", err)
	}

	if gotPath != "/library/sections/5/refresh" || gotQuery != "/mnt/tv/Show" || gotToken != "token" {
		t.Errorf("Request = %s?path=%s with token %q, want /library/sections/5/refresh?path=/mnt/tv/Show with token \"token\"", gotPath, gotQuery, gotToken)
	}
}

func TestNewReadsVersionAndLibraries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Write([]byte(`{"MediaContainer":{"Version":"1.32.0"}}`))
		case "/library/sections":
			w.Write([]byte(`{"MediaContainer":{"Directory":[{"key":"5","title":"TV","Location":[{"path":"/mnt/tv"}]}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	got, err := New(Config{URL: srv.URL, Token: "token"})
	if err != nil {
		t.Fatalf("New returned %v", err)
	}

	libs, err := got.(*target).libraries.Matching("/mnt/tv/Show")
	if err != nil {
		t.Fatalf("Matching returned %v", err)
	}
	if len(libs) != 1 || libs[0].ID != 5 || libs[0].Name != "TV" || libs[0].Path != "/mnt/tv/" {
		t.Errorf("Libraries = %+v, want one TV library with ID 5 at /mnt/tv/", libs)
	}
}

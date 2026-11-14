package media

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan"
)

func TestNewLibraryAddsTrailingSlash(t *testing.T) {
	for path, want := range map[string]string{"/mnt/tv": "/mnt/tv/", "/mnt/tv/": "/mnt/tv/", "": ""} {
		if got := NewLibrary(1, "TV", path).Path; got != want {
			t.Errorf("NewLibrary path %q = %q, want %q", path, got, want)
		}
	}
}

func TestMatchingReturnsEveryLibrary(t *testing.T) {
	libraries := []Library{
		NewLibrary(1, "TV", "/mnt/tv"),
		NewLibrary(2, "TV 4K", "/mnt/tv"),
		NewLibrary(3, "TV 2", "/mnt/tv2"),
	}

	got := Names(Matching(libraries, "/mnt/tv/Show/Season 1"))
	if want := []string{"TV", "TV 4K"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Matching = %v, want %v", got, want)
	}

	if got := Matching(libraries, "/mnt/movies/Film"); len(got) != 0 {
		t.Errorf("Matching = %v, want no libraries", got)
	}
}

func TestNewUpdatedRequest(t *testing.T) {
	req, err := NewUpdatedRequest("http://jellyfin:8096/", "/mnt/tv/Show", "Modified")
	if err != nil {
		t.Fatal(err)
	}

	if req.Method != "POST" || req.URL.String() != "http://jellyfin:8096/Library/Media/Updated" {
		t.Errorf("Request = %s %s, want POST http://jellyfin:8096/Library/Media/Updated", req.Method, req.URL)
	}

	var body map[string][]map[string]string
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	want := map[string][]map[string]string{"Updates": {{"path": "/mnt/tv/Show", "updateType": "Modified"}}}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("Body = %v, want %v", body, want)
	}
}

type fakeAPI struct {
	err   error
	paths []string
}

func (f *fakeAPI) Available() error { return f.err }

func (f *fakeAPI) Scan(path string) error {
	f.paths = append(f.paths, path)
	return f.err
}

func TestPathTargetScan(t *testing.T) {
	rewrite, err := autoscan.NewRewriter([]autoscan.Rewrite{{From: "^/data/", To: "/mnt/"}})
	if err != nil {
		t.Fatal(err)
	}
	libraries := []Library{NewLibrary(0, "TV", "/mnt/tv"), NewLibrary(0, "TV 4K", "/mnt/tv")}

	type Test struct {
		Name   string
		Folder string
		APIErr error
		Want   []string
	}

	var testCases = []Test{
		{"Sends one request for a folder in two libraries", "/data/tv/Show", nil, []string{"/mnt/tv/Show"}},
		{"Sends nothing for a folder in no library", "/data/movies/Film", nil, nil},
		{"Returns the API error", "/data/tv/Show", autoscan.ErrTargetUnavailable, []string{"/mnt/tv/Show"}},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			api := &fakeAPI{err: tc.APIErr}
			target := NewPathTarget(zerolog.Nop(), rewrite, libraries, api)

			err := target.Scan(autoscan.Scan{Folder: tc.Folder})
			if !errors.Is(err, tc.APIErr) {
				t.Errorf("Scan returned %v, want %v", err, tc.APIErr)
			}
			if !reflect.DeepEqual(api.paths, tc.Want) {
				t.Errorf("API got paths %v, want %v", api.paths, tc.Want)
			}
		})
	}
}

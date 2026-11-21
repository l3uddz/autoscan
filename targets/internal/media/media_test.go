package media

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/targets/internal/httpapi"
)

func TestNewLibraryAddsTrailingSlash(t *testing.T) {
	for path, want := range map[string]string{"/mnt/tv": "/mnt/tv/", "/mnt/tv/": "/mnt/tv/", "": ""} {
		if got := NewLibrary(1, "TV", path).Path; got != want {
			t.Errorf("NewLibrary path %q = %q, want %q", path, got, want)
		}
	}
}

func TestNewLibraryKeepsWindowsSeparator(t *testing.T) {
	for path, want := range map[string]string{`C:\TV`: `C:\TV\`, `\\server\tv\`: `\\server\tv\`, "C:/TV": "C:/TV/"} {
		if got := NewLibrary(1, "TV", path).Path; got != want {
			t.Errorf("NewLibrary path %q = %q, want %q", path, got, want)
		}
	}
}

func TestMatchingFindsWindowsLibrary(t *testing.T) {
	libraries := []Library{NewLibrary(1, "TV", `C:\TV`), NewLibrary(2, "TV 2", `C:\TV2`)}

	got := Names(matching(libraries, `C:\TV\Show\Season 1`))
	if want := []string{"TV"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Matching = %v, want %v", got, want)
	}
}

func TestMatchingFindsLibraryRoot(t *testing.T) {
	libraries := []Library{NewLibrary(1, "TV", "/mnt/tv")}

	if got := Names(matching(libraries, "/mnt/tv")); !reflect.DeepEqual(got, []string{"TV"}) {
		t.Errorf("Matching = %v, want [TV]", got)
	}
}

func TestMatchingReturnsEveryLibrary(t *testing.T) {
	libraries := []Library{
		NewLibrary(1, "TV", "/mnt/tv"),
		NewLibrary(2, "TV 4K", "/mnt/tv"),
		NewLibrary(3, "TV 2", "/mnt/tv2"),
	}

	got := Names(matching(libraries, "/mnt/tv/Show/Season 1"))
	if want := []string{"TV", "TV 4K"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Matching = %v, want %v", got, want)
	}

	if got := matching(libraries, "/mnt/movies/Film"); len(got) != 0 {
		t.Errorf("Matching = %v, want no libraries", got)
	}
}

func TestSendUpdated(t *testing.T) {
	var method, path, contentType string
	var body map[string][]map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, contentType = r.Method, r.URL.Path, r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("Decoding body: %v", err)
		}
	}))
	defer srv.Close()

	c, err := httpapi.New(httpapi.Config{BaseURL: srv.URL + "/", Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	if err := SendUpdated(t.Context(), c, nil, "/mnt/tv/Show", "Modified"); err != nil {
		t.Fatal(err)
	}

	if method != http.MethodPost || path != "/Library/Media/Updated" || contentType != "application/json" {
		t.Errorf("Request = %s %s (%s), want POST /Library/Media/Updated (application/json)", method, path, contentType)
	}
	want := map[string][]map[string]string{"Updates": {{"path": "/mnt/tv/Show", "updateType": "Modified"}}}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("Body = %v, want %v", body, want)
	}
}

type fakeAPI struct {
	libraries []Library
	err       error
	paths     []string
}

func (f *fakeAPI) Available(context.Context) error { return f.err }

func (f *fakeAPI) Libraries(context.Context) ([]Library, error) { return f.libraries, nil }

func (f *fakeAPI) Scan(_ context.Context, path string, _ []Library) error {
	f.paths = append(f.paths, path)
	return f.err
}

func TestTargetScan(t *testing.T) {
	config := Config{Rewrite: []autoscan.Rewrite{{From: "^/data/", To: "/mnt/"}}}
	libraries := []Library{NewLibrary(0, "TV", "/mnt/tv"), NewLibrary(0, "TV 4K", "/mnt/tv")}

	type Test struct {
		Name    string
		Folder  string
		APIErr  error
		WantErr error
		Want    []string
	}

	var testCases = []Test{
		{"Sends one request for a folder in two libraries", "/data/tv/Show", nil, nil, []string{"/mnt/tv/Show"}},
		{"Sends nothing for a folder in no library", "/data/movies/Film", nil, autoscan.ErrNoLibrary, nil},
		{"Returns the API error", "/data/tv/Show", autoscan.ErrTargetUnavailable, autoscan.ErrTargetUnavailable, []string{"/mnt/tv/Show"}},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			api := &fakeAPI{libraries: libraries, err: tc.APIErr}
			target, err := NewTarget(t.Context(), zerolog.Nop(), config, api)
			if err != nil {
				t.Fatal(err)
			}

			err = target.Scan(t.Context(), autoscan.Scan{Folder: tc.Folder})
			if !errors.Is(err, tc.WantErr) || (err == nil) != (tc.WantErr == nil) {
				t.Errorf("Scan returned %v, want %v", err, tc.WantErr)
			}
			if !reflect.DeepEqual(api.paths, tc.Want) {
				t.Errorf("API got paths %v, want %v", api.paths, tc.Want)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	var testCases = map[string]struct {
		Config  Config
		WantErr bool
	}{
		"Valid":               {Config{URL: "http://plex:32400", Token: "token"}, false},
		"No token":            {Config{URL: "http://plex:32400"}, false},
		"URL without scheme":  {Config{URL: "plex:32400", Token: "token"}, true},
		"Invalid rewrite":     {Config{URL: "http://plex:32400", Rewrite: []autoscan.Rewrite{{From: "(", To: ""}}}, true},
		"Token with quote":    {Config{URL: "http://plex:32400", Token: `to"ken`}, true},
		"Token with new line": {Config{URL: "http://plex:32400", Token: "token\n"}, true},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			if err := tc.Config.Validate(); (err != nil) != tc.WantErr {
				t.Errorf("Validate() = %v, want error %v", err, tc.WantErr)
			}
		})
	}
}

package webhook

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/l3uddz/autoscan"
)

func TestHandler(t *testing.T) {
	currentTime := time.Now()
	now := func() time.Time { return currentTime }
	rewrites := []autoscan.Rewrite{{From: "^/data/", To: "/mnt/"}}

	type Test struct {
		Name        string
		Event       Event
		ParseErr    error
		CallbackErr error
		WantStatus  int
		WantFolders []string
	}

	var testCases = []Test{
		{
			Name:       "Returns 400 when the parser fails",
			ParseErr:   ErrMissingFields,
			WantStatus: http.StatusBadRequest,
		},
		{
			Name:       "Returns 200 on a Test event without a scan",
			Event:      Event{Type: "test", Folders: []string{"/data/tv"}},
			WantStatus: http.StatusOK,
		},
		{
			Name:       "Returns 200 on an event without folders",
			Event:      Event{Type: "Grab"},
			WantStatus: http.StatusOK,
		},
		{
			Name:        "Rewrites folders and removes duplicates in order",
			Event:       Event{Type: "Rename", Folders: []string{"/data/tv/b", "", "/data/tv/a", "/mnt/tv/b"}},
			WantStatus:  http.StatusOK,
			WantFolders: []string{"/mnt/tv/b", "/mnt/tv/a"},
		},
		{
			Name:        "Returns 500 when the processor fails",
			Event:       Event{Type: "Download", Folders: []string{"/data/tv/a"}},
			CallbackErr: errors.New("datastore unavailable"),
			WantStatus:  http.StatusInternalServerError,
			WantFolders: []string{"/mnt/tv/a"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			parse := func(*http.Request) (Event, error) { return tc.Event, tc.ParseErr }
			trigger, err := New(3, rewrites, parse, now)
			if err != nil {
				t.Fatal(err)
			}

			var got []string
			callback := func(scans ...autoscan.Scan) error {
				for _, scan := range scans {
					if scan.Priority != 3 || !scan.Time.Equal(currentTime) {
						t.Errorf("Got scan %+v, want priority 3 and the test time", scan)
					}
					got = append(got, scan.Folder)
				}
				return tc.CallbackErr
			}

			rec := httptest.NewRecorder()
			trigger(callback).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))

			if rec.Code != tc.WantStatus {
				t.Errorf("Status = %d, want %d", rec.Code, tc.WantStatus)
			}
			if !reflect.DeepEqual(got, tc.WantFolders) {
				t.Errorf("Scanned folders = %v, want %v", got, tc.WantFolders)
			}
		})
	}
}

func TestDecodeJSONReturnsDecodeError(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{"))
	var v struct{}
	if err := DecodeJSON(r, &v); err == nil {
		t.Error("DecodeJSON returned nil for invalid JSON")
	}
}

func TestFileEvent(t *testing.T) {
	files := []File{{Path: "/music/Artist/Album/CD 01/01.flac"}, {Path: "/music/Artist/Album/CD 02/01.flac"}}

	event, err := FileEvent("Download", files)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/music/Artist/Album/CD 01", "/music/Artist/Album/CD 02"}; !reflect.DeepEqual(event.Folders, want) {
		t.Errorf("Folders = %v, want %v", event.Folders, want)
	}

	if _, err := FileEvent("Download", nil); !errors.Is(err, ErrMissingFields) {
		t.Errorf("Download without files returned %v, want %v", err, ErrMissingFields)
	}

	if event, err := FileEvent("Grab", files); err != nil || len(event.Folders) != 0 {
		t.Errorf("Grab returned %+v, %v, want no folders and no error", event, err)
	}
}

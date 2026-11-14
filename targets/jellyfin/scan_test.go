package jellyfin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/l3uddz/autoscan"
)

func TestScanSendsOneUpdateForFolderInTwoLibraries(t *testing.T) {
	var mu sync.Mutex
	var updates []map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Library/VirtualFolders":
			w.Write([]byte(`[{"Name":"TV","Locations":["/mnt/tv"]},{"Name":"TV 4K","Locations":["/mnt/tv"]}]`))
		case "/Library/Media/Updated":
			var body struct{ Updates []map[string]string }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("Decoding update body: %v", err)
			}
			mu.Lock()
			updates = append(updates, body.Updates...)
			mu.Unlock()
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	target, err := New(Config{URL: srv.URL, Token: "token"})
	if err != nil {
		t.Fatalf("New returned %v", err)
	}

	if err := target.Scan(autoscan.Scan{Folder: "/mnt/tv/Show"}); err != nil {
		t.Fatalf("Scan returned %v", err)
	}

	want := []map[string]string{{"path": "/mnt/tv/Show", "updateType": "Modified"}}
	if !reflect.DeepEqual(updates, want) {
		t.Errorf("Got updates %v, want %v", updates, want)
	}
}

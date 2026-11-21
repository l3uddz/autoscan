package media

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rs/zerolog"
)

// fakeServer returns the libraries of a media server and counts the loads.
type fakeServer struct {
	libraries []Library
	err       error
	loads     int
}

func (s *fakeServer) load(context.Context) ([]Library, error) {
	s.loads++
	return s.libraries, s.err
}

// newTestList returns a list for server. Each test runs in a synctest
// bubble, so time.Sleep moves the clock of the list.
func newTestList(t *testing.T, server *fakeServer) *LibraryList {
	t.Helper()
	list, err := NewLibraryList(t.Context(), zerolog.Nop(), server.load)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func matchNames(t *testing.T, list *LibraryList, folder string) []string {
	t.Helper()
	libs, err := list.Matching(t.Context(), folder)
	if err != nil {
		t.Fatalf("Matching(%q) returned %v", folder, err)
	}
	return Names(libs)
}

func TestLibraryListFindsLibraryAddedAfterLoad(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := &fakeServer{libraries: []Library{NewLibrary(1, "TV", "/mnt/tv")}}
		list := newTestList(t, server)

		server.libraries = append(server.libraries, NewLibrary(2, "Movies", "/mnt/movies"))
		time.Sleep(minReloadInterval)

		if got, want := matchNames(t, list, "/mnt/movies/Film"), []string{"Movies"}; !reflect.DeepEqual(got, want) {
			t.Errorf("Matching = %v, want %v", got, want)
		}
		if server.loads != 2 {
			t.Errorf("Loads = %d, want 2", server.loads)
		}
	})
}

func TestLibraryListReloadsForUnmatchedFolderOncePerInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := &fakeServer{libraries: []Library{NewLibrary(1, "TV", "/mnt/tv")}}
		list := newTestList(t, server)

		time.Sleep(minReloadInterval)
		matchNames(t, list, "/mnt/movies/Film")
		time.Sleep(folderReloadInterval - time.Second)
		matchNames(t, list, "/mnt/movies/Film")
		if server.loads != 2 {
			t.Fatalf("Loads = %d, want 2: the folder caused a second reload within the interval", server.loads)
		}

		time.Sleep(time.Second)
		matchNames(t, list, "/mnt/movies/Film")
		if server.loads != 3 {
			t.Errorf("Loads = %d, want 3: the folder caused no reload after the interval", server.loads)
		}
	})
}

func TestLibraryListLimitsReloadRate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := &fakeServer{libraries: []Library{NewLibrary(1, "TV", "/mnt/tv")}}
		list := newTestList(t, server)

		matchNames(t, list, "/mnt/movies/Film")
		if server.loads != 1 {
			t.Fatalf("Loads = %d, want 1: the list reloaded within the minimum interval", server.loads)
		}

		// A skipped reload does not record the folder, so it can reload later.
		time.Sleep(minReloadInterval)
		matchNames(t, list, "/mnt/movies/Film")
		if server.loads != 2 {
			t.Errorf("Loads = %d, want 2: the folder caused no reload after the minimum interval", server.loads)
		}
	})
}

func TestLibraryListReturnsReloadError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := &fakeServer{libraries: []Library{NewLibrary(1, "TV", "/mnt/tv")}}
		list := newTestList(t, server)

		server.err = errors.New("unavailable")
		time.Sleep(minReloadInterval)
		if _, err := list.Matching(t.Context(), "/mnt/movies/Film"); !errors.Is(err, server.err) {
			t.Errorf("Matching returned %v, want %v", err, server.err)
		}

		// A failed reload also starts the minimum interval. Until it ends, an
		// unmatched folder gets the same error, so its scan stays queued.
		time.Sleep(time.Second)
		if _, err := list.Matching(t.Context(), "/mnt/other/Film"); !errors.Is(err, server.err) {
			t.Errorf("Matching within the interval returned %v, want %v", err, server.err)
		}
		if server.loads != 2 {
			t.Errorf("Loads = %d, want 2: the list reloaded within the minimum interval after a failure", server.loads)
		}
		if got, want := matchNames(t, list, "/mnt/tv/Show"), []string{"TV"}; !reflect.DeepEqual(got, want) {
			t.Errorf("Matching after a failed reload = %v, want %v", got, want)
		}
	})
}

package media

import (
	"context"
	"errors"
	"reflect"
	"testing"
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

// newTestList returns a list for server with a clock that the test moves.
func newTestList(t *testing.T, server *fakeServer) (*LibraryList, *time.Time) {
	t.Helper()
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	list, err := NewLibraryList(t.Context(), zerolog.Nop(), server.load)
	if err != nil {
		t.Fatal(err)
	}
	list.now = func() time.Time { return clock }
	list.attemptedAt = clock
	return list, &clock
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
	server := &fakeServer{libraries: []Library{NewLibrary(1, "TV", "/mnt/tv")}}
	list, clock := newTestList(t, server)

	server.libraries = append(server.libraries, NewLibrary(2, "Movies", "/mnt/movies"))
	*clock = clock.Add(minReloadInterval)

	if got, want := matchNames(t, list, "/mnt/movies/Film"), []string{"Movies"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Matching = %v, want %v", got, want)
	}
	if server.loads != 2 {
		t.Errorf("Loads = %d, want 2", server.loads)
	}
}

func TestLibraryListReloadsForUnmatchedFolderOncePerInterval(t *testing.T) {
	server := &fakeServer{libraries: []Library{NewLibrary(1, "TV", "/mnt/tv")}}
	list, clock := newTestList(t, server)

	*clock = clock.Add(minReloadInterval)
	matchNames(t, list, "/mnt/movies/Film")
	*clock = clock.Add(folderReloadInterval - time.Second)
	matchNames(t, list, "/mnt/movies/Film")
	if server.loads != 2 {
		t.Fatalf("Loads = %d, want 2: the folder caused a second reload within the interval", server.loads)
	}

	*clock = clock.Add(time.Second)
	matchNames(t, list, "/mnt/movies/Film")
	if server.loads != 3 {
		t.Errorf("Loads = %d, want 3: the folder caused no reload after the interval", server.loads)
	}
}

func TestLibraryListLimitsReloadRate(t *testing.T) {
	server := &fakeServer{libraries: []Library{NewLibrary(1, "TV", "/mnt/tv")}}
	list, clock := newTestList(t, server)

	matchNames(t, list, "/mnt/movies/Film")
	if server.loads != 1 {
		t.Fatalf("Loads = %d, want 1: the list reloaded within the minimum interval", server.loads)
	}

	// A skipped reload does not record the folder, so it can reload later.
	*clock = clock.Add(minReloadInterval)
	matchNames(t, list, "/mnt/movies/Film")
	if server.loads != 2 {
		t.Errorf("Loads = %d, want 2: the folder caused no reload after the minimum interval", server.loads)
	}
}

func TestLibraryListReturnsReloadError(t *testing.T) {
	server := &fakeServer{libraries: []Library{NewLibrary(1, "TV", "/mnt/tv")}}
	list, clock := newTestList(t, server)

	server.err = errors.New("unavailable")
	*clock = clock.Add(minReloadInterval)
	if _, err := list.Matching(t.Context(), "/mnt/movies/Film"); !errors.Is(err, server.err) {
		t.Errorf("Matching returned %v, want %v", err, server.err)
	}

	// A failed reload also starts the minimum interval. Until it ends, an
	// unmatched folder gets the same error, so its scan stays queued.
	*clock = clock.Add(time.Second)
	if _, err := list.Matching(t.Context(), "/mnt/other/Film"); !errors.Is(err, server.err) {
		t.Errorf("Matching within the interval returned %v, want %v", err, server.err)
	}
	if server.loads != 2 {
		t.Errorf("Loads = %d, want 2: the list reloaded within the minimum interval after a failure", server.loads)
	}
	if got, want := matchNames(t, list, "/mnt/tv/Show"), []string{"TV"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Matching after a failed reload = %v, want %v", got, want)
	}
}

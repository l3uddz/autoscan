package inotify

import "testing"

func TestGetPathObject(t *testing.T) {
	type Test struct {
		Name     string
		Paths    []string
		Event    string
		WantPath string
		WantErr  bool
	}

	var testCases = []Test{
		{
			Name:     "Matches a file below the watched path",
			Paths:    []string{"/mnt/tv"},
			Event:    "/mnt/tv/Show/episode.mkv",
			WantPath: "/mnt/tv",
		},
		{
			Name:     "Matches the watched path itself",
			Paths:    []string{"/mnt/tv"},
			Event:    "/mnt/tv",
			WantPath: "/mnt/tv",
		},
		{
			Name:    "Does not match a sibling that shares the prefix",
			Paths:   []string{"/mnt/tv"},
			Event:   "/mnt/tv2/Show/episode.mkv",
			WantErr: true,
		},
		{
			Name:     "Matches the sibling path, not the shorter prefix",
			Paths:    []string{"/mnt/tv", "/mnt/tv2"},
			Event:    "/mnt/tv2/Show/episode.mkv",
			WantPath: "/mnt/tv2",
		},
		{
			Name:     "Prefers the longest nested path when it is listed last",
			Paths:    []string{"/mnt", "/mnt/tv"},
			Event:    "/mnt/tv/Show/episode.mkv",
			WantPath: "/mnt/tv",
		},
		{
			Name:     "Prefers the longest nested path when it is listed first",
			Paths:    []string{"/mnt/tv", "/mnt"},
			Event:    "/mnt/tv/Show/episode.mkv",
			WantPath: "/mnt/tv",
		},
		{
			Name:     "Matches a watched path with a trailing slash",
			Paths:    []string{"/mnt/tv/"},
			Event:    "/mnt/tv/Show/episode.mkv",
			WantPath: "/mnt/tv/",
		},
		{
			Name:     "Matches below the root path",
			Paths:    []string{"/"},
			Event:    "/mnt/tv/Show/episode.mkv",
			WantPath: "/",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			d := daemon{}
			for _, p := range tc.Paths {
				d.paths = append(d.paths, path{Path: p})
			}

			got, err := d.getPathObject(tc.Event)
			if tc.WantErr {
				if err == nil {
					t.Fatalf("Got path %q, want an error", got.Path)
				}
				return
			}

			if err != nil {
				t.Fatalf("Got error %v, want path %q", err, tc.WantPath)
			}

			if got.Path != tc.WantPath {
				t.Errorf("Got path %q, want %q", got.Path, tc.WantPath)
			}
		})
	}
}

package radarr

import (
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/l3uddz/autoscan"
)

func TestHandler(t *testing.T) {
	type Given struct {
		Config  Config
		Fixture string
	}

	type Expected struct {
		Scans      []autoscan.Scan
		StatusCode int
	}

	type Test struct {
		Name     string
		Given    Given
		Expected Expected
	}

	standardConfig := Config{
		Name:     "radarr",
		Priority: 5,
		Rewrite: []autoscan.Rewrite{{
			From: "/Movies/*",
			To:   "/mnt/unionfs/Media/Movies/$1",
		}},
	}

	currentTime := time.Now()
	now = func() time.Time {
		return currentTime
	}

	var testCases = []Test{
		{
			"Download Event",
			Given{
				Config:  standardConfig,
				Fixture: "testdata/interstellar.json",
			},
			Expected{
				StatusCode: 200,
				Scans: []autoscan.Scan{
					{
						Folder:   "/mnt/unionfs/Media/Movies/Interstellar (2014)",
						Priority: 5,
						Time:     currentTime,
					},
				},
			},
		},
		{
			"MovieFileDelete Event",
			Given{
				Config:  standardConfig,
				Fixture: "testdata/movie_file_delete.json",
			},
			Expected{
				StatusCode: 200,
				Scans: []autoscan.Scan{
					{
						Folder:   "/mnt/unionfs/Media/Movies/Tenet (2020)",
						Priority: 5,
						Time:     currentTime,
					},
				},
			},
		},
		{
			"MovieDelete Event",
			Given{
				Config:  standardConfig,
				Fixture: "testdata/movie_delete.json",
			},
			Expected{
				StatusCode: 200,
				Scans: []autoscan.Scan{
					{
						Folder:   "/mnt/unionfs/Media/Movies/Wonder Woman 1984 (2020)",
						Priority: 5,
						Time:     currentTime,
					},
				},
			},
		},
		{
			"Rename Event",
			Given{
				Config:  standardConfig,
				Fixture: "testdata/rename.json",
			},
			Expected{
				StatusCode: 200,
				Scans: []autoscan.Scan{
					{
						Folder:   "/mnt/unionfs/Media/Movies/Deadpool (2016)",
						Priority: 5,
						Time:     currentTime,
					},
				},
			},
		},
		{
			"Rename Event with previous paths",
			Given{
				Config:  standardConfig,
				Fixture: "testdata/rename_previous.json",
			},
			Expected{
				StatusCode: 200,
				Scans: []autoscan.Scan{
					{
						Folder:   "/mnt/unionfs/Media/Movies/Deadpool (2016)",
						Priority: 5,
						Time:     currentTime,
					},
					{
						Folder:   "/mnt/unionfs/Media/Movies/Deadpool",
						Priority: 5,
						Time:     currentTime,
					},
				},
			},
		},
		{
			"Rename Event with Windows previous paths",
			Given{
				Config:  Config{Name: "radarr", Priority: 5},
				Fixture: "testdata/rename_windows.json",
			},
			Expected{
				StatusCode: 200,
				Scans: []autoscan.Scan{
					{
						Folder:   `C:\Movies\Deadpool (2016)`,
						Priority: 5,
						Time:     currentTime,
					},
					{
						Folder:   `C:\Movies\Deadpool`,
						Priority: 5,
						Time:     currentTime,
					},
				},
			},
		},
		{
			"Returns bad request on invalid JSON",
			Given{
				Config:  standardConfig,
				Fixture: "testdata/invalid.json",
			},
			Expected{
				StatusCode: 400,
			},
		},
		{
			"Returns 200 on an unhandled event without emitting a scan",
			Given{
				Config:  standardConfig,
				Fixture: "testdata/grab.json",
			},
			Expected{
				StatusCode: 200,
			},
		},
		{
			"Returns 200 on Test event without emitting a scan",
			Given{
				Config:  standardConfig,
				Fixture: "testdata/test.json",
			},
			Expected{
				StatusCode: 200,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			var got []autoscan.Scan
			callback := func(scans ...autoscan.Scan) error {
				got = scans
				return nil
			}

			handler, err := New(tc.Given.Config, callback)
			if err != nil {
				t.Fatalf("Could not create Radarr Trigger: %v", err)
			}

			server := httptest.NewServer(handler)
			defer server.Close()

			request, err := os.Open(tc.Given.Fixture)
			if err != nil {
				t.Fatalf("Could not open the fixture: %s", tc.Given.Fixture)
			}

			res, err := http.Post(server.URL, "application/json", request)
			if err != nil {
				t.Fatalf("Request failed: %v", err)
			}

			defer res.Body.Close()
			if res.StatusCode != tc.Expected.StatusCode {
				t.Errorf("Status codes do not match: %d vs %d", res.StatusCode, tc.Expected.StatusCode)
			}
			if !reflect.DeepEqual(got, tc.Expected.Scans) {
				t.Errorf("Got scans %v, want %v", got, tc.Expected.Scans)
			}
		})
	}
}

func TestParentFolder(t *testing.T) {
	for file, want := range map[string]string{
		"/Movies/Old/movie.mkv":   "/Movies/Old",
		`C:\Movies\Old\movie.mkv`: `C:\Movies\Old`,
		"/movie.mkv":              "/",
		`C:\movie.mkv`:            `C:\`,
		"C:/movie.mkv":            "C:/",
		"movie.mkv":               "",
		"":                        "",
	} {
		if got := parentFolder(file); got != want {
			t.Errorf("parentFolder(%q) = %q, want %q", file, got, want)
		}
	}
}

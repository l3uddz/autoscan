package manual

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/l3uddz/autoscan"
)

func TestHandler(t *testing.T) {
	type Given struct {
		Config Config
		Query  url.Values
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
		Priority: 5,
		Rewrite: []autoscan.Rewrite{{
			From: "/Movies/*",
			To:   "/mnt/unionfs/Media/Movies/$1",
		}},
	}

	var testCases = []Test{
		{
			"Returns bad request when no directories are given",
			Given{
				Config: standardConfig,
				Query: url.Values{
					"dir": []string{},
				},
			},
			Expected{
				StatusCode: 400,
			},
		},
		{
			"Returns bad request for an empty directory",
			Given{
				Config: standardConfig,
				Query:  url.Values{"dir": []string{""}},
			},
			Expected{
				StatusCode: 400,
			},
		},
		{
			"Returns bad request for a relative directory",
			Given{
				Config: standardConfig,
				Query:  url.Values{"dir": []string{"tv/Show"}},
			},
			Expected{
				StatusCode: 400,
			},
		},
		{
			"Returns 200 for a relative directory that the rewrite makes absolute",
			Given{
				Config: Config{Rewrite: []autoscan.Rewrite{{From: "^tv/", To: "/mnt/tv/"}}},
				Query:  url.Values{"dir": []string{"tv/Show"}},
			},
			Expected{
				StatusCode: 200,
				Scans:      []autoscan.Scan{{Folder: "/mnt/tv/Show"}},
			},
		},
		{
			"Returns 200 for a Windows drive root and UNC paths",
			Given{
				Config: Config{},
				Query:  url.Values{"dir": []string{"C:/", `\\server\media\Show\`, `\\server/media/Show/`}},
			},
			Expected{
				StatusCode: 200,
				Scans: []autoscan.Scan{
					{Folder: "C:/"},
					{Folder: `\\server\media\Show`},
					{Folder: `\\server/media/Show`},
				},
			},
		},
		{
			"Returns 200 when given multiple directories",
			Given{
				Config: standardConfig,
				Query: url.Values{
					"dir": []string{
						"/Movies/Interstellar (2014)",
						"/Movies/Parasite (2019)",
					},
				},
			},
			Expected{
				StatusCode: 200,
				Scans: []autoscan.Scan{
					{
						Folder:   "/mnt/unionfs/Media/Movies/Interstellar (2014)",
						Priority: 5,
					},
					{
						Folder:   "/mnt/unionfs/Media/Movies/Parasite (2019)",
						Priority: 5,
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			callback := func(scans ...autoscan.Scan) error {
				// The webhook package tests the scan time.
				for i := range scans {
					scans[i].Time = time.Time{}
				}
				if !reflect.DeepEqual(tc.Expected.Scans, scans) {
					t.Logf("want: %v", tc.Expected.Scans)
					t.Logf("got:  %v", scans)
					t.Errorf("Scans do not equal")
					return errors.New("Scans do not equal")
				}

				return nil
			}

			handler, err := New(tc.Given.Config, callback)
			if err != nil {
				t.Fatalf("Could not create Manual Trigger: %v", err)
			}

			server := httptest.NewServer(handler)
			defer server.Close()

			req, err := http.NewRequest("POST", server.URL, nil)
			if err != nil {
				t.Fatalf("Failed creating request: %v", err)
			}

			req.URL.RawQuery = tc.Given.Query.Encode()

			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("Request failed: %v", err)
			}

			defer res.Body.Close()
			if res.StatusCode != tc.Expected.StatusCode {
				t.Errorf("Status codes do not match: %d vs %d", res.StatusCode, tc.Expected.StatusCode)
			}
		})
	}
}

func TestManualPageIsSelfContained(t *testing.T) {
	handler, err := New(Config{}, func(...autoscan.Scan) error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8", ct)
	}
	if body := rec.Body.String(); strings.Contains(body, "http://") || strings.Contains(body, "https://") {
		t.Errorf("manual page references an external URL: %s", body)
	}
}

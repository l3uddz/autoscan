package lidarr

import (
	"errors"
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
		Name:     "lidarr",
		Priority: 5,
		Rewrite: []autoscan.Rewrite{{
			From: "/Music/*",
			To:   "/mnt/unionfs/Media/Music/$1",
		}},
	}

	var testCases = []Test{
		{
			"Scan has all the correct fields",
			Given{
				Config:  standardConfig,
				Fixture: "testdata/marshmello.json",
			},
			Expected{
				StatusCode: 200,
				Scans: []autoscan.Scan{{
					Folder:   "/mnt/unionfs/Media/Music/Marshmello/Joytime III (2019)",
					Priority: 5,
				}},
			},
		},
		{
			"Multiple folders",
			Given{
				Config:  standardConfig,
				Fixture: "testdata/blink-182.json",
			},
			Expected{
				StatusCode: 200,
				Scans: []autoscan.Scan{
					{
						Folder:   "/mnt/unionfs/Media/Music/blink‐182/California (2016)/CD 01",
						Priority: 5,
					},
					{
						Folder:   "/mnt/unionfs/Media/Music/blink‐182/California (2016)/CD 02",
						Priority: 5,
					}},
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
			"Returns bad request on a Download event without files",
			Given{
				Config:  standardConfig,
				Fixture: "testdata/download_without_files.json",
			},
			Expected{
				StatusCode: 400,
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
				t.Fatalf("Could not create Lidarr Trigger: %v", err)
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
		})
	}
}

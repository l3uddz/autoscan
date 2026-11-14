package autoscan

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"testing"
)

func TestDSN(t *testing.T) {
	type Test struct {
		Name  string
		Path  string
		Query url.Values
		Want  string
	}

	var testCases = []Test{
		{
			Name: "readonly",
			Path: "library.db",
			Query: url.Values{
				"mode": []string{"ro"},
			},
			Want: "file://library.db?mode=ro",
		},
		{
			Name: "No Query",
			Path: "library.db",
			Want: "file://library.db",
		},
		{
			Name: "Path from root",
			Path: "/mnt/unionfs/library.db",
			Query: url.Values{
				"mode": []string{"rw"},
			},
			Want: "file:///mnt/unionfs/library.db?mode=rw",
		},
		{
			Name: "Query sorted by key",
			Path: "library.db",
			Query: url.Values{
				"cache": []string{"shared"},
				"mode":  []string{"rw"},
			},
			Want: "file://library.db?cache=shared&mode=rw",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			dsn := DSN(tc.Path, tc.Query)
			if dsn != tc.Want {
				t.Log(dsn)
				t.Log(tc.Want)
				t.Error("DSNs do not match")
			}
		})
	}
}

func TestBodyError(t *testing.T) {
	type Test struct {
		Name string
		Err  error
		Want error
	}

	var testCases = []Test{
		{
			Name: "Timeout is unavailable",
			Err:  os.ErrDeadlineExceeded,
			Want: ErrTargetUnavailable,
		},
		{
			Name: "Invalid JSON is fatal",
			Err:  json.Unmarshal([]byte("{"), new(struct{})),
			Want: ErrFatal,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			err := BodyError("failed decoding response", tc.Err)
			if !errors.Is(err, tc.Want) {
				t.Errorf("Got error %v, want %v", err, tc.Want)
			}
		})
	}
}

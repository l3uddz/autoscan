package autoscan

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
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

func TestResponseError(t *testing.T) {
	type Test struct {
		Status int
		Want   error
	}

	var testCases = []Test{
		{http.StatusOK, nil},
		{http.StatusNoContent, nil},
		{http.StatusUnauthorized, ErrFatal},
		{http.StatusForbidden, ErrFatal},
		{http.StatusNotFound, ErrTargetUnavailable},
		{http.StatusInternalServerError, ErrTargetUnavailable},
		{http.StatusBadGateway, ErrTargetUnavailable},
		{http.StatusServiceUnavailable, ErrTargetUnavailable},
		{http.StatusGatewayTimeout, ErrTargetUnavailable},
		{http.StatusNotImplemented, ErrFatal},
	}

	for _, tc := range testCases {
		t.Run(http.StatusText(tc.Status), func(t *testing.T) {
			body := &closeRecorder{Reader: strings.NewReader("")}
			res := &http.Response{StatusCode: tc.Status, Status: http.StatusText(tc.Status), Body: body}

			err := ResponseError(res, "invalid token")
			if tc.Want == nil {
				if err != nil || body.closed {
					t.Errorf("Got error %v and closed=%v, want nil and an open body", err, body.closed)
				}
				return
			}

			if !errors.Is(err, tc.Want) {
				t.Errorf("Got error %v, want %v", err, tc.Want)
			}
			if !body.closed {
				t.Error("Body is open, want it closed")
			}
			if tc.Status == http.StatusUnauthorized && !strings.Contains(err.Error(), "invalid token") {
				t.Errorf("Got error %v, want it to contain the auth message", err)
			}
		})
	}
}

type closeRecorder struct {
	io.Reader
	closed bool
}

func (c *closeRecorder) Close() error {
	c.closed = true
	return nil
}

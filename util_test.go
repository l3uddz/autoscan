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

func TestTargetRequestErrorOmitsURL(t *testing.T) {
	requestErr := &url.Error{
		Op:  "Get",
		URL: "http://example.invalid/?token=example-token",
		Err: os.ErrDeadlineExceeded,
	}
	err := TargetRequestError(requestErr, ErrTargetUnavailable)
	if !errors.Is(err, ErrTargetUnavailable) || !strings.Contains(err.Error(), os.ErrDeadlineExceeded.Error()) {
		t.Fatalf("error %q does not describe the timeout", err)
	}
	if strings.Contains(err.Error(), "example-token") {
		t.Fatal("error contains the token from the request URL")
	}
}

func TestRedactURL(t *testing.T) {
	type Test struct {
		Name         string
		Raw          string
		SecretParams []string
		Want         string
	}

	var testCases = []Test{
		{
			Name: "Password",
			Raw:  "http://user:secret@example.invalid:8080/path",
			Want: "http://user:xxxxx@example.invalid:8080/path",
		},
		{
			Name: "No userinfo",
			Raw:  "http://example.invalid/path?b=2&a=1",
			Want: "http://example.invalid/path?b=2&a=1",
		},
		{
			Name: "Username only",
			Raw:  "http://user@example.invalid",
			Want: "http://user@example.invalid",
		},
		{
			Name: "Invalid URL",
			Raw:  "http://user:secret@example.invalid:port",
			Want: "invalid url",
		},
		{
			Name:         "Secret parameter",
			Raw:          "http://user:secret@example.invalid/path?ApiKey=token&path=%2Fmnt",
			SecretParams: []string{"ApiKey"},
			Want:         "http://user:xxxxx@example.invalid/path?path=%2Fmnt",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			if got := RedactURL(tc.Raw, tc.SecretParams...); got != tc.Want {
				t.Errorf("RedactURL(%q) = %q, want %q", tc.Raw, got, tc.Want)
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

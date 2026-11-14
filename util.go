package autoscan

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// TargetTimeout limits each HTTP request that a target sends.
// When a request times out, the target is unavailable, and the processor
// retries later. The scan stays in the datastore until then.
const TargetTimeout = 30 * time.Second

// BodyError wraps an error from reading or decoding a target response body.
// A timeout means that the target is unavailable, so the processor retries.
// Any other error is fatal.
func BodyError(msg string, err error) error {
	sentinel := ErrFatal

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		sentinel = ErrTargetUnavailable
	}

	return fmt.Errorf("%s: %v: %w", msg, err, sentinel)
}

// ResponseError returns nil for a 2xx response. For any other status, it
// closes the response body and returns an error. A 401 status is fatal, and
// authMessage describes it. 404 and 5xx gateway errors mean that the target
// is unavailable. Any other status is fatal.
func ResponseError(res *http.Response, authMessage string) error {
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}

	res.Body.Close()

	switch res.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("%s: %s: %w", authMessage, res.Status, ErrFatal)
	case http.StatusNotFound, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return fmt.Errorf("%s: %w", res.Status, ErrTargetUnavailable)
	default:
		return fmt.Errorf("%s: %w", res.Status, ErrFatal)
	}
}

func JoinURL(base string, paths ...string) string {
	// credits: https://stackoverflow.com/a/57220413
	p := path.Join(paths...)
	return fmt.Sprintf("%s/%s", strings.TrimRight(base, "/"), strings.TrimLeft(p, "/"))
}

// DSN creates a data source name for use with sql.Open.
func DSN(path string, q url.Values) string {
	u := url.URL{
		Scheme:   "file",
		Path:     path,
		RawQuery: q.Encode(),
	}

	return u.String()
}

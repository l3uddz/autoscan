package autoscan

import (
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"
)

// TargetTimeout limits each HTTP request that a target sends.
// When a request times out, the target is unavailable, and the processor
// retries later. The scan stays in the datastore until then.
const TargetTimeout = 30 * time.Second

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

package autoscan

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan/targets/internal/httpapi"
)

// manualTrigger is the endpoint of the manual trigger of the other autoscan.
const manualTrigger = "triggers/manual"

type apiClient struct {
	http *httpapi.Client
}

func newAPIClient(baseURL string, user string, pass string, log zerolog.Logger) apiClient {
	return apiClient{http: httpapi.New(httpapi.Config{
		BaseURL: baseURL,
		Log:     log,
		Prepare: func(req *http.Request) {
			if user != "" && pass != "" {
				req.SetBasicAuth(user, pass)
			}
		},
		AuthMessage: "invalid basic auth",
	})}
}

func (c apiClient) Available() error {
	if err := c.http.Send(http.MethodHead, manualTrigger, nil); err != nil {
		return fmt.Errorf("availability: %w", err)
	}

	return nil
}

func (c apiClient) Scan(path string) error {
	if err := c.http.Send(http.MethodPost, manualTrigger, url.Values{"dir": {path}}); err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	return nil
}

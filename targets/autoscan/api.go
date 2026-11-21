package autoscan

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan/targets/internal/httpapi"
)

// manualTrigger is the path of the manual trigger of the other autoscan.
var manualTrigger = []string{"triggers", "manual"}

type apiClient struct {
	http *httpapi.Client
}

func newAPIClient(baseURL string, user string, pass string, log zerolog.Logger) (apiClient, error) {
	client, err := httpapi.New(httpapi.Config{
		BaseURL: baseURL,
		Log:     log,
		Prepare: func(req *http.Request) {
			if user != "" && pass != "" {
				req.SetBasicAuth(user, pass)
			}
		},
		AuthMessage: "invalid basic auth",
	})
	return apiClient{http: client}, err
}

func (c apiClient) Available(ctx context.Context) error {
	if err := c.http.Send(ctx, http.MethodHead, nil, manualTrigger...); err != nil {
		return fmt.Errorf("availability: %w", err)
	}

	return nil
}

func (c apiClient) Scan(ctx context.Context, path string) error {
	if err := c.http.Send(ctx, http.MethodPost, url.Values{"dir": {path}}, manualTrigger...); err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	return nil
}

package jellyfin

import (
	"fmt"
	"net/http"

	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan/targets/internal/httpapi"
	"github.com/l3uddz/autoscan/targets/internal/media"
)

// apiKeyParam is the query parameter that Jellyfin reads the token from.
const apiKeyParam = "ApiKey"

type apiClient struct {
	http *httpapi.Client
}

func newAPIClient(baseURL string, token string, log zerolog.Logger) (apiClient, error) {
	client, err := httpapi.New(httpapi.Config{
		BaseURL: baseURL,
		Log:     log,
		Prepare: func(req *http.Request) {
			// Jellyfin disables the legacy X-Emby-Token header by default.
			if req.URL.User != nil {
				// The Authorization header carries Basic auth for a reverse proxy.
				// The token is then in the URL. The client removes it from logs,
				// but the proxy access log can still contain it.
				q := req.URL.Query()
				q.Set(apiKeyParam, token)
				req.URL.RawQuery = q.Encode()
			} else {
				req.Header.Set("Authorization", fmt.Sprintf(`MediaBrowser Token="%s"`, token))
			}
			req.Header.Set("Accept", "application/json") // Force JSON Response.
		},
		AuthMessage:  "invalid jellyfin token",
		SecretParams: []string{apiKeyParam},
	})
	return apiClient{http: client}, err
}

func (c apiClient) Available() error {
	if err := c.http.Send(http.MethodGet, nil, "System", "Info"); err != nil {
		return fmt.Errorf("availability: %w", err)
	}

	return nil
}

func (c apiClient) Libraries() ([]media.Library, error) {
	var resp []struct {
		Name      string   `json:"Name"`
		Locations []string `json:"Locations"`
	}

	if err := c.http.GetJSON(&resp, "Library", "VirtualFolders"); err != nil {
		return nil, fmt.Errorf("libraries: %w", err)
	}

	libraries := make([]media.Library, 0)
	for _, lib := range resp {
		for _, folder := range lib.Locations {
			libraries = append(libraries, media.NewLibrary(0, lib.Name, folder))
		}
	}

	return libraries, nil
}

func (c apiClient) Scan(path string) error {
	if err := media.SendUpdated(c.http, path, "Modified"); err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	return nil
}

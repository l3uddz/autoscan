package plex

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan/targets/internal/httpapi"
	"github.com/l3uddz/autoscan/targets/internal/media"
)

type apiClient struct {
	http *httpapi.Client
}

func newAPIClient(baseURL string, token string, log zerolog.Logger) (apiClient, error) {
	client, err := httpapi.New(httpapi.Config{
		BaseURL: baseURL,
		Log:     log,
		Prepare: func(req *http.Request) {
			req.Header.Set("X-Plex-Token", token)
			req.Header.Set("Accept", "application/json") // Force JSON Response.
		},
		AuthMessage: "invalid plex token",
	})
	return apiClient{http: client}, err
}

func (c apiClient) Version() (string, error) {
	var resp struct {
		MediaContainer struct {
			Version string
		}
	}

	// The trailing "/" requests the root path with its trailing slash, also
	// behind a reverse proxy with a path prefix.
	if err := c.http.GetJSON(&resp, "/"); err != nil {
		return "", fmt.Errorf("version: %w", err)
	}

	return resp.MediaContainer.Version, nil
}

func (c apiClient) Libraries() ([]media.Library, error) {
	var resp struct {
		MediaContainer struct {
			Libraries []struct {
				ID       int    `json:"key,string"`
				Name     string `json:"title"`
				Sections []struct {
					Path string `json:"path"`
				} `json:"Location"`
			} `json:"Directory"`
		} `json:"MediaContainer"`
	}

	if err := c.http.GetJSON(&resp, "library", "sections"); err != nil {
		return nil, fmt.Errorf("libraries: %w", err)
	}

	libraries := make([]media.Library, 0)
	for _, lib := range resp.MediaContainer.Libraries {
		for _, folder := range lib.Sections {
			libraries = append(libraries, media.NewLibrary(lib.ID, lib.Name, folder.Path))
		}
	}

	return libraries, nil
}

func (c apiClient) Scan(path string, libraryID int) error {
	query := url.Values{"path": {path}}
	if err := c.http.Send(http.MethodGet, query, "library", "sections", strconv.Itoa(libraryID), "refresh"); err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	return nil
}

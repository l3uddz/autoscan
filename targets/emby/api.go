package emby

import (
	"fmt"
	"net/http"

	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan/targets/internal/httpapi"
	"github.com/l3uddz/autoscan/targets/internal/media"
)

type apiClient struct {
	http *httpapi.Client
}

func newAPIClient(baseURL string, token string, log zerolog.Logger) apiClient {
	return apiClient{http: httpapi.New(httpapi.Config{
		BaseURL: baseURL,
		Log:     log,
		Prepare: func(req *http.Request) {
			req.Header.Set("X-Emby-Token", token)
			req.Header.Set("Accept", "application/json") // Force JSON Response.
		},
		AuthMessage: "invalid emby token",
	})}
}

func (c apiClient) Available() error {
	if err := c.http.Send(http.MethodGet, "emby/System/Info", nil); err != nil {
		return fmt.Errorf("availability: %w", err)
	}

	return nil
}

func (c apiClient) Libraries() ([]media.Library, error) {
	var resp []struct {
		Name    string `json:"Name"`
		Folders []struct {
			Path string `json:"Path"`
		} `json:"SubFolders"`
	}

	if err := c.http.GetJSON("emby/Library/SelectableMediaFolders", &resp); err != nil {
		return nil, fmt.Errorf("libraries: %w", err)
	}

	libraries := make([]media.Library, 0)
	for _, lib := range resp {
		for _, folder := range lib.Folders {
			libraries = append(libraries, media.NewLibrary(0, lib.Name, folder.Path))
		}
	}

	return libraries, nil
}

func (c apiClient) Scan(path string) error {
	if err := media.SendUpdated(c.http, path, "Created"); err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	return nil
}

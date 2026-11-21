package emby

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan/targets/internal/httpapi"
	"github.com/l3uddz/autoscan/targets/internal/media"
)

// apiRoot is the path of the Emby API. Every request uses it, so a reverse
// proxy that forwards only /emby reaches every endpoint.
var apiRoot = []string{"emby"}

// endpoint returns the path elements of an endpoint below apiRoot.
func endpoint(elem ...string) []string {
	return slices.Concat(apiRoot, elem)
}

type apiClient struct {
	http *httpapi.Client
}

func newAPIClient(baseURL string, token string, log zerolog.Logger) (apiClient, error) {
	client, err := httpapi.New(httpapi.Config{
		BaseURL: baseURL,
		Log:     log,
		Prepare: func(req *http.Request) {
			req.Header.Set("X-Emby-Token", token)
		},
		AuthMessage: "invalid emby token",
	})
	return apiClient{http: client}, err
}

func (c apiClient) Available(ctx context.Context) error {
	if err := c.http.Send(ctx, http.MethodGet, nil, endpoint("System", "Info")...); err != nil {
		return fmt.Errorf("availability: %w", err)
	}

	return nil
}

func (c apiClient) Libraries(ctx context.Context) ([]media.Library, error) {
	var resp []struct {
		Name    string `json:"Name"`
		Folders []struct {
			Path string `json:"Path"`
		} `json:"SubFolders"`
	}

	if err := c.http.GetJSON(ctx, &resp, endpoint("Library", "SelectableMediaFolders")...); err != nil {
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

func (c apiClient) Scan(ctx context.Context, path string) error {
	if err := media.SendUpdated(ctx, c.http, apiRoot, path, "Created"); err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	return nil
}

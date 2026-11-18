package jellyfin

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan"
	"github.com/l3uddz/autoscan/targets/internal/media"
)

type apiClient struct {
	client  *http.Client
	log     zerolog.Logger
	baseURL string
	token   string
}

func newAPIClient(baseURL string, token string, log zerolog.Logger) apiClient {
	return apiClient{
		client:  &http.Client{Timeout: autoscan.TargetTimeout},
		log:     log,
		baseURL: baseURL,
		token:   token,
	}
}

func (c apiClient) do(req *http.Request) (*http.Response, error) {
	// Jellyfin disables the legacy X-Emby-Token header by default.
	if req.URL.User != nil {
		// The Authorization header carries Basic auth for a reverse proxy.
		// The token is then in the URL. autoscan.RedactURL removes it from
		// logs, but the proxy access log can still contain it.
		q := req.URL.Query()
		q.Set(apiKeyParam, c.token)
		req.URL.RawQuery = q.Encode()
	} else {
		req.Header.Set("Authorization", fmt.Sprintf(`MediaBrowser Token="%s"`, c.token))
	}
	req.Header.Set("Accept", "application/json") // Force JSON Response.

	res, err := c.client.Do(req)
	if err != nil {
		return nil, autoscan.TargetRequestError(err, autoscan.ErrTargetUnavailable)
	}

	if err := autoscan.ResponseError(res, "invalid jellyfin token"); err != nil {
		c.log.Trace().
			Str("request_url", autoscan.RedactURL(res.Request.URL.String(), apiKeyParam)).
			Int("response_status", res.StatusCode).
			Msg("Request failed")

		return nil, err
	}

	return res, nil
}

// apiKeyParam is the query parameter that Jellyfin reads the token from.
const apiKeyParam = "ApiKey"

func (c apiClient) Available() error {
	// create request
	reqURL := autoscan.JoinURL(c.baseURL, "System", "Info")
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed creating availability request: %w", autoscan.TargetRequestError(err, autoscan.ErrFatal))
	}

	// send request
	res, err := c.do(req)
	if err != nil {
		return fmt.Errorf("availability: %w", err)
	}

	defer res.Body.Close()
	return nil
}

func (c apiClient) Libraries() ([]media.Library, error) {
	// create request
	reqURL := autoscan.JoinURL(c.baseURL, "Library", "VirtualFolders")
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed creating libraries request: %w", autoscan.TargetRequestError(err, autoscan.ErrFatal))
	}

	// send request
	res, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("libraries: %w", err)
	}

	defer res.Body.Close()

	// decode response
	type Response struct {
		Name      string   `json:"Name"`
		Locations []string `json:"Locations"`
	}

	resp := make([]Response, 0)
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return nil, autoscan.BodyError("failed decoding libraries request response", err)
	}

	// process response
	libraries := make([]media.Library, 0)
	for _, lib := range resp {
		for _, folder := range lib.Locations {
			libraries = append(libraries, media.NewLibrary(0, lib.Name, folder))
		}
	}

	return libraries, nil
}

func (c apiClient) Scan(path string) error {
	req, err := media.NewUpdatedRequest(c.baseURL, path, "Modified")
	if err != nil {
		return err
	}

	// send request
	res, err := c.do(req)
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	defer res.Body.Close()
	return nil
}

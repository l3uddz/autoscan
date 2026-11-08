package jellyfin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/rs/zerolog"

	"github.com/l3uddz/autoscan"
)

type apiClient struct {
	client  *http.Client
	log     zerolog.Logger
	baseURL string
	token   string
}

func newAPIClient(baseURL string, token string, log zerolog.Logger) apiClient {
	return apiClient{
		client:  &http.Client{},
		log:     log,
		baseURL: baseURL,
		token:   token,
	}
}

func (c apiClient) do(req *http.Request) (*http.Response, error) {
	// Jellyfin disables the legacy X-Emby-Token header by default.
	if req.URL.User != nil {
		// The Authorization header carries Basic auth for a reverse proxy.
		q := req.URL.Query()
		q.Set(apiKeyParam, c.token)
		req.URL.RawQuery = q.Encode()
	} else {
		req.Header.Set("Authorization", fmt.Sprintf(`MediaBrowser Token="%s"`, c.token))
	}
	req.Header.Set("Accept", "application/json") // Force JSON Response.

	res, err := c.client.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			urlErr.URL = redactURL(req.URL)
		}
		return nil, fmt.Errorf("%v: %w", err, autoscan.ErrTargetUnavailable)
	}

	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return res, nil
	}

	c.log.Trace().
		Str("request_url", redactURL(res.Request.URL)).
		Int("response_status", res.StatusCode).
		Msg("Request failed")

	// statusCode not in the 2xx range, close response
	res.Body.Close()

	switch res.StatusCode {
	case 401:
		return nil, fmt.Errorf("invalid jellyfin token: %s: %w", res.Status, autoscan.ErrFatal)
	case 404, 500, 502, 503, 504:
		return nil, fmt.Errorf("%s: %w", res.Status, autoscan.ErrTargetUnavailable)
	default:
		return nil, fmt.Errorf("%s: %w", res.Status, autoscan.ErrFatal)
	}
}

// apiKeyParam is the query parameter that Jellyfin reads the token from.
const apiKeyParam = "ApiKey"

// redactURL returns u without the token and the user password.
func redactURL(u *url.URL) string {
	r := *u
	q := r.Query()
	q.Del(apiKeyParam)
	r.RawQuery = q.Encode()
	return r.Redacted()
}

func (c apiClient) Available() error {
	// create request
	reqURL := autoscan.JoinURL(c.baseURL, "System", "Info")
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed creating availability request: %v: %w", err, autoscan.ErrFatal)
	}

	// send request
	res, err := c.do(req)
	if err != nil {
		return fmt.Errorf("availability: %w", err)
	}

	defer res.Body.Close()
	return nil
}

type library struct {
	Name string
	Path string
}

func (c apiClient) Libraries() ([]library, error) {
	// create request
	reqURL := autoscan.JoinURL(c.baseURL, "Library", "VirtualFolders")
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed creating libraries request: %v: %w", err, autoscan.ErrFatal)
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
		return nil, fmt.Errorf("failed decoding libraries request response: %v: %w", err, autoscan.ErrFatal)
	}

	// process response
	libraries := make([]library, 0)
	for _, lib := range resp {
		for _, folder := range lib.Locations {
			libPath := folder

			// Add trailing slash if there is none.
			if len(libPath) > 0 && libPath[len(libPath)-1] != '/' {
				libPath += "/"
			}

			libraries = append(libraries, library{
				Name: lib.Name,
				Path: libPath,
			})
		}
	}

	return libraries, nil
}

type scanRequest struct {
	Path       string `json:"path"`
	UpdateType string `json:"updateType"`
}

func (c apiClient) Scan(path string) error {
	// create request payload
	type Payload struct {
		Updates []scanRequest `json:"Updates"`
	}

	payload := &Payload{
		Updates: []scanRequest{
			{
				Path:       path,
				UpdateType: "Modified",
			},
		},
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed encoding scan request payload: %v: %w", err, autoscan.ErrFatal)
	}

	// create request
	reqURL := autoscan.JoinURL(c.baseURL, "Library", "Media", "Updated")
	req, err := http.NewRequest("POST", reqURL, bytes.NewBuffer(b))
	if err != nil {
		return fmt.Errorf("failed creating scan request: %v: %w", err, autoscan.ErrFatal)
	}

	req.Header.Set("Content-Type", "application/json")

	// send request
	res, err := c.do(req)
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	defer res.Body.Close()
	return nil
}

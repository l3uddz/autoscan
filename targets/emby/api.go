package emby

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
	req.Header.Set("X-Emby-Token", c.token)
	req.Header.Set("Accept", "application/json") // Force JSON Response.

	res, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%v: %w", err, autoscan.ErrTargetUnavailable)
	}

	if err := autoscan.ResponseError(res, "invalid emby token"); err != nil {
		c.log.Trace().
			Stringer("request_url", res.Request.URL).
			Int("response_status", res.StatusCode).
			Msg("Request failed")

		return nil, err
	}

	return res, nil
}

func (c apiClient) Available() error {
	// create request
	reqURL := autoscan.JoinURL(c.baseURL, "emby", "System", "Info")
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

func (c apiClient) Libraries() ([]media.Library, error) {
	// create request
	reqURL := autoscan.JoinURL(c.baseURL, "emby", "Library", "SelectableMediaFolders")
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
		Name    string `json:"Name"`
		Folders []struct {
			Path string `json:"Path"`
		} `json:"SubFolders"`
	}

	resp := make([]Response, 0)
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return nil, autoscan.BodyError("failed decoding libraries request response", err)
	}

	// process response
	libraries := make([]media.Library, 0)
	for _, lib := range resp {
		for _, folder := range lib.Folders {
			libraries = append(libraries, media.NewLibrary(0, lib.Name, folder.Path))
		}
	}

	return libraries, nil
}

func (c apiClient) Scan(path string) error {
	req, err := media.NewUpdatedRequest(c.baseURL, path, "Created")
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

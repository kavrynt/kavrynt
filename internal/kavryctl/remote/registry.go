package remote

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kavrynt/kavrynt/internal/kavryctl/manifest"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
}

type ServerRecord struct {
	Manifest     manifest.Manifest `json:"manifest"`
	RegisteredAt time.Time         `json:"registeredAt"`
	UpdatedAt    time.Time         `json:"updatedAt"`
}

type listResponse struct {
	Servers []ServerRecord `json:"servers"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func NewClient(baseURL string) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("registry URL is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse registry URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("registry URL must use http or https")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("registry URL must include a host")
	}

	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}, nil
}

func (c *Client) Register(m manifest.Manifest) (ServerRecord, bool, error) {
	body, err := json.Marshal(m)
	if err != nil {
		return ServerRecord{}, false, fmt.Errorf("encode manifest: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.endpoint("/v1/servers"), bytes.NewReader(body))
	if err != nil {
		return ServerRecord{}, false, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ServerRecord{}, false, fmt.Errorf("registry request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return ServerRecord{}, false, decodeError(resp)
	}

	var record ServerRecord
	if err := json.NewDecoder(resp.Body).Decode(&record); err != nil {
		return ServerRecord{}, false, fmt.Errorf("decode registry response: %w", err)
	}

	return record, resp.StatusCode == http.StatusCreated, nil
}

func (c *Client) List() ([]ServerRecord, error) {
	resp, err := c.httpClient.Get(c.endpoint("/v1/servers"))
	if err != nil {
		return nil, fmt.Errorf("registry request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, decodeError(resp)
	}

	var result listResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode registry response: %w", err)
	}
	return result.Servers, nil
}

func (c *Client) Inspect(name string) (ServerRecord, error) {
	resp, err := c.httpClient.Get(c.endpoint("/v1/servers/" + url.PathEscape(name)))
	if err != nil {
		return ServerRecord{}, fmt.Errorf("registry request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ServerRecord{}, decodeError(resp)
	}

	var record ServerRecord
	if err := json.NewDecoder(resp.Body).Decode(&record); err != nil {
		return ServerRecord{}, fmt.Errorf("decode registry response: %w", err)
	}
	return record, nil
}

func (c *Client) Unregister(name string) error {
	req, err := http.NewRequest(http.MethodDelete, c.endpoint("/v1/servers/"+url.PathEscape(name)), nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("registry request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return decodeError(resp)
	}
	return nil
}

func (c *Client) endpoint(path string) string {
	return c.baseURL + path
}

func decodeError(resp *http.Response) error {
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return fmt.Errorf("registry returned %s", resp.Status)
	}

	var apiError errorResponse
	if err := json.Unmarshal(data, &apiError); err == nil && apiError.Error != "" {
		return fmt.Errorf("registry returned %s: %s", resp.Status, apiError.Error)
	}

	message := strings.TrimSpace(string(data))
	if message == "" {
		return fmt.Errorf("registry returned %s", resp.Status)
	}
	return fmt.Errorf("registry returned %s: %s", resp.Status, message)
}

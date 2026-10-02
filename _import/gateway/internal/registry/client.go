package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/kavrynt/gateway/internal/model"
)

type Client struct {
	baseURL string
	client  *http.Client
}

func NewClient(baseURL string, client *http.Client) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("registry URL is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("registry URL must be an absolute URL")
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &Client{baseURL: baseURL, client: client}, nil
}

func (c *Client) ListServers(ctx context.Context) ([]model.ServerRecord, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/servers", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry returned status %d", resp.StatusCode)
	}

	var list model.ServerList
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&list); err != nil {
		return nil, err
	}
	return list.Servers, nil
}

// HTTP client for the local Cyborg daemon.
// It is the CLI-facing adapter for daemon requests.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/williamfzc/cyborg/internal/core/action"
	"github.com/williamfzc/cyborg/internal/core/device"
	coredriver "github.com/williamfzc/cyborg/internal/core/driver"
	"github.com/williamfzc/cyborg/internal/daemon"
)

type Client struct {
	baseURL string
	http    *http.Client
}

func NewDefault() *Client {
	return &Client{
		baseURL: daemon.DefaultBaseURL(),
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) Ping(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodGet, "/status", nil)
	return err
}

func (c *Client) Status(ctx context.Context) (daemon.Status, error) {
	resp, err := c.do(ctx, http.MethodGet, "/status", nil)
	if err != nil {
		return daemon.Status{}, err
	}
	defer resp.Body.Close()
	var out daemon.Status
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

func (c *Client) Drivers(ctx context.Context) ([]coredriver.Summary, error) {
	resp, err := c.do(ctx, http.MethodGet, "/drivers", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out []coredriver.Summary
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

func (c *Client) DriverActions(ctx context.Context, kind device.Kind) ([]coredriver.ActionSpec, error) {
	resp, err := c.do(ctx, http.MethodGet, "/drivers/"+string(kind)+"/actions", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out []coredriver.ActionSpec
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

func (c *Client) Create(ctx context.Context, kind device.Kind, options map[string]any) (device.Device, error) {
	resp, err := c.do(ctx, http.MethodPost, "/devices", map[string]any{"kind": kind, "options": options})
	if err != nil {
		return device.Device{}, err
	}
	defer resp.Body.Close()
	var out device.Device
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

func (c *Client) List(ctx context.Context) ([]device.Device, error) {
	resp, err := c.do(ctx, http.MethodGet, "/devices", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out []device.Device
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

func (c *Client) Show(ctx context.Context, id string) (device.Device, error) {
	resp, err := c.do(ctx, http.MethodGet, "/devices/"+id, nil)
	if err != nil {
		return device.Device{}, err
	}
	defer resp.Body.Close()
	var out device.Device
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

func (c *Client) Remove(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/devices/"+id, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (c *Client) Act(ctx context.Context, req action.Action) (action.Result, error) {
	resp, err := c.do(ctx, http.MethodPost, "/actions", req)
	if err != nil {
		return action.Result{}, err
	}
	defer resp.Body.Close()
	var out action.Result
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return nil, errors.New(strings.TrimSpace(string(data)))
	}
	return resp, nil
}

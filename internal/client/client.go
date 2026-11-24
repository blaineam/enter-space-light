// Package client talks to a running daemon over its local API, using the
// handshake file (address + bearer token) the daemon writes on start.
package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/blaineam/enter-space-light/internal/paths"
)

// ErrNotRunning means no daemon answered.
var ErrNotRunning = errors.New("Enter Space Light is not running (start it with `esl run`)")

// Client is a connection to the daemon.
type Client struct {
	URL   string
	Token string
	http  *http.Client
}

// Connect reads api.json and checks the daemon answers.
func Connect() (*Client, error) {
	b, err := os.ReadFile(paths.APIFile())
	if err != nil {
		return nil, ErrNotRunning
	}
	var hs struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(b, &hs); err != nil || hs.URL == "" {
		return nil, ErrNotRunning
	}
	c := &Client{URL: hs.URL, Token: hs.Token, http: &http.Client{Timeout: 15 * time.Minute}}
	ping := &http.Client{Timeout: 3 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, c.URL+"/api/status", nil)
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := ping.Do(req)
	if err != nil {
		return nil, ErrNotRunning // stale api.json from a crash
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrNotRunning
	}
	return c, nil
}

// Do calls method path with an optional JSON body, decoding into out.
func (c *Client) Do(method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.URL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("X-ESL", "1")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("%s %s: %s", method, path, resp.Status)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// LoginURL returns a one-time URL that opens the UI signed in.
func (c *Client) LoginURL() (string, error) {
	var r struct {
		Code string `json:"code"`
		URL  string `json:"url"`
	}
	if err := c.Do(http.MethodPost, "/api/login-code", nil, &r); err != nil {
		return "", err
	}
	return r.URL + "/login?code=" + r.Code, nil
}

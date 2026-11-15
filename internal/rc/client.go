// Package rc is a minimal client for rclone's remote control API over a unix
// socket. Only methods listed in the allowlist can be called: the rc API is
// equivalent to shell access as the rclone user, so the allowlist is the
// security boundary between the web UI and the engine.
package rc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// APIError is returned when rclone answers with a non-200 status or with an
// "error" field in an otherwise successful response.
type APIError struct {
	Method string
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("rclone %s: %s", e.Method, e.Body)
}

// ErrResponseTooLarge is returned when an rc response exceeds maxResponseBytes.
// It exists so callers can tell a protocol limit from malformed JSON.
var ErrResponseTooLarge = errors.New("rclone response too large")

// maxResponseBytes bounds one rc response. It is a variable so tests can use a
// small limit instead of sending tens of megabytes.
var maxResponseBytes int64 = 32 << 20

// Client posts JSON to rclone over a unix socket.
type Client struct {
	http *http.Client
	user string
	pass string
}

// NewClient builds a client bound to socket. Empty user disables basic auth.
func NewClient(socket, user, pass string) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			dialer := net.Dialer{Timeout: 5 * time.Second}
			return dialer.DialContext(ctx, "unix", socket)
		},
		MaxIdleConns:       8,
		IdleConnTimeout:    90 * time.Second,
		DisableCompression: true,
	}
	return &Client{http: &http.Client{Transport: transport}, user: user, pass: pass}
}

// Call invokes one rc method. in may be nil; out may be nil.
//
// A top-level "error" field is treated as a failed call. Some methods answer it
// as part of their payload instead (job/status reports a finished transfer with
// success=false and error text), so those callers use CallWithPayloadError.
func (c *Client) Call(ctx context.Context, method string, in, out any) error {
	return c.call(ctx, method, in, out, false)
}

// CallWithPayloadError is Call for methods whose "error" field is data rather
// than a transport failure: only a non-200 answer counts as an error.
func (c *Client) CallWithPayloadError(ctx context.Context, method string, in, out any) error {
	return c.call(ctx, method, in, out, true)
}

func (c *Client) call(ctx context.Context, method string, in, out any, payloadError bool) error {
	if !Allowed(method) {
		return fmt.Errorf("%w: %s", ErrMethodNotAllowed, method)
	}

	var body io.Reader = http.NoBody
	if in != nil {
		payload, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("marshal %s: %w", method, err)
		}
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://rclone/"+method, body)
	if err != nil {
		return fmt.Errorf("request %s: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("call %s: %w", method, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read %s: %w", method, err)
	}
	if int64(len(data)) > maxResponseBytes {
		return fmt.Errorf("%w: %s", ErrResponseTooLarge, method)
	}

	if !payloadError {
		var probe struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &probe) == nil && probe.Error != "" {
			return &APIError{Method: method, Status: resp.StatusCode, Body: probe.Error}
		}
	}
	if resp.StatusCode != http.StatusOK {
		return &APIError{Method: method, Status: resp.StatusCode, Body: string(data)}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode %s: %w", method, err)
	}
	return nil
}

// Version is the subset of core/version this app reports in its UI.
type Version struct {
	Version    string `json:"version"`
	Decomposed []int  `json:"decomposed"`
	IsGit      bool   `json:"isGit"`
	IsBeta     bool   `json:"isBeta"`
}

// Ping checks that rclone answers and returns its version.
func (c *Client) Ping(ctx context.Context) (Version, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var v Version
	if err := c.Call(ctx, "core/version", nil, &v); err != nil {
		return Version{}, err
	}
	return v, nil
}

// IsUnavailable reports whether err means "rclone is not running or the socket
// is not reachable", as opposed to rclone answering with an error.
func IsUnavailable(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return false
	}
	return true
}

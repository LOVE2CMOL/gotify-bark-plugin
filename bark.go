package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// gotifyMessage mirrors the payload Gotify pushes over its WebSocket stream
// (model.MessageExternal).
type gotifyMessage struct {
	ID            uint           `json:"id"`
	ApplicationID uint           `json:"appid"`
	Message       string         `json:"message"`
	Title         string         `json:"title"`
	Priority      *int           `json:"priority"`
	Extras        map[string]any `json:"extras"`
	Date          time.Time      `json:"date"`
}

// barkPush is the body of POST /push on a bark-server (API v2).
// See https://github.com/Finb/bark-server/blob/master/docs/API_V2.md
type barkPush struct {
	Body      string `json:"body"`
	DeviceKey string `json:"device_key"`
	Title     string `json:"title,omitempty"`
	Subtitle  string `json:"subtitle,omitempty"`
	Level     string `json:"level,omitempty"`
	Badge     *int   `json:"badge,omitempty"`
	Call      string `json:"call,omitempty"`
	AutoCopy  string `json:"autoCopy,omitempty"`
	Copy      string `json:"copy,omitempty"`
	Sound     string `json:"sound,omitempty"`
	Icon      string `json:"icon,omitempty"`
	Group     string `json:"group,omitempty"`
	URL       string `json:"url,omitempty"`
	Action    string `json:"action,omitempty"`
	IsArchive string `json:"isArchive,omitempty"`
	TTL       int    `json:"ttl,omitempty"`
}

// barkResponse is the bark-server response envelope. code == 200 means the
// message was accepted by APNs.
type barkResponse struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	Timestamp int64  `json:"timestamp"`
}

// barkError describes a non-200 answer of a bark-server.
type barkError struct {
	StatusCode int
	Code       int
	Message    string
	Body       string
}

func (e *barkError) Error() string {
	switch {
	case e.Message != "" && e.Code != 0:
		return fmt.Sprintf("bark returned HTTP %d, code %d: %s", e.StatusCode, e.Code, e.Message)
	case e.Message != "":
		return fmt.Sprintf("bark returned HTTP %d: %s", e.StatusCode, e.Message)
	case e.Body != "":
		return fmt.Sprintf("bark returned HTTP %d: %s", e.StatusCode, truncate(e.Body, 300))
	default:
		return fmt.Sprintf("bark returned HTTP %d", e.StatusCode)
	}
}

// isPermanent reports whether retrying the same push is pointless.
func (e *barkError) isPermanent() bool {
	if e.StatusCode >= 400 && e.StatusCode < 500 && e.StatusCode != http.StatusTooManyRequests {
		return true
	}
	// bark-server answers 200 with a non-200 code for bad device keys.
	if e.StatusCode == http.StatusOK && e.Code >= 400 && e.Code < 500 {
		return true
	}
	return false
}

// newHTTPClient builds the HTTP client used for bark requests.
func newHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          8,
			MaxIdleConnsPerHost:   4,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: timeout,
		},
	}
}

// sendBarkPush posts one push to the bark server. It retries transient
// failures with a small backoff so a short bark-server restart does not lose
// notifications.
func sendBarkPush(ctx context.Context, client *http.Client, serverURL string, push barkPush) error {
	payload, err := json.Marshal(push)
	if err != nil {
		return fmt.Errorf("encode bark payload: %w", err)
	}

	endpoint := strings.TrimRight(serverURL, "/") + "/push"
	const attempts = 3
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt-1) * 500 * time.Millisecond):
			}
		}
		lastErr = postBarkPush(ctx, client, endpoint, payload)
		if lastErr == nil {
			return nil
		}
		if be, ok := lastErr.(*barkError); ok && be.isPermanent() {
			return lastErr
		}
	}
	return lastErr
}

func postBarkPush(ctx context.Context, client *http.Client, endpoint string, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("User-Agent", "gotify-bark-plugin/"+Version)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("call bark: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("read bark response: %w", err)
	}

	parsed := barkResponse{}
	_ = json.Unmarshal(raw, &parsed)

	if resp.StatusCode != http.StatusOK || (parsed.Code != 0 && parsed.Code != http.StatusOK) {
		return &barkError{
			StatusCode: resp.StatusCode,
			Code:       parsed.Code,
			Message:    parsed.Message,
			Body:       strings.TrimSpace(string(raw)),
		}
	}
	return nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

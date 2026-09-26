package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	streamHandshakeTimeout = 15 * time.Second
	streamReadTimeout      = 90 * time.Second
	streamWriteTimeout     = 10 * time.Second
	reconnectMinDelay      = 2 * time.Second
	reconnectMaxDelay      = 60 * time.Second
	appCacheTTL            = 5 * time.Minute
)

// streamURL converts the configured Gotify base URL into the WebSocket stream
// endpoint, keeping any reverse-proxy path prefix intact.
//
//	http://gotify:80/      -> ws://gotify:80/stream
//	https://push.example/gotify -> wss://push.example/gotify/stream
func streamURL(baseURL, token string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse gotify url: %w", err)
	}
	switch u.Scheme {
	case "http", "ws":
		u.Scheme = "ws"
	case "https", "wss":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("unsupported gotify url scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return "", errors.New("gotify url has no host")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/stream"
	u.RawQuery = ""
	u.Fragment = ""
	q := url.Values{}
	q.Set("token", token)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// httpBaseURL returns the http(s) equivalent of the configured Gotify URL.
func httpBaseURL(baseURL string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	}
	u.RawQuery, u.Fragment = "", ""
	return strings.TrimRight(u.String(), "/"), nil
}

// applicationInfo is what the plugin needs to know about a Gotify application:
// the name used as the Bark group, and the icon the user picked for it.
type applicationInfo struct {
	Name  string
	Image string
}

// appCache resolves Gotify application ids to their metadata so that the Bark
// group, the {appname} template placeholder and the "auto" icon can use what
// the user configured in Gotify.
type appCache struct {
	mu      sync.Mutex
	apps    map[uint]applicationInfo
	fetched time.Time
}

func (c *appCache) get(ctx context.Context, client *http.Client, baseURL, token string, appID uint) applicationInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.apps == nil || time.Since(c.fetched) > appCacheTTL {
		if apps, err := fetchApplications(ctx, client, baseURL, token); err != nil {
			log.Printf("[bark] cannot list gotify applications (falling back to app ids): %v", err)
			if c.apps == nil {
				c.apps = map[uint]applicationInfo{}
			}
		} else {
			c.apps = apps
		}
		c.fetched = time.Now()
	}
	return c.apps[appID]
}

func fetchApplications(ctx context.Context, client *http.Client, baseURL, token string) (map[uint]applicationInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/application", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Gotify-Key", token)
	req.Header.Set("User-Agent", "gotify-bark-plugin/"+Version)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /application returned HTTP %d: %s", resp.StatusCode, truncate(strings.TrimSpace(string(body)), 200))
	}
	var apps []struct {
		ID    uint   `json:"id"`
		Name  string `json:"name"`
		Image string `json:"image"`
	}
	if err := json.Unmarshal(body, &apps); err != nil {
		return nil, err
	}
	out := make(map[uint]applicationInfo, len(apps))
	for _, a := range apps {
		out[a.ID] = applicationInfo{Name: a.Name, Image: a.Image}
	}
	return out, nil
}

// resolveAppIcon turns the image configured on a Gotify application into an
// absolute URL the iPhone can fetch.
//
// Gotify stores most icons as a server-relative path ("static/defaultapp.png")
// but lets users paste a full URL as well, so both have to be handled.
func resolveAppIcon(baseURL, image string) string {
	image = strings.TrimSpace(image)
	if image == "" {
		return ""
	}
	if strings.HasPrefix(image, "http://") || strings.HasPrefix(image, "https://") {
		return image
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return ""
	}
	return baseURL + "/" + strings.TrimLeft(image, "/")
}

// streamSubscriber keeps a WebSocket connection to the Gotify stream endpoint
// and hands every received message to onMessage. It reconnects with
// exponential backoff and gives up only when the context is cancelled.
type streamSubscriber struct {
	baseURL   string
	token     string
	client    *http.Client
	debug     bool
	onMessage func(gotifyMessage)
}

func (s *streamSubscriber) run(ctx context.Context) {
	delay := reconnectMinDelay
	for {
		if ctx.Err() != nil {
			return
		}
		start := time.Now()
		err := s.connectAndRead(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > 30*time.Second {
			// The connection was healthy for a while, so restart the backoff.
			delay = reconnectMinDelay
		}
		log.Printf("[bark] gotify stream disconnected: %v (reconnecting in %s)", err, delay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay < reconnectMaxDelay {
			delay *= 2
			if delay > reconnectMaxDelay {
				delay = reconnectMaxDelay
			}
		}
	}
}

func (s *streamSubscriber) connectAndRead(ctx context.Context) error {
	target, err := streamURL(s.baseURL, s.token)
	if err != nil {
		return err
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: streamHandshakeTimeout,
		Proxy:            http.ProxyFromEnvironment,
	}
	conn, resp, err := dialer.DialContext(ctx, target, nil)
	if err != nil {
		if resp != nil {
			switch resp.StatusCode {
			case http.StatusUnauthorized, http.StatusForbidden:
				return fmt.Errorf("gotify rejected the token (HTTP %d): client_token must be a Gotify *client* token, see the plugin instructions", resp.StatusCode)
			case http.StatusNotFound:
				return fmt.Errorf("gotify stream endpoint not found (HTTP 404): check gotify_url, including any reverse-proxy path prefix")
			}
			return fmt.Errorf("websocket dial failed (HTTP %d): %w", resp.StatusCode, err)
		}
		return fmt.Errorf("websocket dial failed: %w", err)
	}
	defer conn.Close()

	// ReadMessage blocks until data arrives or the read deadline expires, so a
	// cancelled context alone would not unblock it. Closing the connection from
	// a watcher goroutine makes the read return immediately, which is what lets
	// stop() finish quickly instead of hanging for the whole read timeout.
	closed := make(chan struct{})
	defer close(closed)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-closed:
		}
	}()

	log.Printf("[bark] connected to gotify stream at %s", redactToken(target))
	conn.SetReadLimit(1 << 20)
	_ = conn.SetReadDeadline(time.Now().Add(streamReadTimeout))
	conn.SetPingHandler(func(appData string) error {
		_ = conn.SetReadDeadline(time.Now().Add(streamReadTimeout))
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(streamWriteTimeout))
	})

	for {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		_ = conn.SetReadDeadline(time.Now().Add(streamReadTimeout))

		var msg gotifyMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			log.Printf("[bark] ignoring malformed stream message: %v", err)
			continue
		}
		if s.debug {
			log.Printf("[bark] stream message id=%d appid=%d title=%q priority=%v", msg.ID, msg.ApplicationID, msg.Title, msg.priority())
		}
		s.onMessage(msg)
	}
}

// redactToken hides the client token when logging the stream URL.
func redactToken(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "***"
	}
	q := u.Query()
	if q.Get("token") != "" {
		q.Set("token", "***")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// priority returns the effective priority, defaulting to 0.
func (m gotifyMessage) priority() int {
	if m.Priority == nil {
		return 0
	}
	return *m.Priority
}

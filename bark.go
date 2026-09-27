package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Bark end-to-end encryption parameters. The key length selects the AES
// variant and must match the algorithm chosen in the Bark app; the IV is
// always 16 characters. See https://bark.day.app/#/encryption
const (
	encryptIVLen   = 16
	encryptModeCBC = "cbc"
	encryptModeECB = "ecb"
)

// encryptKeyLens maps every accepted encrypt_key length to the algorithm name
// the Bark app shows in its encryption settings (AES128 / AES192 / AES256).
// Go's aes.NewCipher derives the variant from the key length by itself.
var encryptKeyLens = map[int]string{16: "AES128", 24: "AES192", 32: "AES256"}

// encryptModes lists the accepted encrypt_mode values.
var encryptModes = map[string]bool{encryptModeCBC: true, encryptModeECB: true}

// levelAuto derives the interruption level from the Gotify priority, and
// iconAuto reuses the icon configured on the Gotify application.
const (
	levelAuto = "auto"
	iconAuto  = "auto"
)

// callMinPriority is the lowest Gotify priority that still triggers Bark's
// `call` flag. `call` keeps the phone ringing for 30 seconds, which is only
// tolerable for genuinely urgent messages.
const callMinPriority = 9

// barkLevelForPriority maps a Gotify priority onto Bark's interruption level.
//
// The thresholds follow the mapping that works well with the Bark iOS app:
//
//	< 1   -> passive        (silent, only shows in the notification centre)
//	1-3   -> active         (normal notification)
//	4-7   -> timeSensitive  (breaks through Focus modes)
//	> 7   -> critical       (breaks through the mute switch, needs app consent)
func barkLevelForPriority(priority int) string {
	switch {
	case priority < 1:
		return "passive"
	case priority <= 3:
		return "active"
	case priority <= 7:
		return "timeSensitive"
	default:
		return "critical"
	}
}

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
//
// In encrypted mode the payload is sent as the AES ciphertext instead and the
// device key travels in the URL, so DeviceKey is omitted there.
type barkPush struct {
	Body      string `json:"body"`
	DeviceKey string `json:"device_key,omitempty"`
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

// barkRequest is a fully prepared HTTP call to a bark-server.
type barkRequest struct {
	endpoint    string
	contentType string
	body        []byte
}

// buildBarkRequest turns a push into the HTTP call to perform: a plaintext
// POST /push, or, when a key is configured, an encrypted POST /{device_key}
// carrying `ciphertext` (plus `iv` in CBC mode).
func buildBarkRequest(cfg *Config, deviceKey string, push barkPush) (*barkRequest, error) {
	base := strings.TrimRight(cfg.ServerURL, "/")

	if cfg.EncryptKey == "" {
		push.DeviceKey = deviceKey
		payload, err := json.Marshal(push)
		if err != nil {
			return nil, fmt.Errorf("encode bark payload: %w", err)
		}
		return &barkRequest{
			endpoint:    base + "/push",
			contentType: "application/json; charset=utf-8",
			body:        payload,
		}, nil
	}

	// Encrypted mode: the device key selects the target through the URL and
	// the payload stays opaque to the server.
	push.DeviceKey = ""
	plain, err := json.Marshal(push)
	if err != nil {
		return nil, fmt.Errorf("encode bark payload: %w", err)
	}
	ciphertext, iv, err := encryptBarkPayload(cfg.EncryptKey, cfg.EncryptMode, cfg.EncryptIV, plain)
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("ciphertext", ciphertext)
	if iv != "" {
		form.Set("iv", iv)
	}
	return &barkRequest{
		endpoint:    base + "/" + url.PathEscape(deviceKey),
		contentType: "application/x-www-form-urlencoded",
		body:        []byte(form.Encode()),
	}, nil
}

// encryptBarkPayload seals plaintext with AES in the configured mode and
// returns the base64 ciphertext together with the IV to transmit ("" for ECB).
// The AES variant follows the key length — 16 bytes is AES-128, 24 is AES-192
// and 32 is AES-256 — matching whichever algorithm was picked in the Bark app.
//
// Bark takes the key and the IV as raw characters. Its shell example
// hex-encodes them only because `openssl enc -K/-iv` demands hex input, so the
// IV that travels on the wire is the plain 16-character string.
func encryptBarkPayload(key, mode, fixedIV string, plaintext []byte) (string, string, error) {
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return "", "", fmt.Errorf("initialise cipher: %w", err)
	}
	padded := pkcs7Pad(plaintext, block.BlockSize())

	if mode == encryptModeECB {
		out := make([]byte, len(padded))
		for i := 0; i < len(padded); i += block.BlockSize() {
			block.Encrypt(out[i:i+block.BlockSize()], padded[i:i+block.BlockSize()])
		}
		return base64.StdEncoding.EncodeToString(out), "", nil
	}

	iv := fixedIV
	if iv == "" {
		// A fresh IV per push keeps identical messages from producing
		// identical ciphertexts. Hex keeps it printable and 16 characters
		// long, matching Bark's own tooling.
		raw := make([]byte, encryptIVLen/2)
		if _, err := rand.Read(raw); err != nil {
			return "", "", fmt.Errorf("generate iv: %w", err)
		}
		iv = hex.EncodeToString(raw)
	}
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, []byte(iv)).CryptBlocks(out, padded)
	return base64.StdEncoding.EncodeToString(out), iv, nil
}

// pkcs7Pad appends PKCS#7 padding, the scheme `openssl enc` applies by default.
func pkcs7Pad(data []byte, blockSize int) []byte {
	n := blockSize - len(data)%blockSize
	return append(append([]byte(nil), data...), bytes.Repeat([]byte{byte(n)}, n)...)
}

// sendBarkPush posts one push to the bark server. It retries transient
// failures with a small backoff so a short bark-server restart does not lose
// notifications.
func sendBarkPush(ctx context.Context, client *http.Client, cfg *Config, deviceKey string, push barkPush) error {
	call, err := buildBarkRequest(cfg, deviceKey, push)
	if err != nil {
		return err
	}

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
		lastErr = postBarkRequest(ctx, client, call)
		if lastErr == nil {
			return nil
		}
		if be, ok := lastErr.(*barkError); ok && be.isPermanent() {
			return lastErr
		}
	}
	return lastErr
}

func postBarkRequest(ctx context.Context, client *http.Client, call *barkRequest) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, call.endpoint, bytes.NewReader(call.body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", call.contentType)
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

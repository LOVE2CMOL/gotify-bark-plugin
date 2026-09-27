// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 DSH
//
// This program is free software: you can redistribute it and/or modify it under
// the terms of the GNU General Public License as published by the Free Software
// Foundation, either version 3 of the License, or (at your option) any later
// version.
//
// This program is distributed in the hope that it will be useful, but WITHOUT
// ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS
// FOR A PARTICULAR PURPOSE.  See the GNU General Public License for more
// details.
//
// You should have received a copy of the GNU General Public License along with
// this program.  If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// DefaultServerURL is the public Bark service, used when the user leaves the
// server field empty.
const DefaultServerURL = "https://api.day.app"

// Config is the plugin configuration, edited by the user in the Gotify WebUI
// (Plugins -> Bark Forwarder -> Configuration). Gotify marshals it to YAML.
type Config struct {
	// Enabled toggles the whole forwarding pipeline.
	Enabled bool `yaml:"enabled"`
	// GotifyURL is the base URL of the Gotify server this plugin runs on.
	// Leave empty to auto-detect it from the request that renders the plugin
	// page; set it explicitly when the auto-detected address is not reachable
	// from inside the container (very common with Docker).
	GotifyURL string `yaml:"gotify_url"`
	// ClientToken is a Gotify *client* token (WebUI -> Clients -> Create
	// Client). The value shown when the client is created is the private form
	// and is the one to paste here; a public "gtfy..." token works as well.
	ClientToken string `yaml:"client_token"`
	// DeviceKeys holds one or more Bark device keys, separated by comma, space
	// or newline. A full Bark URL is accepted too, the key is extracted.
	DeviceKeys string `yaml:"device_keys"`

	// ServerURL is the bark-server base URL, e.g. https://api.day.app or
	// http://192.168.1.10:8080.
	ServerURL string `yaml:"server_url"`

	// --- 端到端加密（Bark App 里的「加密」开关）--------------------------
	// EncryptKey enables Bark's end-to-end encryption. Its length picks the
	// AES variant (16 = AES-128, 24 = AES-192, 32 = AES-256) and must be
	// identical to the key and algorithm configured in the Bark app. Empty
	// means plaintext pushes. See https://bark.day.app/#/encryption
	EncryptKey string `yaml:"encrypt_key"`
	// EncryptMode selects the cipher mode: "cbc" (default, one IV per push) or
	// "ecb" (no IV).
	EncryptMode string `yaml:"encrypt_mode"`
	// EncryptIV pins the CBC initialisation vector (16 characters). Bark's own
	// tooling generates a random IV for every message, which is what happens
	// when this is left empty; set it only to reproduce a fixed ciphertext.
	EncryptIV string `yaml:"encrypt_iv"`

	// DefaultGroup is applied when the Gotify application name is empty.
	DefaultGroup string `yaml:"default_group"`
	// GroupByApp uses the Gotify application name as the Bark group.
	GroupByApp bool `yaml:"group_by_app"`
	// Level is the Bark interruption level: active, timeSensitive, passive or
	// critical (empty = Bark default).
	Level string `yaml:"level"`
	// Sound is the Bark ringtone name, e.g. alarm, minuet, multiwayinvitation.
	Sound string `yaml:"sound"`
	// Icon is an image URL shown as the notification icon (iOS 15+).
	Icon string `yaml:"icon"`
	// Call set to "1" to keep the ringtone playing for 30 seconds.
	Call string `yaml:"call"`
	// AutoCopy set to "1" to let Bark copy the message body to the clipboard.
	AutoCopy string `yaml:"auto_copy"`
	// CopyTemplate is the text copied to the clipboard, "1" means the body.
	CopyTemplate string `yaml:"copy_template"`
	// URLTemplate is the jump URL opened when the notification is tapped.
	// Placeholders: {appid} {appname} {title} {message} {priority}.
	URLTemplate string `yaml:"url_template"`
	// Action set to "none" makes tapping the notification do nothing.
	Action string `yaml:"action"`
	// Archive set to "1" asks Bark to keep the message in its history.
	Archive string `yaml:"archive"`
	// TTL is the archive lifetime in seconds (0 = Bark default).
	TTL int `yaml:"ttl"`
	// Badge is the app icon badge number (-1 = leave untouched).
	Badge int `yaml:"badge"`

	// MinPriority drops messages below this Gotify priority.
	MinPriority int `yaml:"min_priority"`
	// SkipTitlePrefix drops messages whose title starts with this string.
	SkipTitlePrefix string `yaml:"skip_title_prefix"`
	// IncludeExtras appends the Gotify message extras (as JSON) to the body.
	IncludeExtras bool `yaml:"include_extras"`

	// TimeoutSeconds is the HTTP timeout for a single Bark request.
	TimeoutSeconds int `yaml:"timeout_seconds"`
	// Debug logs every forwarded / skipped message.
	Debug bool `yaml:"debug"`
	// DryRun logs what would be sent without calling Bark.
	DryRun bool `yaml:"dry_run"`
}

// DefaultConfig implements plugin.Configurer. The returned value must always be
// accepted by ValidateAndSetConfig, otherwise Gotify would disable the plugin
// on a fresh install.
func (p *Plugin) DefaultConfig() interface{} {
	return &Config{
		Enabled:        false,
		ServerURL:      DefaultServerURL,
		GroupByApp:     true,
		Level:          "active",
		Badge:          -1,
		MinPriority:    -2,
		TimeoutSeconds: 10,
	}
}

var (
	soundPattern = regexp.MustCompile(`^[A-Za-z0-9_.\-]+$`)
	levelValues  = map[string]bool{"": true, levelAuto: true, "active": true, "timeSensitive": true, "passive": true, "critical": true}
	actionValues = map[string]bool{"": true, "none": true}
)

// ValidateAndSetConfig implements plugin.Configurer. It normalises the user
// input and rejects configurations that cannot work, so that a broken config
// never reaches the forwarding goroutine.
func (p *Plugin) ValidateAndSetConfig(config interface{}) error {
	cfg, ok := config.(*Config)
	if !ok {
		return fmt.Errorf("unexpected config type %T", config)
	}

	normalized, err := normalizeConfig(cfg)
	if err != nil {
		return err
	}

	p.mu.Lock()
	p.config = normalized
	p.mu.Unlock()

	// Apply the new settings to a running subscription immediately.
	p.restart()
	return nil
}

func normalizeConfig(cfg *Config) (*Config, error) {
	out := *cfg

	// --- Gotify base URL -------------------------------------------------
	out.GotifyURL = strings.TrimSpace(out.GotifyURL)
	if out.GotifyURL != "" {
		u, err := url.Parse(out.GotifyURL)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("gotify_url %q is not a valid URL", cfg.GotifyURL)
		}
		switch u.Scheme {
		case "http", "https", "ws", "wss":
		default:
			return nil, fmt.Errorf("gotify_url must use http, https, ws or wss, got %q", u.Scheme)
		}
		u.RawQuery, u.Fragment = "", ""
		out.GotifyURL = strings.TrimRight(u.String(), "/")
	}

	// --- client token ----------------------------------------------------
	out.ClientToken = normalizeToken(out.ClientToken)
	if strings.Contains(out.ClientToken, "://") {
		return nil, errors.New("client_token looks like a URL: paste the Gotify *client* token (WebUI -> Clients), not the server address")
	}

	// --- device keys -----------------------------------------------------
	keys, err := parseDeviceKeys(out.DeviceKeys)
	if err != nil {
		return nil, err
	}
	// An empty device list is accepted on purpose: the default configuration
	// must pass validation, otherwise Gotify disables the plugin right after
	// installation. start() refuses to forward until a key is configured.
	out.DeviceKeys = strings.Join(keys, ",")

	// --- bark server -----------------------------------------------------
	out.ServerURL = strings.TrimSpace(out.ServerURL)
	if out.ServerURL == "" {
		out.ServerURL = DefaultServerURL
	}
	if !strings.Contains(out.ServerURL, "://") {
		// `bark:8080` is a Docker service name or a LAN host, and those never
		// speak TLS; `bark.example.com` is a public domain and almost always
		// does. Guessing wrong here is the single most common setup mistake,
		// so pick the scheme instead of blindly assuming https.
		out.ServerURL = inferServerScheme(out.ServerURL) + "://" + out.ServerURL
	}
	su, err := url.Parse(out.ServerURL)
	if err != nil || su.Host == "" {
		return nil, fmt.Errorf("server_url %q is not a valid URL", cfg.ServerURL)
	}
	if su.Scheme != "http" && su.Scheme != "https" {
		return nil, fmt.Errorf("server_url must use http or https, got %q", su.Scheme)
	}
	su.RawQuery, su.Fragment = "", ""
	out.ServerURL = strings.TrimRight(su.String(), "/")

	// --- end-to-end encryption -------------------------------------------
	out.EncryptKey = strings.TrimSpace(out.EncryptKey)
	out.EncryptMode = strings.ToLower(strings.TrimSpace(out.EncryptMode))
	out.EncryptIV = strings.TrimSpace(out.EncryptIV)
	if out.EncryptKey != "" {
		if _, ok := encryptKeyLens[len(out.EncryptKey)]; !ok {
			return nil, fmt.Errorf("encrypt_key must be 16, 24 or 32 characters (AES128/AES192/AES256, matching the algorithm picked in the Bark app), got %d", len(out.EncryptKey))
		}
		if out.EncryptMode == "" {
			out.EncryptMode = encryptModeCBC
		}
		if !encryptModes[out.EncryptMode] {
			return nil, fmt.Errorf("encrypt_mode must be %q or %q, got %q", encryptModeCBC, encryptModeECB, cfg.EncryptMode)
		}
		if out.EncryptIV != "" && len(out.EncryptIV) != encryptIVLen {
			return nil, fmt.Errorf("encrypt_iv must be exactly %d characters, got %d", encryptIVLen, len(out.EncryptIV))
		}
		if out.EncryptMode == encryptModeECB {
			// ECB has no IV; sending one would only confuse the app.
			out.EncryptIV = ""
		}
	} else {
		out.EncryptMode = ""
		out.EncryptIV = ""
	}

	// --- enum-ish options ------------------------------------------------
	out.Level = strings.TrimSpace(out.Level)
	if strings.EqualFold(out.Level, levelAuto) {
		out.Level = levelAuto
	}
	if !levelValues[out.Level] {
		return nil, fmt.Errorf("level must be one of auto, active, timeSensitive, passive, critical (or empty), got %q", cfg.Level)
	}
	out.Action = strings.TrimSpace(out.Action)
	if !actionValues[out.Action] {
		return nil, fmt.Errorf("action must be empty or \"none\", got %q", cfg.Action)
	}

	// --- sound / flags ---------------------------------------------------
	out.Sound = strings.TrimSpace(out.Sound)
	if out.Sound != "" && !soundPattern.MatchString(out.Sound) {
		return nil, fmt.Errorf("sound %q contains unsupported characters", cfg.Sound)
	}
	out.Call = normalizeFlag(out.Call)
	out.AutoCopy = normalizeFlag(out.AutoCopy)
	out.Archive = normalizeFlag(out.Archive)

	// --- numbers ---------------------------------------------------------
	if out.TTL < 0 {
		return nil, errors.New("ttl must not be negative")
	}
	if out.Badge < -1 {
		return nil, errors.New("badge must be -1 (keep) or a non-negative number")
	}
	if out.MinPriority < -2 || out.MinPriority > 10 {
		return nil, fmt.Errorf("min_priority must be between -2 and 10, got %d", cfg.MinPriority)
	}
	if out.TimeoutSeconds <= 0 {
		out.TimeoutSeconds = 10
	}
	if out.TimeoutSeconds > 120 {
		return nil, errors.New("timeout_seconds must be at most 120")
	}

	// --- templates -------------------------------------------------------
	out.URLTemplate = strings.TrimSpace(out.URLTemplate)
	if out.URLTemplate != "" {
		tu, err := url.Parse(out.URLTemplate)
		if err != nil || tu.Scheme == "" {
			return nil, fmt.Errorf("url_template %q is not a valid absolute URL", cfg.URLTemplate)
		}
	}
	out.CopyTemplate = strings.TrimSpace(out.CopyTemplate)
	out.DefaultGroup = strings.TrimSpace(out.DefaultGroup)
	out.SkipTitlePrefix = strings.TrimSpace(out.SkipTitlePrefix)
	out.Icon = strings.TrimSpace(out.Icon)
	switch {
	case out.Icon == "":
		// no icon
	case strings.EqualFold(out.Icon, iconAuto):
		// Reuse whatever icon the Gotify application is configured with.
		out.Icon = iconAuto
	default:
		if iu, err := url.Parse(out.Icon); err != nil || iu.Scheme == "" || iu.Host == "" {
			return nil, fmt.Errorf("icon %q must be an absolute URL or %q", cfg.Icon, iconAuto)
		}
	}

	return &out, nil
}

// normalizeToken trims whitespace, quotes and an optional Bearer prefix that
// users tend to copy along with the token.
func normalizeToken(raw string) string {
	t := strings.TrimSpace(raw)
	t = strings.Trim(t, `"'`)
	t = strings.TrimSpace(t)
	if len(t) > 7 && strings.EqualFold(t[:7], "bearer ") {
		t = strings.TrimSpace(t[7:])
	}
	return t
}

// normalizeFlag turns on/off/true/false/yes/no into Bark's "1" / "".
func normalizeFlag(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on", "y":
		return "1"
	case "", "0", "false", "no", "off", "n":
		return ""
	default:
		return "1"
	}
}

// parseDeviceKeys splits the device key list and normalises every entry.
// A full Bark push URL is accepted and reduced to its device key.
func parseDeviceKeys(raw string) ([]string, error) {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	seen := make(map[string]bool, len(fields))
	keys := make([]string, 0, len(fields))
	for _, f := range fields {
		key := normalizeDeviceKey(f)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, nil
	}
	for _, k := range keys {
		if strings.ContainsAny(k, "/?#&") {
			return nil, fmt.Errorf("device key %q contains unsupported characters", k)
		}
	}
	return keys, nil
}

func normalizeDeviceKey(raw string) string {
	k := strings.TrimSpace(raw)
	k = strings.Trim(k, `"'`)
	if strings.Contains(k, "://") {
		if u, err := url.Parse(k); err == nil {
			segments := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(segments) > 0 && segments[0] != "" {
				k = segments[0]
			}
		}
	}
	return strings.Trim(k, "/")
}

// inferServerScheme guesses http or https for a server_url typed without a
// scheme. Single-label hosts — a Docker service name such as `bark`, or
// `localhost` — and private/loopback IPs get http; public domains and public
// IPs get https.
func inferServerScheme(raw string) string {
	host := raw
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")

	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
			return "http"
		}
		return "https"
	}
	if host == "" || !strings.Contains(host, ".") {
		// No dot at all: a container/service name or a NetBIOS-style short
		// name, both of which live on the LAN.
		return "http"
	}
	return "https"
}

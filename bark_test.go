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
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"strings"
	"testing"
)

// TestEncryptBarkPayloadMatchesBarkExample pins the implementation to the
// ciphertext published in Bark's own encryption documentation. The sample
// values come from https://bark.day.app/#/encryption and the expected output is
// the one that page prints.
func TestEncryptBarkPayloadMatchesBarkExample(t *testing.T) {
	const (
		key       = "jfhgujcjd12456ghgfhyrg123085sfzb"
		iv        = "9b0e96c8eac84683"
		plaintext = `{"body": "test", "sound": "birdsong"}`
		want      = "sUvOzqjXxWXUvFjSG8tFiMVehZNpRc4FQ04REMTS67X1uatyDLO8sWoB5Op6Py7J"
	)

	got, gotIV, err := encryptBarkPayload(key, encryptModeCBC, iv, []byte(plaintext))
	if err != nil {
		t.Fatalf("encryptBarkPayload: %v", err)
	}
	if got != want {
		t.Errorf("ciphertext mismatch\n got: %s\nwant: %s", got, want)
	}
	if gotIV != iv {
		t.Errorf("iv mismatch: got %q want %q", gotIV, iv)
	}
}

func TestEncryptBarkPayloadCBCGeneratesSixteenCharIV(t *testing.T) {
	const key = "jfhgujcjd12456ghgfhyrg123085sfzb"
	seen := map[string]bool{}
	for i := 0; i < 32; i++ {
		_, iv, err := encryptBarkPayload(key, encryptModeCBC, "", []byte(`{"body":"x"}`))
		if err != nil {
			t.Fatalf("encryptBarkPayload: %v", err)
		}
		if len(iv) != encryptIVLen {
			t.Fatalf("iv %q has length %d, want %d", iv, len(iv), encryptIVLen)
		}
		seen[iv] = true
	}
	if len(seen) < 30 {
		t.Errorf("expected a fresh IV per push, got only %d distinct IVs in 32 runs", len(seen))
	}
}

func TestEncryptBarkPayloadECBSendsNoIV(t *testing.T) {
	const key = "jfhgujcjd12456ghgfhyrg123085sfzb"
	ciphertext, iv, err := encryptBarkPayload(key, encryptModeECB, "", []byte(`{"body":"x"}`))
	if err != nil {
		t.Fatalf("encryptBarkPayload: %v", err)
	}
	if iv != "" {
		t.Errorf("ECB must not transmit an IV, got %q", iv)
	}
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		t.Fatalf("ciphertext is not valid base64: %v", err)
	}
	if len(raw)%16 != 0 || len(raw) == 0 {
		t.Errorf("ciphertext length %d is not a positive multiple of the block size", len(raw))
	}
}

func TestPKCS7Pad(t *testing.T) {
	cases := []struct {
		in    string
		bs    int
		want  int
		lastB byte
	}{
		{"", 16, 16, 16},
		{"0123456789abcde", 16, 16, 1},   // 15 bytes: one byte of padding
		{"0123456789abcdef", 16, 32, 16}, // full block gains a whole padding block
		{"0123456789abcdefg", 16, 32, 15},
	}
	for _, c := range cases {
		got := pkcs7Pad([]byte(c.in), c.bs)
		if len(got) != c.want {
			t.Errorf("pkcs7Pad(%q) length = %d, want %d", c.in, len(got), c.want)
			continue
		}
		if last := got[len(got)-1]; last != c.lastB {
			t.Errorf("pkcs7Pad(%q) last byte = %d, want %d", c.in, last, c.lastB)
		}
		if !strings.HasPrefix(string(got), c.in) {
			t.Errorf("pkcs7Pad(%q) altered the input", c.in)
		}
	}
}

// TestBuildBarkRequestRouting checks that the encrypted mode switches both the
// endpoint (/push -> /{device_key}) and the encoding (JSON -> form).
func TestBuildBarkRequestRouting(t *testing.T) {
	push := barkPush{Body: "hello", Title: "t"}

	plain, err := buildBarkRequest(&Config{ServerURL: "https://api.day.app"}, "devkey", push)
	if err != nil {
		t.Fatalf("plaintext request: %v", err)
	}
	if plain.endpoint != "https://api.day.app/push" {
		t.Errorf("plaintext endpoint = %q", plain.endpoint)
	}
	if !strings.Contains(string(plain.body), `"device_key":"devkey"`) {
		t.Errorf("plaintext body should carry the device key: %s", plain.body)
	}

	enc, err := buildBarkRequest(&Config{
		ServerURL:   "https://api.day.app/",
		EncryptKey:  "jfhgujcjd12456ghgfhyrg123085sfzb",
		EncryptMode: encryptModeCBC,
	}, "devkey", push)
	if err != nil {
		t.Fatalf("encrypted request: %v", err)
	}
	if enc.endpoint != "https://api.day.app/devkey" {
		t.Errorf("encrypted endpoint = %q", enc.endpoint)
	}
	if enc.contentType != "application/x-www-form-urlencoded" {
		t.Errorf("encrypted content type = %q", enc.contentType)
	}
	if strings.Contains(string(enc.body), "hello") {
		t.Errorf("encrypted body must not leak the plaintext: %s", enc.body)
	}
	if !strings.Contains(string(enc.body), "ciphertext=") || !strings.Contains(string(enc.body), "iv=") {
		t.Errorf("encrypted body must carry ciphertext and iv: %s", enc.body)
	}
}

func intPtr(v int) *int { return &v }

// TestBarkLevelForPriority pins the auto level mapping agreed for the Bark app.
func TestBarkLevelForPriority(t *testing.T) {
	cases := []struct {
		priority int
		want     string
	}{
		{-2, "passive"}, {-1, "passive"}, {0, "passive"},
		{1, "active"}, {2, "active"}, {3, "active"},
		{4, "timeSensitive"}, {5, "timeSensitive"}, {6, "timeSensitive"}, {7, "timeSensitive"},
		{8, "critical"}, {9, "critical"}, {10, "critical"},
	}
	for _, c := range cases {
		if got := barkLevelForPriority(c.priority); got != c.want {
			t.Errorf("barkLevelForPriority(%d) = %q, want %q", c.priority, got, c.want)
		}
	}
}

func TestBuildBarkPushAutoLevel(t *testing.T) {
	cfg := &Config{Level: levelAuto, DeviceKeys: "k"}
	for _, c := range []struct {
		priority int
		want     string
	}{{0, "passive"}, {2, "active"}, {6, "timeSensitive"}, {9, "critical"}} {
		msg := gotifyMessage{Priority: intPtr(c.priority)}
		if got := buildBarkPush(cfg, msg, "k", applicationInfo{}, "").Level; got != c.want {
			t.Errorf("priority %d -> level %q, want %q", c.priority, got, c.want)
		}
	}

	// A fixed level must still win over the priority.
	fixed := &Config{Level: "passive"}
	if got := buildBarkPush(fixed, gotifyMessage{Priority: intPtr(9)}, "k", applicationInfo{}, "").Level; got != "passive" {
		t.Errorf("fixed level was overridden: got %q", got)
	}
}

// TestBuildBarkPushCallRequiresHighPriority covers the rule that `call` only
// fires for Gotify priority >= 9, never for ordinary messages.
func TestBuildBarkPushCallRequiresHighPriority(t *testing.T) {
	cfg := &Config{Call: "1"}
	for _, c := range []struct {
		priority int
		want     string
	}{{-2, ""}, {0, ""}, {5, ""}, {8, ""}, {9, "1"}, {10, "1"}} {
		msg := gotifyMessage{Priority: intPtr(c.priority)}
		if got := buildBarkPush(cfg, msg, "k", applicationInfo{}, "").Call; got != c.want {
			t.Errorf("priority %d -> call %q, want %q", c.priority, got, c.want)
		}
	}

	// call disabled stays disabled no matter the priority.
	off := &Config{}
	if got := buildBarkPush(off, gotifyMessage{Priority: intPtr(10)}, "k", applicationInfo{}, "").Call; got != "" {
		t.Errorf("call must stay empty when not configured, got %q", got)
	}
}

func TestBuildBarkPushAutoIcon(t *testing.T) {
	cfg := &Config{Icon: iconAuto}
	app := applicationInfo{Name: "testapp", Image: "static/defaultapp.png"}
	got := buildBarkPush(cfg, gotifyMessage{}, "k", app, "http://gotify:80").Icon
	if want := "http://gotify:80/static/defaultapp.png"; got != want {
		t.Errorf("auto icon = %q, want %q", got, want)
	}

	// An application without an icon must not produce one.
	got = buildBarkPush(cfg, gotifyMessage{}, "k", applicationInfo{}, "http://gotify:80").Icon
	if got != "" {
		t.Errorf("auto icon without application image = %q, want empty", got)
	}

	// An explicit icon still wins.
	explicit := &Config{Icon: "https://cdn.example/x.png"}
	if got := buildBarkPush(explicit, gotifyMessage{}, "k", app, "http://gotify:80").Icon; got != "https://cdn.example/x.png" {
		t.Errorf("explicit icon was overridden: %q", got)
	}
}

func TestResolveAppIcon(t *testing.T) {
	cases := []struct {
		base, image, want string
	}{
		{"http://gotify:80", "static/defaultapp.png", "http://gotify:80/static/defaultapp.png"},
		{"http://gotify:80/", "/static/defaultapp.png", "http://gotify:80/static/defaultapp.png"},
		{"https://push.example/gotify", "static/a.png", "https://push.example/gotify/static/a.png"},
		{"http://gotify:80", "https://cdn.example/i.png", "https://cdn.example/i.png"},
		{"http://gotify:80", "http://cdn.example/i.png", "http://cdn.example/i.png"},
		{"http://gotify:80", "", ""},
		{"http://gotify:80", "   ", ""},
		{"", "static/a.png", ""},
	}
	for _, c := range cases {
		if got := resolveAppIcon(c.base, c.image); got != c.want {
			t.Errorf("resolveAppIcon(%q, %q) = %q, want %q", c.base, c.image, got, c.want)
		}
	}
}

// TestBuildBarkPushUsesApplicationNameForGroup checks the group keeps working
// now that it is fed by applicationInfo instead of a bare string.
func TestBuildBarkPushUsesApplicationNameForGroup(t *testing.T) {
	cfg := &Config{GroupByApp: true, URLTemplate: "https://x.example/{appname}/{messageid}"}
	app := applicationInfo{Name: "my app"}
	push := buildBarkPush(cfg, gotifyMessage{ID: 42}, "k", app, "")
	if push.Group != "my app" {
		t.Errorf("group = %q, want %q", push.Group, "my app")
	}
	if want := "https://x.example/my%20app/42"; push.URL != want {
		t.Errorf("url = %q, want %q", push.URL, want)
	}

	fallback := &Config{GroupByApp: true, DefaultGroup: "gotify"}
	if got := buildBarkPush(fallback, gotifyMessage{}, "k", applicationInfo{}, "").Group; got != "gotify" {
		t.Errorf("default group fallback = %q, want %q", got, "gotify")
	}
}

// TestEncryptBarkPayloadAES128MatchesBarkExample pins the AES-128 path to the
// ciphertexts printed in Bark's own encryption documentation (the Chinese and
// English pages use a different IV). Bark derives the AES variant from the key
// length, so a 16-character key has to reproduce `openssl enc -aes-128-cbc`.
func TestEncryptBarkPayloadAES128MatchesBarkExample(t *testing.T) {
	const (
		key       = "1234567890123456"
		plaintext = `{"body": "test", "sound": "birdsong"}`
	)
	cases := []struct {
		name string
		iv   string
		want string
	}{
		{
			name: "chinese documentation example",
			iv:   "1234567890123456",
			want: "+aPt5cwN9GbTLLSFri60l3h1X00u/9j1FENfWiTxhNHVLGU+XoJ15JJG5W/d/yf0",
		},
		{
			name: "english documentation example",
			iv:   "1111111111111111",
			want: "d3QhjQjP5majvNt5CjsvFWwqqj2gKl96RFj5OO+u6ynTt7lkyigDYNA3abnnCLpr",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, gotIV, err := encryptBarkPayload(key, encryptModeCBC, c.iv, []byte(plaintext))
			if err != nil {
				t.Fatalf("encryptBarkPayload: %v", err)
			}
			if got != c.want {
				t.Errorf("ciphertext mismatch\n got: %s\nwant: %s", got, c.want)
			}
			if gotIV != c.iv {
				t.Errorf("iv mismatch: got %q want %q", gotIV, c.iv)
			}
		})
	}
}

// TestEncryptBarkPayloadRoundTripsEveryKeyLength covers AES-128, AES-192 and
// AES-256, which is exactly how the Bark app selects the algorithm: by key
// length. AES-192 has no published sample, so it is verified by decryption.
func TestEncryptBarkPayloadRoundTripsEveryKeyLength(t *testing.T) {
	plaintext := []byte(`{"body":"中文、emoji 🐋 和符号 +/ 都要原样还原"}`)
	keys := []struct {
		name string
		key  string
	}{
		{"AES-128", "1234567890123456"},
		{"AES-192", "123456789012345678901234"},
		{"AES-256", "jfhgujcjd12456ghgfhyrg123085sfzb"},
	}
	for _, c := range keys {
		t.Run(c.name, func(t *testing.T) {
			ciphertext, iv, err := encryptBarkPayload(c.key, encryptModeCBC, "", plaintext)
			if err != nil {
				t.Fatalf("encryptBarkPayload: %v", err)
			}
			raw, err := base64.StdEncoding.DecodeString(ciphertext)
			if err != nil {
				t.Fatalf("decode ciphertext: %v", err)
			}
			block, err := aes.NewCipher([]byte(c.key))
			if err != nil {
				t.Fatalf("aes.NewCipher: %v", err)
			}
			if len(raw)%block.BlockSize() != 0 {
				t.Fatalf("ciphertext length %d is not a multiple of the block size", len(raw))
			}
			plain := make([]byte, len(raw))
			cipher.NewCBCDecrypter(block, []byte(iv)).CryptBlocks(plain, raw)
			plain = pkcs7Unpad(t, plain)
			if string(plain) != string(plaintext) {
				t.Errorf("round-trip mismatch\n got: %q\nwant: %q", plain, plaintext)
			}
		})
	}
}

func pkcs7Unpad(t *testing.T, data []byte) []byte {
	t.Helper()
	if len(data) == 0 {
		t.Fatal("plaintext is empty")
	}
	n := int(data[len(data)-1])
	if n == 0 || n > len(data) {
		t.Fatalf("invalid PKCS#7 padding length %d", n)
	}
	return data[:len(data)-n]
}

// TestNormalizeConfigInfersServerScheme covers the "forgot the scheme" case:
// container names and LAN addresses get http, public names get https.
func TestNormalizeConfigInfersServerScheme(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"bark:8080", "http://bark:8080"},
		{"bark", "http://bark"},
		{"localhost:8080", "http://localhost:8080"},
		{"127.0.0.1:8080", "http://127.0.0.1:8080"},
		{"192.168.1.10:8080", "http://192.168.1.10:8080"},
		{"10.0.0.5", "http://10.0.0.5"},
		{"172.16.3.4:8080", "http://172.16.3.4:8080"},
		{"bark.example.com:4430", "https://bark.example.com:4430"},
		{"api.day.app", "https://api.day.app"},
		{"8.8.8.8", "https://8.8.8.8"},
		{"http://bark:8080", "http://bark:8080"},
		{"https://api.day.app/", "https://api.day.app"},
	}
	for _, c := range cases {
		got, err := normalizeConfig(&Config{ServerURL: c.in, ClientToken: "gtfyc.x", DeviceKeys: "key1"})
		if err != nil {
			t.Fatalf("normalizeConfig(%q): %v", c.in, err)
		}
		if got.ServerURL != c.want {
			t.Errorf("server_url %q normalized to %q, want %q", c.in, got.ServerURL, c.want)
		}
	}
}

// TestNormalizeConfigAcceptsEveryAESKeyLength checks the three key sizes the
// Bark app offers; any other length would make aes.NewCipher fail at push time.
func TestNormalizeConfigAcceptsEveryAESKeyLength(t *testing.T) {
	accepted := []string{
		"1234567890123456",                 // 16 -> AES128
		"123456789012345678901234",         // 24 -> AES192
		"jfhgujcjd12456ghgfhyrg123085sfzb", // 32 -> AES256
	}
	for _, key := range accepted {
		if _, err := normalizeConfig(&Config{ServerURL: "https://api.day.app", ClientToken: "gtfyc.x", DeviceKeys: "key1", EncryptKey: key}); err != nil {
			t.Errorf("encrypt_key with %d characters rejected: %v", len(key), err)
		}
	}

	rejected := []string{"short", "12345678901234567890", strings.Repeat("x", 33)}
	for _, key := range rejected {
		if _, err := normalizeConfig(&Config{ServerURL: "https://api.day.app", ClientToken: "gtfyc.x", DeviceKeys: "key1", EncryptKey: key}); err == nil {
			t.Errorf("encrypt_key with %d characters should have been rejected", len(key))
		}
	}
}

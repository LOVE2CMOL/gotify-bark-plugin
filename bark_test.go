package main

import (
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

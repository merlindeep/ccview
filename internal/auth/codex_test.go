package auth

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// makeJWT builds an unsigned JWT whose payload carries the given exp claim (in
// Unix seconds). Only the payload segment matters to jwtExpiryMillis.
func makeJWT(expUnix int64) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":` + strconv.FormatInt(expUnix, 10) + `}`))
	return header + "." + payload + ".sig"
}

func TestCodexResolveEnv(t *testing.T) {
	exp := time.Now().Add(time.Hour).Unix()
	token := makeJWT(exp)
	r := &CodexResolver{Getenv: func(k string) string {
		if k == CodexEnvToken {
			return " " + token + " "
		}
		return ""
	}}
	creds, err := r.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if creds.AccessToken != token {
		t.Errorf("token not trimmed/returned: %q", creds.AccessToken)
	}
	if creds.Source != "env "+CodexEnvToken {
		t.Errorf("source = %q", creds.Source)
	}
	got, ok := creds.ExpiresAtTime()
	if !ok || got.Unix() != exp {
		t.Errorf("expiry = %v (ok=%v), want unix %d", got, ok, exp)
	}
}

func TestCodexResolveFile(t *testing.T) {
	dir := t.TempDir()
	codexDir := filepath.Join(dir, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	token := makeJWT(time.Now().Add(2 * time.Hour).Unix())
	blob := `{"auth_mode":"chatgpt","tokens":{"access_token":"` + token + `","account_id":"acct-42"}}`
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), []byte(blob), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &CodexResolver{
		Getenv:      func(string) string { return "" },
		UserHomeDir: func() (string, error) { return dir, nil },
		ReadFile:    os.ReadFile,
	}
	creds, err := r.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if creds.AccessToken != token || creds.AccountID != "acct-42" {
		t.Errorf("creds = %+v", creds)
	}
	if _, ok := creds.ExpiresAtTime(); !ok {
		t.Error("expected expiry from JWT")
	}
}

func TestCodexResolveNotFound(t *testing.T) {
	r := &CodexResolver{
		Getenv:      func(string) string { return "" },
		UserHomeDir: func() (string, error) { return t.TempDir(), nil },
		ReadFile:    os.ReadFile,
	}
	if _, err := r.Resolve(); !errors.Is(err, ErrCodexNotFound) {
		t.Errorf("err = %v, want ErrCodexNotFound", err)
	}
}

func TestCodexResolveMalformedFile(t *testing.T) {
	dir := t.TempDir()
	codexDir := filepath.Join(dir, ".codex")
	_ = os.MkdirAll(codexDir, 0o755)
	// Present but no access token.
	_ = os.WriteFile(filepath.Join(codexDir, "auth.json"), []byte(`{"tokens":{}}`), 0o600)
	r := &CodexResolver{
		Getenv:      func(string) string { return "" },
		UserHomeDir: func() (string, error) { return dir, nil },
		ReadFile:    os.ReadFile,
	}
	if _, err := r.Resolve(); !errors.Is(err, ErrCodexNotFound) {
		t.Errorf("err = %v, want ErrCodexNotFound", err)
	}
}

func TestCodexDiagnose(t *testing.T) {
	dir := t.TempDir()
	codexDir := filepath.Join(dir, ".codex")
	_ = os.MkdirAll(codexDir, 0o755)
	_ = os.WriteFile(filepath.Join(codexDir, "auth.json"),
		[]byte(`{"tokens":{"access_token":"`+makeJWT(0)+`","account_id":"a"}}`), 0o600)
	r := &CodexResolver{
		Getenv:      func(string) string { return "" },
		UserHomeDir: func() (string, error) { return dir, nil },
		ReadFile:    os.ReadFile,
	}
	diags := r.Diagnose()
	if len(diags) != 2 {
		t.Fatalf("diags = %+v", diags)
	}
	if diags[0].Found {
		t.Error("env should not be found")
	}
	if !diags[1].Found {
		t.Errorf("file should be found: %+v", diags[1])
	}
}

func TestJWTExpiryMillis(t *testing.T) {
	// Non-JWT tokens and malformed payloads yield 0.
	if jwtExpiryMillis("opaque-token") != 0 {
		t.Error("non-JWT should yield 0")
	}
	if jwtExpiryMillis("a.b.c") != 0 {
		t.Error("undecodable payload should yield 0")
	}
	// A JWT without exp yields 0.
	noExp := base64.RawURLEncoding.EncodeToString([]byte(`{}`))
	if jwtExpiryMillis("h."+noExp+".s") != 0 {
		t.Error("missing exp should yield 0")
	}
	// A valid exp is returned in milliseconds.
	if got := jwtExpiryMillis(makeJWT(1700000000)); got != 1700000000*1000 {
		t.Errorf("exp millis = %d", got)
	}
}

package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// CodexEnvToken is the environment variable holding a bare Codex access token,
// consulted before the on-disk credentials file.
const CodexEnvToken = "CODEX_ACCESS_TOKEN"

// codexAuthFile is the credentials file written by the Codex CLI, relative to
// the user's home directory.
var codexAuthFile = filepath.Join(".codex", "auth.json")

// ErrCodexNotFound is returned when no Codex credential source yields a token.
var ErrCodexNotFound = errors.New("Codex OAuth token not found (checked $" + CodexEnvToken + ", ~/.codex/auth.json)")

// codexRawCreds matches the JSON shape the Codex CLI stores in ~/.codex/auth.json.
type codexRawCreds struct {
	Tokens struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

// CodexResolver locates Codex credentials. Like [Resolver] it takes its
// environment and filesystem through injectable function fields, so resolution
// is testable without touching the host. It is read-only: the token is never
// written, refreshed, or transmitted anywhere other than the Codex usage
// endpoint.
type CodexResolver struct {
	// Getenv reads an environment variable.
	Getenv func(string) string
	// UserHomeDir returns the current user's home directory.
	UserHomeDir func() (string, error)
	// ReadFile reads a file's contents.
	ReadFile func(string) ([]byte, error)
}

// NewCodex returns a CodexResolver wired to the real host environment.
func NewCodex() *CodexResolver {
	return &CodexResolver{
		Getenv:      os.Getenv,
		UserHomeDir: os.UserHomeDir,
		ReadFile:    os.ReadFile,
	}
}

// Resolve returns the first Codex credentials it can locate: the
// $CODEX_ACCESS_TOKEN environment variable (a bare token), then
// ~/.codex/auth.json. It returns [ErrCodexNotFound] when both are exhausted.
func (r *CodexResolver) Resolve() (Credentials, error) {
	if r.Getenv != nil {
		if token := trim(r.Getenv(CodexEnvToken)); token != "" {
			c := Credentials{AccessToken: token, Source: "env " + CodexEnvToken}
			c.ExpiresAt = jwtExpiryMillis(token)
			return c, nil
		}
	}

	if r.UserHomeDir != nil && r.ReadFile != nil {
		if home, err := r.UserHomeDir(); err == nil {
			path := filepath.Join(home, codexAuthFile)
			if b, err := r.ReadFile(path); err == nil {
				if c, ok := parseCodexRaw(b, path); ok {
					return c, nil
				}
			}
		}
	}

	return Credentials{}, ErrCodexNotFound
}

// parseCodexRaw decodes a Codex credentials blob, returning false when it does
// not contain an access token.
func parseCodexRaw(b []byte, source string) (Credentials, bool) {
	var r codexRawCreds
	if err := json.Unmarshal(b, &r); err != nil {
		return Credentials{}, false
	}
	if r.Tokens.AccessToken == "" {
		return Credentials{}, false
	}
	return Credentials{
		AccessToken: r.Tokens.AccessToken,
		AccountID:   r.Tokens.AccountID,
		ExpiresAt:   jwtExpiryMillis(r.Tokens.AccessToken),
		Source:      source,
	}, true
}

// Diagnose probes every Codex credential source in priority order WITHOUT
// short-circuiting, mirroring [Resolver.Diagnose]. It never returns the token.
func (r *CodexResolver) Diagnose() []SourceDiagnostic {
	return []SourceDiagnostic{
		r.diagnoseEnv(),
		r.diagnoseFile(),
	}
}

func (r *CodexResolver) diagnoseEnv() SourceDiagnostic {
	d := SourceDiagnostic{Name: "env " + CodexEnvToken}
	switch {
	case r.Getenv == nil:
		d.Detail = "unavailable"
	case trim(r.Getenv(CodexEnvToken)) != "":
		d.Found, d.Detail = true, "set"
	default:
		d.Detail = "not set"
	}
	return d
}

func (r *CodexResolver) diagnoseFile() SourceDiagnostic {
	d := SourceDiagnostic{Name: "~/.codex/auth.json"}
	if r.UserHomeDir == nil || r.ReadFile == nil {
		d.Detail = "unavailable"
		return d
	}
	home, err := r.UserHomeDir()
	if err != nil {
		d.Detail = "home directory unknown"
		return d
	}
	b, err := r.ReadFile(filepath.Join(home, codexAuthFile))
	if err != nil {
		d.Detail = "missing"
		return d
	}
	if _, ok := parseCodexRaw(b, ""); ok {
		d.Found, d.Detail = true, "found"
	} else {
		d.Detail = "present but no access token"
	}
	return d
}

// jwtExpiryMillis extracts the "exp" claim (seconds since the Unix epoch) from a
// JWT access token and returns it in Unix milliseconds, or 0 when the token is
// not a JWT or carries no usable expiry. The signature is not verified — the
// value is used only to decide whether to attempt a refresh, never to trust the
// token — and the token itself is never logged.
func jwtExpiryMillis(token string) int64 {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp <= 0 {
		return 0
	}
	return claims.Exp * 1000
}

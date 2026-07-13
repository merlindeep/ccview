package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCodexNewOptions(t *testing.T) {
	c := NewCodex(WithCodexBaseURL("http://example.test"))
	if c.BaseURL != "http://example.test" {
		t.Errorf("BaseURL = %q", c.BaseURL)
	}
	if c.UserAgent == "" {
		t.Error("UserAgent should default")
	}
}

func TestCodexFetchSuccessAndHeaders(t *testing.T) {
	var gotAuth, gotAccount, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccount = r.Header.Get(CodexAccountHeader)
		gotCT = r.Header.Get("Content-Type")
		_, _ = w.Write([]byte(`{"plan_type":"team","rate_limit":{"primary_window":{"used_percent":5,"limit_window_seconds":604800,"reset_at":1784533989}}}`))
	}))
	defer srv.Close()

	c := NewCodex(WithCodexBaseURL(srv.URL))
	u, raw, err := c.Fetch(context.Background(), "secret", "acct-1")
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if u.PlanType != "team" || u.RateLimit.PrimaryWindow == nil || u.RateLimit.PrimaryWindow.UsedPercent != 5 {
		t.Errorf("decoded = %+v", u)
	}
	if len(raw) == 0 {
		t.Error("expected raw body")
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotAccount != "acct-1" {
		t.Errorf("%s = %q", CodexAccountHeader, gotAccount)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q", gotCT)
	}
}

func TestCodexFetchOmitsAccountHeaderWhenEmpty(t *testing.T) {
	var hadAccount bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadAccount = r.Header[http.CanonicalHeaderKey(CodexAccountHeader)]
		_, _ = w.Write([]byte(`{"plan_type":"free"}`))
	}))
	defer srv.Close()

	c := NewCodex(WithCodexBaseURL(srv.URL))
	if _, _, err := c.Fetch(context.Background(), "t", ""); err != nil {
		t.Fatal(err)
	}
	if hadAccount {
		t.Error("account header should be omitted when accountID is empty")
	}
}

func TestCodexFetchNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()

	c := NewCodex(WithCodexBaseURL(srv.URL))
	_, raw, err := c.Fetch(context.Background(), "t", "a")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 401 {
		t.Fatalf("err = %v, want APIError 401", err)
	}
	if string(raw) != "nope" {
		t.Errorf("raw = %q", raw)
	}
}

func TestCodexFetchMalformedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()
	c := NewCodex(WithCodexBaseURL(srv.URL))
	if _, _, err := c.Fetch(context.Background(), "t", "a"); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestCodexFetchBadURL(t *testing.T) {
	c := NewCodex(WithCodexBaseURL("://nope"))
	if _, _, err := c.Fetch(context.Background(), "t", "a"); err == nil {
		t.Fatal("expected request-build error")
	}
}

func TestCodexWithHTTPClient(t *testing.T) {
	hc := &http.Client{}
	c := NewCodex(WithCodexHTTPClient(hc))
	if c.HTTP != hc {
		t.Error("WithCodexHTTPClient not applied")
	}
}

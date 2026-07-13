package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/merlindeep/claude-cost-viewer/internal/auth"
	"github.com/merlindeep/claude-cost-viewer/internal/render"
	"github.com/merlindeep/claude-cost-viewer/internal/tui"
	"github.com/merlindeep/claude-cost-viewer/internal/usage"
)

// fakeCodexFetcher returns a canned Codex payload (or an error).
type fakeCodexFetcher struct {
	c     *usage.Codex
	raw   []byte
	err   error
	calls int
}

func (f *fakeCodexFetcher) Fetch(_ context.Context, _, _ string) (*usage.Codex, []byte, error) {
	f.calls++
	return f.c, f.raw, f.err
}

func sampleCodex() *usage.Codex {
	c, _ := usage.ParseCodex([]byte(`{"plan_type":"team","rate_limit":{"primary_window":{"used_percent":3,"limit_window_seconds":604800,"reset_at":1784533989}}}`))
	return c
}

// withCodex adds a working Codex provider to test deps.
func withCodex(d *deps) *fakeCodexFetcher {
	cf := &fakeCodexFetcher{c: sampleCodex(), raw: []byte(`{"ok":true}`)}
	d.CodexResolver = &fakeResolver{creds: auth.Credentials{AccessToken: "codex-tok", AccountID: "acct", Source: "test-codex"}}
	d.NewCodexFetcher = func() codexFetcher { return cf }
	d.CodexReload = func(context.Context) error { return nil }
	return cf
}

func TestParseProviders(t *testing.T) {
	if p, err := parseProviders("all"); err != nil || p != nil {
		t.Errorf(`parseProviders("all") = %v, %v`, p, err)
	}
	if p, err := parseProviders(""); err != nil || p != nil {
		t.Errorf(`parseProviders("") = %v, %v`, p, err)
	}
	if p, err := parseProviders("Codex"); err != nil || len(p) != 1 || p[0] != usage.ProviderCodex {
		t.Errorf(`parseProviders("Codex") = %v, %v`, p, err)
	}
	if p, err := parseProviders("claude,codex"); err != nil || len(p) != 2 {
		t.Errorf(`parseProviders("claude,codex") = %v, %v`, p, err)
	}
	if _, err := parseProviders("bogus"); err == nil {
		t.Error("expected error for unknown provider")
	}
}

// With both providers configured, a single watch iteration renders both blocks.
func TestRunWatchBothProviders(t *testing.T) {
	d, _, claudeFet, out, _ := newTestDeps()
	cf := withCodex(&d)
	if err := runWatch(context.Background(), d, opts(render.ModeCompact, true)); err != nil {
		t.Fatal(err)
	}
	if claudeFet.calls != 1 || cf.calls != 1 {
		t.Errorf("fetch calls: claude=%d codex=%d, want 1 each", claudeFet.calls, cf.calls)
	}
	if !strings.Contains(out.String(), "Claude usage  Max 20x") {
		t.Errorf("missing Claude block:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Codex usage  Team") {
		t.Errorf("missing Codex block:\n%s", out.String())
	}
}

// --provider codex shows only the Codex block even when Claude creds resolve.
func TestRunWatchProviderSelectionCodexOnly(t *testing.T) {
	d, _, claudeFet, out, _ := newTestDeps()
	cf := withCodex(&d)
	o := opts(render.ModeCompact, true)
	o.Providers = []usage.Provider{usage.ProviderCodex}
	if err := runWatch(context.Background(), d, o); err != nil {
		t.Fatal(err)
	}
	if claudeFet.calls != 0 {
		t.Errorf("Claude should not be fetched, got %d calls", claudeFet.calls)
	}
	if cf.calls != 1 {
		t.Errorf("Codex fetch calls = %d, want 1", cf.calls)
	}
	if strings.Contains(out.String(), "Claude usage") {
		t.Errorf("Claude block should be absent:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Codex usage") {
		t.Errorf("missing Codex block:\n%s", out.String())
	}
}

// When one provider has no credentials but the other succeeds, the missing one
// is skipped silently rather than printing a "no token" message.
func TestRunWatchMissingProviderSkippedWhenOtherSucceeds(t *testing.T) {
	d, claudeRes, _, out, _ := newTestDeps()
	_ = withCodex(&d)
	claudeRes.err = auth.ErrNotFound
	if err := runWatch(context.Background(), d, opts(render.ModeCompact, true)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "No Claude Code OAuth token found.") {
		t.Errorf("missing-provider message should be suppressed when another renders:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Codex usage") {
		t.Errorf("Codex block should render:\n%s", out.String())
	}
}

// A fetch error for one provider is surfaced but does not suppress the other.
func TestRunWatchOneProviderErrorsOtherRenders(t *testing.T) {
	d, _, claudeFet, out, _ := newTestDeps()
	cf := withCodex(&d)
	cf.err = errAny("codex network")
	_ = claudeFet
	if err := runWatch(context.Background(), d, opts(render.ModeCompact, true)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Claude usage") {
		t.Errorf("Claude block should render:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Codex: Error: codex network") {
		t.Errorf("expected labelled Codex error:\n%s", out.String())
	}
}

// The TUI fetch closure aggregates snapshots from both providers.
func TestRunTUIBothProviders(t *testing.T) {
	d, _, _, _, _ := newTestDeps()
	_ = withCodex(&d)
	var captured tui.Config
	d.RunTUI = func(_ context.Context, cfg tui.Config) error { captured = cfg; return nil }
	if err := runTUI(context.Background(), d, runOptions{}); err != nil {
		t.Fatal(err)
	}
	r := captured.Fetch()
	if r.Err != nil || len(r.Snapshots) != 2 {
		t.Fatalf("fetch = %+v", r)
	}
	if r.Snapshots[0].Provider != usage.ProviderClaude || r.Snapshots[1].Provider != usage.ProviderCodex {
		t.Errorf("provider order = %v, %v", r.Snapshots[0].Provider, r.Snapshots[1].Provider)
	}
}

type errAny string

func (e errAny) Error() string { return string(e) }

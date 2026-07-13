package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/merlindeep/claude-cost-viewer/internal/auth"
	"github.com/merlindeep/claude-cost-viewer/internal/client"
	"github.com/merlindeep/claude-cost-viewer/internal/render"
	"github.com/merlindeep/claude-cost-viewer/internal/tui"
	"github.com/merlindeep/claude-cost-viewer/internal/usage"
)

// maxBackoff caps the exponential backoff applied after HTTP 429 responses.
const maxBackoff = 15 * time.Minute

// minRecommendedInterval is the smallest refresh interval that is unlikely to
// trip the endpoint's aggressive rate limiting. Faster intervals are allowed
// but warned about.
const minRecommendedInterval = 60 * time.Second

// Minimal ANSI codes for status/error lines (the render package owns the rest).
const (
	cReset  = "\033[0m"
	cDim    = "\033[2m"
	cRed    = "\033[31m"
	cYellow = "\033[33m"
)

func colorize(enabled bool, code, s string) string {
	if !enabled || code == "" {
		return s
	}
	return code + s + cReset
}

// fetcher fetches a Claude usage snapshot for a token.
type fetcher interface {
	Fetch(ctx context.Context, token string) (*usage.Usage, []byte, error)
}

// codexFetcher fetches a Codex usage snapshot for a token and account id.
type codexFetcher interface {
	Fetch(ctx context.Context, token, accountID string) (*usage.Codex, []byte, error)
}

// credResolver resolves OAuth credentials.
type credResolver interface {
	Resolve() (auth.Credentials, error)
}

// deps holds every external dependency, injected so the run logic is testable
// without a network, terminal, or real credentials. The Claude and Codex
// providers each get their own resolver, fetcher factory, and reload command; a
// nil resolver means that provider is not configured and is skipped entirely.
type deps struct {
	Resolver        credResolver
	NewFetcher      func(version string) fetcher
	Reload          func(ctx context.Context) error
	CodexResolver   credResolver
	NewCodexFetcher func() codexFetcher
	CodexReload     func(ctx context.Context) error
	Version         func() string
	Now             func() time.Time
	Sleep           func(ctx context.Context, d time.Duration) bool
	RunTUI          func(ctx context.Context, cfg tui.Config) error
	ClearScreen     bool
	Out             io.Writer
	Err             io.Writer
	MockFile        func() string
	MockPlan        func() string
	ReadFile        func(string) ([]byte, error)
}

// runOptions are the resolved, validated options for a single invocation.
type runOptions struct {
	Interval        time.Duration
	Once            bool
	Mode            render.Mode
	Color           bool
	ShowZeroModels  bool
	AutoReloadToken bool
	// Providers restricts which providers are shown. Empty means "all".
	Providers []usage.Provider
}

func (o runOptions) renderOptions(now time.Time) render.Options {
	return render.Options{
		Color: o.Color,
		Now:   now,
	}
}

func (o runOptions) meterOptions() usage.MeterOptions {
	return usage.MeterOptions{IncludeZeroModels: o.ShowZeroModels}
}

// wants reports whether provider p is selected. An empty selection means all.
func (o runOptions) wants(p usage.Provider) bool {
	if len(o.Providers) == 0 {
		return true
	}
	for _, sel := range o.Providers {
		if sel == p {
			return true
		}
	}
	return false
}

// isHumanDashboard reports whether the mode is a full-screen, human-oriented
// view (which clears the screen and prints a footer) rather than machine output.
func (o runOptions) isHumanDashboard() bool {
	return o.Mode == render.ModeCompact || o.Mode == render.ModeTable
}

// providerRuntime bundles everything needed to fetch and render one provider,
// plus the per-provider state (reload cooldown gate, backoff) that must persist
// across watch-loop iterations.
type providerRuntime struct {
	name        usage.Provider
	resolver    credResolver
	reload      func(ctx context.Context) error
	reloadNote  string
	noToken     []string
	expiredHint string
	url         string
	fetch       func(ctx context.Context, creds auth.Credentials, opt usage.MeterOptions) (usage.Snapshot, []byte, error)
	gate        reloadGate
	backoff     time.Duration
}

// reauthMessage is the friendly, actionable error the TUI shows when a token has
// expired and could not be refreshed.
func (pr *providerRuntime) reauthMessage() error {
	return fmt.Errorf("%s token expired and re-authentication failed — %s, then press r to try again",
		pr.name.Short(), strings.TrimSuffix(pr.expiredHint, ", then try again."))
}

// buildProviders constructs a runtime for each provider that is both selected
// (via --provider) and configured (its resolver dependency is present). The
// runtimes are built once and reused across watch-loop iterations so their
// reload cooldown and backoff state persist.
func buildProviders(d deps, o runOptions) []*providerRuntime {
	var out []*providerRuntime

	if o.wants(usage.ProviderClaude) && d.Resolver != nil {
		f := d.NewFetcher(d.Version())
		out = append(out, &providerRuntime{
			name:       usage.ProviderClaude,
			resolver:   d.Resolver,
			reload:     d.Reload,
			reloadNote: "Token expired — running Claude Code once to reload it.",
			noToken: []string{
				"No Claude Code OAuth token found.",
				"Run Claude Code at least once, or export CLAUDE_CODE_OAUTH_TOKEN.",
			},
			expiredHint: "Run any Claude Code command to refresh the token, then try again.",
			url:         client.DefaultBaseURL,
			fetch: func(ctx context.Context, creds auth.Credentials, opt usage.MeterOptions) (usage.Snapshot, []byte, error) {
				u, raw, err := f.Fetch(ctx, creds.AccessToken)
				if err != nil {
					return usage.Snapshot{}, raw, err
				}
				return usage.ClaudeSnapshot(u, usage.ClassifyPlan(creds.Plan).Label(), opt), raw, nil
			},
		})
	}

	if o.wants(usage.ProviderCodex) && d.CodexResolver != nil {
		cf := d.NewCodexFetcher()
		out = append(out, &providerRuntime{
			name:       usage.ProviderCodex,
			resolver:   d.CodexResolver,
			reload:     d.CodexReload,
			reloadNote: "Token expired — running Codex once to reload it.",
			noToken: []string{
				"No Codex OAuth token found.",
				"Run 'codex login' at least once, or export CODEX_ACCESS_TOKEN.",
			},
			expiredHint: "Run 'codex login' to refresh the token, then try again.",
			url:         client.CodexBaseURL,
			fetch: func(ctx context.Context, creds auth.Credentials, opt usage.MeterOptions) (usage.Snapshot, []byte, error) {
				c, raw, err := cf.Fetch(ctx, creds.AccessToken, creds.AccountID)
				if err != nil {
					return usage.Snapshot{}, raw, err
				}
				return c.Snapshot(opt), raw, nil
			},
		})
	}

	return out
}

// isUnauthorized reports whether err is an HTTP 401 from the usage endpoint,
// i.e. the OAuth token was rejected because it has expired or been revoked.
func isUnauthorized(err error) bool {
	var apiErr *client.APIError
	return errors.As(err, &apiErr) && apiErr.Status == 401
}

// isRateLimited reports whether err is an HTTP 429 from the usage endpoint. The
// endpoint returns 429 both for genuine rate limiting and to reject a token it
// dislikes — notably an expired token or a foreign User-Agent.
func isRateLimited(err error) bool {
	var apiErr *client.APIError
	return errors.As(err, &apiErr) && apiErr.Status == 429
}

// credsExpired reports whether creds carry a known expiry that is at or before
// now. Credentials without expiry information are treated as not expired.
func credsExpired(creds auth.Credentials, now time.Time) bool {
	exp, ok := creds.ExpiresAtTime()
	return ok && !exp.After(now)
}

// shouldReauth reports whether err warrants re-resolving credentials and
// retrying the request once. The endpoint rejects a stale token in one of two
// ways: an explicit HTTP 401, or — for an already-expired token — an HTTP 429
// that is otherwise indistinguishable from real rate limiting. In both cases the
// provider's CLI may have refreshed the stored token in the background, so a
// single re-resolve is worth attempting.
func shouldReauth(err error, creds auth.Credentials, now time.Time) bool {
	return isUnauthorized(err) || (isRateLimited(err) && credsExpired(creds, now))
}

// fetchWithRetry fetches a snapshot for creds. If the endpoint rejects the token
// — with HTTP 401, or with HTTP 429 while the token has already expired — it
// re-resolves credentials through the provider's standard chain (in case its CLI
// refreshed the token in the background) and retries once with the fresh token.
// The retry is skipped when re-resolution fails or yields the same token.
//
// It returns the credentials actually used, so a successful refresh is reflected
// upstream (for example in the plan label).
func fetchWithRetry(ctx context.Context, pr *providerRuntime, opt usage.MeterOptions, creds auth.Credentials, now time.Time) (usage.Snapshot, auth.Credentials, error) {
	snap, _, err := pr.fetch(ctx, creds, opt)
	if !shouldReauth(err, creds, now) {
		return snap, creds, err
	}
	fresh, rerr := pr.resolver.Resolve()
	if rerr != nil || fresh.AccessToken == creds.AccessToken {
		return snap, creds, err
	}
	snap, _, err = pr.fetch(ctx, fresh, opt)
	return snap, fresh, err
}

// reloadGate serializes auto-reload attempts and enforces the cooldown between
// them. It is safe for concurrent use: the TUI fetches from multiple goroutines.
type reloadGate struct {
	mu   sync.Mutex
	last time.Time
}

// due reports whether a reload may be attempted at now, recording the attempt
// time when it returns true. It returns false inside the cooldown window.
func (g *reloadGate) due(now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if now.Sub(g.last) < reloadCooldown {
		return false
	}
	g.last = now
	return true
}

// maybeReloadToken asks the provider's CLI to reload an expired token, at most
// once per reloadCooldown, when --auto-reload-expired-token is set. It returns
// the possibly-refreshed credentials. notify, when non-nil, is called just
// before a reload attempt (the dashboard prints a status line; the TUI passes
// nil because it owns the screen). ccview never reads or writes the token itself
// — it only spawns the helper and re-resolves through the standard chain.
func maybeReloadToken(ctx context.Context, pr *providerRuntime, o runOptions, creds auth.Credentials, now time.Time, notify func()) auth.Credentials {
	if !o.AutoReloadToken || pr.reload == nil || !credsExpired(creds, now) || !pr.gate.due(now) {
		return creds
	}
	if notify != nil {
		notify()
	}
	_ = pr.reload(ctx)
	if fresh, err := pr.resolver.Resolve(); err == nil {
		creds = fresh
	}
	return creds
}

// runWatch runs the polling loop. With Once set it performs a single iteration.
func runWatch(ctx context.Context, d deps, o runOptions) error {
	providers := buildProviders(d, o)
	human := o.isHumanDashboard()

	for {
		if ctx.Err() != nil {
			return nil
		}
		if human && d.ClearScreen && !o.Once {
			_, _ = io.WriteString(d.Out, "\033[H\033[2J")
		}
		now := d.Now()

		// statusW receives human status/error lines. For machine modes it is
		// stderr so it never pollutes stdout.
		statusW := d.Out
		if !human {
			statusW = d.Err
		}

		// Mock mode: render a canned Claude payload from a file (used for demos
		// and tests).
		if mf := d.MockFile(); mf != "" {
			if b, err := d.ReadFile(mf); err == nil {
				if u, perr := usage.Parse(b); perr == nil {
					snap := usage.ClaudeSnapshot(u, usage.ClassifyPlan(d.MockPlan()).Label(), o.meterOptions())
					_ = render.Render(d.Out, []usage.Snapshot{snap}, o.Mode, o.renderOptions(now))
				}
			}
			if human {
				writeFooter(d.Out, o, now, "[mock]")
			}
			if o.Once {
				return nil
			}
			if !d.Sleep(ctx, o.Interval) {
				return nil
			}
			continue
		}

		snaps, notFound := collectSnapshots(ctx, o, providers, statusW, now)

		if len(snaps) > 0 {
			if err := render.Render(d.Out, snaps, o.Mode, o.renderOptions(now)); err != nil {
				return err
			}
		} else {
			for _, pr := range notFound {
				printNoToken(statusW, o.Color, pr)
			}
		}

		if human {
			writeFooter(d.Out, o, now, "")
		}
		if o.Once {
			return nil
		}
		if !d.Sleep(ctx, sleepDuration(o, providers)) {
			return nil
		}
	}
}

// collectSnapshots resolves, (optionally) reloads, and fetches each provider in
// turn. Successful snapshots are returned in provider order; providers whose
// credentials were not found are returned in notFound so the caller can decide
// whether to surface a "no token" message (only when nothing rendered). Fetch
// errors for providers that *did* have a token are printed immediately, since
// they represent real problems worth showing even when another provider worked.
func collectSnapshots(ctx context.Context, o runOptions, providers []*providerRuntime, statusW io.Writer, now time.Time) ([]usage.Snapshot, []*providerRuntime) {
	var snaps []usage.Snapshot
	var notFound []*providerRuntime
	multi := len(providers) > 1

	for _, pr := range providers {
		creds, err := pr.resolver.Resolve()
		if err != nil {
			notFound = append(notFound, pr)
			continue
		}

		creds = maybeReloadToken(ctx, pr, o, creds, now, func() {
			fmt.Fprintln(statusW, colorize(o.Color, cDim, pr.reloadNote))
		})

		snap, creds, ferr := fetchWithRetry(ctx, pr, o.meterOptions(), creds, now)
		if ferr != nil {
			printFetchError(statusW, o, pr, ferr, creds, now, multi)
			continue
		}
		pr.backoff = 0
		snaps = append(snaps, snap)
	}
	return snaps, notFound
}

// providerLabel returns the "Provider: " prefix for status lines when more than
// one provider is active, and an empty string otherwise (a single provider
// keeps the original, unprefixed output).
func providerLabel(pr *providerRuntime, multi bool) string {
	if !multi {
		return ""
	}
	return pr.name.Short() + ": "
}

// printNoToken prints a provider's "no credentials found" message.
func printNoToken(w io.Writer, color bool, pr *providerRuntime) {
	for i, line := range pr.noToken {
		code := cRed
		if i > 0 {
			code = cDim
		}
		fmt.Fprintln(w, colorize(color, code, line))
	}
}

// printFetchError renders the appropriate status line for a fetch failure and,
// for genuine rate limiting, advances the provider's backoff.
func printFetchError(statusW io.Writer, o runOptions, pr *providerRuntime, ferr error, creds auth.Credentials, now time.Time, multi bool) {
	label := providerLabel(pr, multi)
	switch {
	case isUnauthorized(ferr):
		printExpiredTokenNotice(statusW, o.Color, 401, pr.expiredHint, label)
	case isRateLimited(ferr) && credsExpired(creds, now):
		// An expired token is rejected with 429, not 401. Surface it as an auth
		// problem instead of backing off as if rate limited.
		printExpiredTokenNotice(statusW, o.Color, 429, pr.expiredHint, label)
	case isRateLimited(ferr):
		pr.backoff = client.NextBackoff(pr.backoff, o.Interval, maxBackoff)
		fmt.Fprintln(statusW, colorize(o.Color, cYellow,
			fmt.Sprintf("%sRate limited (HTTP 429) — backing off to %s.", label, pr.backoff)))
	default:
		fmt.Fprintln(statusW, colorize(o.Color, cRed, label+"Error: "+ferr.Error()))
	}
}

// sleepDuration returns the interval to wait before the next iteration: the base
// interval, unless a provider is backing off past it after a 429.
func sleepDuration(o runOptions, providers []*providerRuntime) time.Duration {
	d := o.Interval
	for _, pr := range providers {
		if pr.backoff > d {
			d = pr.backoff
		}
	}
	return d
}

// printExpiredTokenNotice explains that the stored token has expired and could
// not be refreshed, plus how to fix it. status is the HTTP status the endpoint
// used to reject the token: 401, or 429 for a token that was already expired
// (this endpoint rejects expired tokens with 429 rather than 401). label is an
// optional "Provider: " prefix, and hint is the provider-specific fix.
func printExpiredTokenNotice(w io.Writer, color bool, status int, hint, label string) {
	fmt.Fprintln(w, colorize(color, cRed,
		fmt.Sprintf("%sToken expired and re-authentication failed (HTTP %d).", label, status)))
	fmt.Fprintln(w, colorize(color, cDim, hint))
}

// writeFooter prints the status footer for the human dashboard modes. It is a
// no-op for one-shot (--once) runs, keeping single snapshots clean for piping.
func writeFooter(w io.Writer, o runOptions, now time.Time, note string) {
	if o.Once {
		return
	}
	s := fmt.Sprintf("updated %s · every %s · Ctrl+C to quit", now.Format("15:04:05"), o.Interval)
	if note != "" {
		s += " " + note
	}
	fmt.Fprintln(w, "\n"+colorize(o.Color, cDim, s))
}

// runDebug prints diagnostics for each configured provider: the credential
// source, a masked token, expiry, plan, the HTTP status, and a snippet of the
// raw response, followed by a compact render on success.
func runDebug(ctx context.Context, d deps, o runOptions) error {
	out := d.Out
	ua := d.Version()
	providers := buildProviders(d, o)
	multi := len(providers) > 1

	fmt.Fprintln(out, "ccview --debug")
	fmt.Fprintf(out, "User-Agent: claude-code/%s\n", ua)

	for _, pr := range providers {
		if multi {
			fmt.Fprintf(out, "\n=== %s ===\n", pr.name.Short())
		}

		// When the resolver supports it, print a per-source breakdown so a failed
		// lookup is self-diagnosable (which sources were tried and what each held).
		if dg, ok := pr.resolver.(interface {
			Diagnose() []auth.SourceDiagnostic
		}); ok {
			fmt.Fprintln(out, "\ncredential sources (priority order):")
			for i, s := range dg.Diagnose() {
				fmt.Fprintf(out, "  %d. %s — %s\n", i+1, s.Name, s.Detail)
			}
			fmt.Fprintln(out)
		}

		creds, err := pr.resolver.Resolve()
		if err != nil {
			fmt.Fprintf(out, "creds: NOT FOUND — %v\n", err)
			continue
		}
		fmt.Fprintf(out, "creds source: %s\n", creds.Source)
		fmt.Fprintf(out, "token: %s\n", creds.MaskedToken())
		if exp, ok := creds.ExpiresAtTime(); ok {
			fmt.Fprintf(out, "expiresAt: %s (%s)\n", exp.Local().Format("15:04:05"), humanLeft(exp.Sub(d.Now())))
		}
		if creds.Plan != "" {
			fmt.Fprintf(out, "plan: %s (%s)\n", creds.Plan, usage.ClassifyPlan(creds.Plan).Label())
		}

		snap, raw, ferr := pr.fetch(ctx, creds, o.meterOptions())
		fmt.Fprintf(out, "\nGET %s\n", pr.url)
		if ferr != nil {
			fmt.Fprintf(out, "error: %v\n", ferr)
			fmt.Fprintf(out, "raw: %s\n", client.Snippet(raw))
			continue
		}
		fmt.Fprintln(out, "status: OK")
		fmt.Fprintf(out, "raw: %s\n\n", client.Snippet(raw))
		if err := render.Render(out, []usage.Snapshot{snap}, render.ModeCompact, o.renderOptions(d.Now())); err != nil {
			return err
		}
	}
	return nil
}

func humanLeft(d time.Duration) string {
	if d <= 0 {
		return "expired"
	}
	return fmt.Sprintf("%dm left", int(d.Minutes()))
}

// runTUI launches the interactive dashboard. The fetch closure reuses the same
// credential/mock resolution as the watch loop, collecting a snapshot per
// provider.
func runTUI(ctx context.Context, d deps, o runOptions) error {
	providers := buildProviders(d, o)
	fetch := func() tui.Result {
		now := d.Now()
		if mf := d.MockFile(); mf != "" {
			b, err := d.ReadFile(mf)
			if err != nil {
				return tui.Result{Err: err}
			}
			u, perr := usage.Parse(b)
			if perr != nil {
				return tui.Result{Err: perr}
			}
			snap := usage.ClaudeSnapshot(u, usage.ClassifyPlan(d.MockPlan()).Label(), o.meterOptions())
			return tui.Result{Snapshots: []usage.Snapshot{snap}}
		}

		var snaps []usage.Snapshot
		var firstErr error
		for _, pr := range providers {
			creds, err := pr.resolver.Resolve()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			creds = maybeReloadToken(ctx, pr, o, creds, now, nil)
			snap, creds, ferr := fetchWithRetry(ctx, pr, o.meterOptions(), creds, now)
			if ferr != nil {
				if shouldReauth(ferr, creds, now) {
					ferr = pr.reauthMessage()
				}
				if firstErr == nil {
					firstErr = ferr
				}
				continue
			}
			snaps = append(snaps, snap)
		}
		if len(snaps) == 0 && firstErr != nil {
			return tui.Result{Err: firstErr}
		}
		return tui.Result{Snapshots: snaps}
	}
	return d.RunTUI(ctx, tui.Config{Fetch: fetch, Interval: o.Interval})
}

// warnIfFast prints a warning when the interval is below the recommended floor.
func warnIfFast(w io.Writer, interval time.Duration, color bool) {
	if interval < minRecommendedInterval {
		fmt.Fprintln(w, colorize(color, cYellow, fmt.Sprintf(
			"warning: interval %s is below the recommended %s; the usage endpoint rate-limits aggressively and may return HTTP 429.",
			interval, minRecommendedInterval)))
	}
}

// sleepCtx sleeps for d or until ctx is cancelled, returning true if the full
// duration elapsed and false if interrupted.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Auto-reload of an expired token (opt-in via --auto-reload-expired-token). The
// watch loop asks the provider's CLI to refresh the stored token instead of
// merely reporting the expiry, at most once per reloadCooldown.
const (
	// reloadCooldown is the minimum delay between auto-refresh attempts.
	reloadCooldown = 5 * time.Minute
	// reloadTimeout bounds a single refresh command invocation.
	reloadTimeout = 30 * time.Second
	// defaultReloadCmd runs when CCVIEW_RELOAD_CMD is unset: a minimal one-shot
	// Claude Code call on the cheapest model, which refreshes the OAuth token as
	// part of its auth bootstrap. Its output is irrelevant and is discarded.
	defaultReloadCmd = "claude -p --model haiku hi"
	// defaultCodexReloadCmd runs when CCVIEW_CODEX_RELOAD_CMD is unset: a quota-
	// free Codex login-status check, which refreshes the OAuth token from the
	// stored refresh token when it has expired.
	defaultCodexReloadCmd = "codex login status"
)

// reloadCmdline returns the shell command used to refresh the Claude token: the
// CCVIEW_RELOAD_CMD override when set, otherwise [defaultReloadCmd].
func reloadCmdline(getenv func(string) string) string {
	if c := strings.TrimSpace(getenv("CCVIEW_RELOAD_CMD")); c != "" {
		return c
	}
	return defaultReloadCmd
}

// codexReloadCmdline returns the shell command used to refresh the Codex token:
// the CCVIEW_CODEX_RELOAD_CMD override when set, otherwise
// [defaultCodexReloadCmd].
func codexReloadCmdline(getenv func(string) string) string {
	if c := strings.TrimSpace(getenv("CCVIEW_CODEX_RELOAD_CMD")); c != "" {
		return c
	}
	return defaultCodexReloadCmd
}

// runReload executes a refresh command non-interactively with a timeout. The
// command's stdin/stdout/stderr are left nil (the null device), so it cannot
// block on a prompt and cannot pollute ccview's display. Only the side effect —
// the provider's CLI rewriting the stored token — matters; the token never
// passes through ccview.
func runReload(ctx context.Context, cmdline string) error {
	ctx, cancel := context.WithTimeout(ctx, reloadTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "sh", "-c", cmdline).Run()
}

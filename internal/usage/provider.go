package usage

import (
	"encoding/json"
	"fmt"
	"time"
)

// Provider identifies which service a snapshot came from. ccview can show more
// than one at once (Claude and Codex), so every snapshot is tagged.
type Provider string

const (
	// ProviderClaude is Anthropic's Claude (the OAuth usage endpoint).
	ProviderClaude Provider = "claude"
	// ProviderCodex is OpenAI's Codex (the ChatGPT backend usage endpoint).
	ProviderCodex Provider = "codex"
)

// Short returns the terse provider name used in one-line output.
func (p Provider) Short() string {
	switch p {
	case ProviderClaude:
		return "Claude"
	case ProviderCodex:
		return "Codex"
	default:
		return string(p)
	}
}

// Title returns the human-readable header shown above a provider's meters.
func (p Provider) Title() string {
	switch p {
	case ProviderClaude:
		return "Claude usage"
	case ProviderCodex:
		return "Codex usage"
	default:
		return string(p) + " usage"
	}
}

// Snapshot is a provider-neutral, render-ready view of one provider's usage. The
// renderers consume a slice of these, so adding a provider never touches display
// code: a provider only has to produce a [Snapshot].
type Snapshot struct {
	// Provider identifies the source (Claude or Codex).
	Provider Provider
	// Plan is the human-readable plan label (e.g. "Max 20x", "Team"), or empty.
	Plan string
	// Meters is the ordered list of usage windows to display.
	Meters []Meter
}

// Title is the header for the snapshot's block.
func (s Snapshot) Title() string { return s.Provider.Title() }

// ClaudeSnapshot builds a snapshot from a decoded Claude usage payload.
func ClaudeSnapshot(u *Usage, plan string, opt MeterOptions) Snapshot {
	return Snapshot{
		Provider: ProviderClaude,
		Plan:     plan,
		Meters:   u.Meters(opt),
	}
}

// codexWindow is a single rate-limit window as returned by the Codex usage
// endpoint. Percentages are in [0, 100]; timestamps are Unix seconds.
type codexWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
	ResetAfterSeconds  int64   `json:"reset_after_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

// Codex is the decoded payload of GET
// https://chatgpt.com/backend-api/wham/usage. Only the fields ccview renders are
// modelled; the endpoint returns more (credits, spend controls) that may be
// surfaced later.
type Codex struct {
	PlanType  string `json:"plan_type"`
	RateLimit struct {
		PrimaryWindow   *codexWindow `json:"primary_window"`
		SecondaryWindow *codexWindow `json:"secondary_window"`
	} `json:"rate_limit"`
}

// ParseCodex decodes a Codex usage payload from raw JSON.
func ParseCodex(b []byte) (*Codex, error) {
	var c Codex
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("decode codex usage payload: %w", err)
	}
	return &c, nil
}

// codexSessionCutoff is the window length at or below which a Codex window is
// treated as the rolling session (rather than the weekly) window. Codex reports
// a ~5-hour primary window on individual plans; team/enterprise plans report
// only the 7-day window. Classifying by length rather than position makes the
// mapping robust to which slot each window lands in.
const codexSessionCutoff = 6 * 60 * 60

// window2meter converts a Codex window to a normalized meter. It returns false
// when the window is absent.
func (w *codexWindow) meter() (Meter, bool) {
	if w == nil {
		return Meter{}, false
	}
	m := Meter{Percent: w.UsedPercent}
	if w.LimitWindowSeconds > 0 && w.LimitWindowSeconds <= codexSessionCutoff {
		m.Key, m.Label, m.Kind = "session", "Session", KindSession
	} else {
		m.Key, m.Label, m.Kind = "weekly", "Weekly", KindWeekly
	}
	if w.ResetAt > 0 {
		m.ResetsAt = time.Unix(w.ResetAt, 0)
		m.HasReset = true
	}
	return m, true
}

// Snapshot converts the Codex payload into a provider-neutral snapshot. The
// MeterOptions argument is accepted for symmetry with the Claude path; Codex has
// no per-model windows, so it is currently unused.
func (c *Codex) Snapshot(_ MeterOptions) Snapshot {
	var meters []Meter
	if m, ok := c.RateLimit.PrimaryWindow.meter(); ok {
		meters = append(meters, m)
	}
	if m, ok := c.RateLimit.SecondaryWindow.meter(); ok {
		meters = append(meters, m)
	}
	return Snapshot{
		Provider: ProviderCodex,
		Plan:     ClassifyPlan(c.PlanType).Label(),
		Meters:   meters,
	}
}

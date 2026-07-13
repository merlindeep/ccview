package usage

import (
	"testing"
	"time"
)

func TestProviderTitleShort(t *testing.T) {
	cases := []struct {
		p            Provider
		title, short string
	}{
		{ProviderClaude, "Claude usage", "Claude"},
		{ProviderCodex, "Codex usage", "Codex"},
		{Provider("grok"), "grok usage", "grok"},
	}
	for _, c := range cases {
		if got := c.p.Title(); got != c.title {
			t.Errorf("%s.Title() = %q, want %q", c.p, got, c.title)
		}
		if got := c.p.Short(); got != c.short {
			t.Errorf("%s.Short() = %q, want %q", c.p, got, c.short)
		}
	}
}

func TestClaudeSnapshot(t *testing.T) {
	u := &Usage{FiveHour: &Window{Utilization: f(9), ResetsAt: "2026-07-13T21:00:00Z"}}
	s := ClaudeSnapshot(u, "Team", MeterOptions{})
	if s.Provider != ProviderClaude || s.Plan != "Team" {
		t.Errorf("snapshot = %+v", s)
	}
	if len(s.Meters) != 1 || s.Meters[0].Kind != KindSession {
		t.Errorf("meters = %+v", s.Meters)
	}
	if s.Title() != "Claude usage" {
		t.Errorf("title = %q", s.Title())
	}
}

func TestParseCodexInvalid(t *testing.T) {
	if _, err := ParseCodex([]byte("not json")); err == nil {
		t.Error("expected parse error")
	}
}

func TestCodexSnapshotWeeklyOnly(t *testing.T) {
	// Team plan: a single 7-day primary window, no secondary.
	body := []byte(`{
		"plan_type": "team",
		"rate_limit": {
			"primary_window": {"used_percent": 0, "limit_window_seconds": 604800, "reset_at": 1784533989},
			"secondary_window": null
		}
	}`)
	c, err := ParseCodex(body)
	if err != nil {
		t.Fatal(err)
	}
	s := c.Snapshot(MeterOptions{})
	if s.Provider != ProviderCodex || s.Plan != "Team" {
		t.Errorf("snapshot = %+v", s)
	}
	if len(s.Meters) != 1 {
		t.Fatalf("meters = %+v", s.Meters)
	}
	m := s.Meters[0]
	if m.Kind != KindWeekly || m.Label != "Weekly" || m.Percent != 0 {
		t.Errorf("weekly meter = %+v", m)
	}
	if !m.HasReset || !m.ResetsAt.Equal(time.Unix(1784533989, 0)) {
		t.Errorf("reset = %v (has=%v)", m.ResetsAt, m.HasReset)
	}
}

func TestCodexSnapshotSessionAndWeekly(t *testing.T) {
	// Individual plan: a rolling 5-hour primary and a weekly secondary. The
	// windows are classified by length, not by slot.
	body := []byte(`{
		"plan_type": "plus",
		"rate_limit": {
			"primary_window": {"used_percent": 42, "limit_window_seconds": 18000, "reset_at": 100},
			"secondary_window": {"used_percent": 7, "limit_window_seconds": 604800, "reset_at": 200}
		}
	}`)
	c, err := ParseCodex(body)
	if err != nil {
		t.Fatal(err)
	}
	s := c.Snapshot(MeterOptions{})
	if s.Plan != "Plus" {
		t.Errorf("plan = %q, want Plus", s.Plan)
	}
	if len(s.Meters) != 2 {
		t.Fatalf("meters = %+v", s.Meters)
	}
	if s.Meters[0].Kind != KindSession || s.Meters[0].Percent != 42 {
		t.Errorf("session meter = %+v", s.Meters[0])
	}
	if s.Meters[1].Kind != KindWeekly || s.Meters[1].Percent != 7 {
		t.Errorf("weekly meter = %+v", s.Meters[1])
	}
}

func TestCodexSnapshotEmpty(t *testing.T) {
	c, err := ParseCodex([]byte(`{"plan_type": "free"}`))
	if err != nil {
		t.Fatal(err)
	}
	s := c.Snapshot(MeterOptions{})
	if len(s.Meters) != 0 {
		t.Errorf("expected no meters, got %+v", s.Meters)
	}
	if s.Plan != "Free" {
		t.Errorf("plan = %q", s.Plan)
	}
}

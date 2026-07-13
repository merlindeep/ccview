package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/merlindeep/claude-cost-viewer/internal/usage"
)

// twoProviderSnaps returns a Claude and a Codex snapshot for multi-block tests.
func twoProviderSnaps() []usage.Snapshot {
	claude := usage.ClaudeSnapshot(sampleUsage(), "Max 20x", usage.MeterOptions{})
	codex, _ := usage.ParseCodex([]byte(`{"plan_type":"team","rate_limit":{"primary_window":{"used_percent":0,"limit_window_seconds":604800,"reset_at":1784533989}}}`))
	return []usage.Snapshot{claude, codex.Snapshot(usage.MeterOptions{})}
}

func TestRenderCompactMultiProvider(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, twoProviderSnaps(), ModeCompact, baseOpts()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "Claude usage  Max 20x") || !strings.Contains(out, "Codex usage  Team") {
		t.Errorf("both provider titles expected:\n%s", out)
	}
	// A blank line separates the two blocks.
	if !strings.Contains(out, "\n\nCodex usage") {
		t.Errorf("blocks should be separated by a blank line:\n%q", out)
	}
}

func TestRenderOnelineMultiProvider(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, twoProviderSnaps(), ModeOneline, baseOpts()); err != nil {
		t.Fatal(err)
	}
	want := "Claude 5h:42% 7d:13% sonnet:8% opus:90% extra:16%  Codex 7d:0%\n"
	if buf.String() != want {
		t.Errorf("oneline multi =\n %q\nwant\n %q", buf.String(), want)
	}
}

func TestRenderJSONMultiProvider(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, twoProviderSnaps(), ModeJSON, baseOpts()); err != nil {
		t.Fatal(err)
	}
	var doc jsonDocument
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(doc.Providers) != 2 {
		t.Fatalf("providers = %d, want 2", len(doc.Providers))
	}
	if doc.Providers[0].Provider != "claude" || doc.Providers[1].Provider != "codex" {
		t.Errorf("provider order = %q, %q", doc.Providers[0].Provider, doc.Providers[1].Provider)
	}
}

func TestRenderTableMultiProvider(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, twoProviderSnaps(), ModeTable, baseOpts()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Count(out, "METER") != 2 {
		t.Errorf("expected a table header per provider:\n%s", out)
	}
}

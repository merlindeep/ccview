package render

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/merlindeep/claude-cost-viewer/internal/usage"
)

// renderCompact reproduces the original at-a-glance view: for each provider a
// title with the plan name, then one indented bar line per available window.
// Multiple providers are stacked with a blank line between them. This is the
// default mode and the one intended for the long-running watch loop.
func renderCompact(w io.Writer, snaps []usage.Snapshot, opt Options) error {
	var b strings.Builder
	now := opt.now()
	for i, s := range snaps {
		if i > 0 {
			b.WriteString("\n")
		}
		compactBlock(&b, s, opt, now)
	}
	return writeString(w, b.String())
}

// compactBlock writes a single provider's title and meter lines.
func compactBlock(b *strings.Builder, s usage.Snapshot, opt Options, now time.Time) {
	title := wrap(opt.Color, ansiBold+ansiCyan, s.Title())
	if s.Plan != "" {
		title += "  " + wrap(opt.Color, ansiDim, s.Plan)
	}
	b.WriteString(title + "\n")

	if len(s.Meters) == 0 {
		b.WriteString("  " + wrap(opt.Color, ansiDim, "(no usage windows reported)") + "\n")
		return
	}
	for _, m := range s.Meters {
		b.WriteString(compactLine(m, opt, now) + "\n")
	}
}

// compactLine formats one meter as:
//
//	"  Session   ████░░░░░░░░░░░░░░░░  42%   resets in 3h 44m"
//
// Per-model windows are indented and lower-cased to match the original layout.
func compactLine(m usage.Meter, opt Options, now time.Time) string {
	label := m.Label
	if m.Kind == usage.KindWeeklyModel {
		label = "  " + strings.ToLower(m.Label)
	}

	barStr := bar(m.Percent, opt.width(), opt.Color)
	pct := fmt.Sprintf("%3d%%", roundPct(m.Percent))
	pct = wrap(opt.Color, colorFor(m.Percent), pct)

	line := fmt.Sprintf("  %-9s %s %s", label, barStr, pct)

	tail := ""
	if r := resetText(m, now); r != "" {
		tail = "resets " + r
	}
	if m.Kind == usage.KindExtra && m.Detail != "" {
		if tail != "" {
			tail += "  "
		}
		tail += m.Detail
	}
	if tail != "" {
		line += "   " + wrap(opt.Color, ansiDim, tail)
	}
	return line
}

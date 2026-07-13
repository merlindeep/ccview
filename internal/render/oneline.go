package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/merlindeep/claude-cost-viewer/internal/usage"
)

// onelineLabel produces a terse label for the single-line view: "5h" for the
// session window, "7d" for the weekly window, the lower-cased model name for
// per-model windows, and "extra" for extra usage.
func onelineLabel(m usage.Meter) string {
	switch m.Kind {
	case usage.KindSession:
		return "5h"
	case usage.KindWeekly:
		return "7d"
	case usage.KindExtra:
		return "extra"
	default:
		return strings.ToLower(m.Label)
	}
}

// renderOneline writes a single compact line suitable for embedding in a status
// bar, e.g. "Claude 5h:42% 7d:13% opus:8%". With more than one provider the
// per-provider segments are joined with two spaces, e.g.
// "Claude 5h:42% 7d:13%  Codex 7d:0%". A trailing newline is included.
func renderOneline(w io.Writer, snaps []usage.Snapshot, opt Options) error {
	segments := make([]string, 0, len(snaps))
	for _, s := range snaps {
		segments = append(segments, onelineSegment(s, opt))
	}
	return writeString(w, strings.Join(segments, "  ")+"\n")
}

// onelineSegment renders one provider's portion of the single line.
func onelineSegment(s usage.Snapshot, opt Options) string {
	prefix := wrap(opt.Color, ansiCyan, s.Provider.Short())
	if len(s.Meters) == 0 {
		return prefix + ": no data"
	}
	parts := make([]string, 0, len(s.Meters))
	for _, m := range s.Meters {
		val := fmt.Sprintf("%d%%", roundPct(m.Percent))
		val = wrap(opt.Color, colorFor(m.Percent), val)
		parts = append(parts, onelineLabel(m)+":"+val)
	}
	return prefix + " " + strings.Join(parts, " ")
}

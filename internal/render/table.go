package render

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/merlindeep/ccview/internal/usage"
)

// renderTable renders an aligned table with every available column, one table
// per provider, stacked with a blank line between. Column widths are computed
// from the plain (uncoloured) cell text, then colour is applied after padding,
// so alignment holds whether or not colour is enabled.
func renderTable(w io.Writer, snaps []usage.Snapshot, opt Options) error {
	var b strings.Builder
	now := opt.now()
	for i, s := range snaps {
		if i > 0 {
			b.WriteString("\n")
		}
		tableBlock(&b, s, opt, now)
	}
	return writeString(w, b.String())
}

// tableBlock writes a single provider's title and usage table.
func tableBlock(b *strings.Builder, s usage.Snapshot, opt Options, now time.Time) {
	title := wrap(opt.Color, ansiBold, s.Title())
	if s.Plan != "" {
		title += "  " + wrap(opt.Color, ansiDim, s.Plan)
	}
	b.WriteString(title + "\n")

	if len(s.Meters) == 0 {
		b.WriteString(wrap(opt.Color, ansiDim, "(no usage windows reported)") + "\n")
		return
	}

	type row struct {
		name   string
		pctStr string
		pct    float64
		reset  string
		detail string
	}
	rows := make([]row, 0, len(s.Meters))
	hasDetail := false
	for _, m := range s.Meters {
		reset := resetText(m, now)
		if reset == "" {
			reset = "—"
		}
		if m.Detail != "" {
			hasDetail = true
		}
		rows = append(rows, row{
			name:   m.Label,
			pctStr: fmt.Sprintf("%d%%", roundPct(m.Percent)),
			pct:    m.Percent,
			reset:  reset,
			detail: m.Detail,
		})
	}

	const (
		hName   = "METER"
		hUsage  = "USAGE"
		hPct    = "%"
		hReset  = "RESETS"
		hDetail = "DETAIL"
	)
	wName, wPct, wReset, wDetail := len(hName), len(hPct), len(hReset), len(hDetail)
	wBar := max(len(hUsage), opt.width())
	for _, r := range rows {
		wName = max(wName, len(r.name))
		wPct = max(wPct, len(r.pctStr))
		wReset = max(wReset, len(r.reset))
		wDetail = max(wDetail, len(r.detail))
	}

	// Header.
	header := fmt.Sprintf("%-*s  %-*s  %*s  %-*s", wName, hName, wBar, hUsage, wPct, hPct, wReset, hReset)
	if hasDetail {
		header += "  " + fmt.Sprintf("%-*s", wDetail, hDetail)
	}
	b.WriteString(wrap(opt.Color, ansiDim, strings.TrimRight(header, " ")) + "\n")

	// Rows.
	for _, r := range rows {
		barStr := bar(r.pct, opt.width(), opt.Color)
		barCell := barStr + strings.Repeat(" ", wBar-opt.width())

		pctCell := wrap(opt.Color, colorFor(r.pct), fmt.Sprintf("%*s", wPct, r.pctStr))

		line := fmt.Sprintf("%-*s  %s  %s  %-*s", wName, r.name, barCell, pctCell, wReset, r.reset)
		if hasDetail {
			line += "  " + fmt.Sprintf("%-*s", wDetail, r.detail)
		}
		b.WriteString(strings.TrimRight(line, " ") + "\n")
	}
}

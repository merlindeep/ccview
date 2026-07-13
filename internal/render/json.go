package render

import (
	"encoding/json"
	"io"
	"time"

	"github.com/merlindeep/ccview/internal/usage"
)

// jsonMeter is the machine-readable representation of a single meter.
type jsonMeter struct {
	Key             string  `json:"key"`
	Label           string  `json:"label"`
	Kind            string  `json:"kind"`
	Percent         float64 `json:"percent"`
	ResetsAt        *string `json:"resets_at,omitempty"`
	ResetsInSeconds *int64  `json:"resets_in_seconds,omitempty"`
	Detail          string  `json:"detail,omitempty"`
}

// jsonProvider is one provider's block in the JSON output.
type jsonProvider struct {
	Provider string      `json:"provider"`
	Plan     string      `json:"plan,omitempty"`
	Meters   []jsonMeter `json:"meters"`
}

// jsonDocument is the top-level JSON output. Providers are always emitted as an
// array so consumers see a uniform shape whether one or several are present.
type jsonDocument struct {
	GeneratedAt string         `json:"generated_at"`
	Providers   []jsonProvider `json:"providers"`
}

// kindString maps a meter kind to a stable JSON token.
func kindString(k usage.Kind) string {
	switch k {
	case usage.KindSession:
		return "session"
	case usage.KindWeekly:
		return "weekly"
	case usage.KindWeeklyModel:
		return "weekly_model"
	case usage.KindExtra:
		return "extra"
	default:
		return "unknown"
	}
}

// renderJSON writes the snapshots as indented JSON. Reset timestamps are emitted
// in RFC3339 form alongside the number of seconds remaining, so consumers can
// use whichever is convenient.
func renderJSON(w io.Writer, snaps []usage.Snapshot, opt Options) error {
	now := opt.now()
	doc := jsonDocument{
		GeneratedAt: now.Format(time.RFC3339),
		Providers:   []jsonProvider{},
	}
	for _, s := range snaps {
		p := jsonProvider{
			Provider: string(s.Provider),
			Plan:     s.Plan,
			Meters:   []jsonMeter{},
		}
		for _, m := range s.Meters {
			jm := jsonMeter{
				Key:     m.Key,
				Label:   m.Label,
				Kind:    kindString(m.Kind),
				Percent: m.Percent,
				Detail:  m.Detail,
			}
			if m.HasReset {
				ts := m.ResetsAt.Format(time.RFC3339)
				jm.ResetsAt = &ts
				secs := int64(m.ResetsAt.Sub(now).Seconds())
				jm.ResetsInSeconds = &secs
			}
			p.Meters = append(p.Meters, jm)
		}
		doc.Providers = append(doc.Providers, p)
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(doc)
}

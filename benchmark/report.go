package benchmark

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"
)

// Report bundles a Runner output: the raw per-cell RunResults plus a
// per-(case, model) aggregate so a consumer can read the headline without
// re-tallying.
type Report struct {
	GeneratedAt time.Time       `json:"generated_at"`
	Cases       int             `json:"cases"`
	Models      int             `json:"models"`
	Repeats     int             `json:"repeats"`
	Aggregate   []AggregateRow  `json:"aggregate"`
	Results     []RunResult     `json:"results"`
}

// AggregateRow summarises every iteration of one (case, model) pair.
type AggregateRow struct {
	Case                string  `json:"case"`
	Model               string  `json:"model"`
	Iterations          int     `json:"iterations"`
	KindRate            float64 `json:"kind_rate"`
	PayloadRate         float64 `json:"payload_rate"`
	ValidationCleanRate float64 `json:"validation_clean_rate"`
	AvgTurns            float64 `json:"avg_turns"`
	Errors              int     `json:"errors"`
}

// BuildReport tallies results across (case, model) pairs and returns the
// finished Report ready to encode.
func BuildReport(results []RunResult, cases, models, repeats int) Report {
	type key struct{ caseName, model string }
	type acc struct {
		row     AggregateRow
		turnSum int
	}
	groups := map[key]*acc{}
	order := []key{}
	for _, r := range results {
		k := key{caseName: r.Case, model: r.Model}
		g, ok := groups[k]
		if !ok {
			g = &acc{row: AggregateRow{Case: r.Case, Model: r.Model}}
			groups[k] = g
			order = append(order, k)
		}
		g.row.Iterations++
		g.turnSum += r.Score.Turns
		if r.Score.KindMatch {
			g.row.KindRate++
		}
		if r.Score.PayloadMatch {
			g.row.PayloadRate++
		}
		if r.Score.ValidationClean {
			g.row.ValidationCleanRate++
		}
		if r.Error != "" {
			g.row.Errors++
		}
	}

	sort.Slice(order, func(i, j int) bool {
		if order[i].caseName != order[j].caseName {
			return order[i].caseName < order[j].caseName
		}
		return order[i].model < order[j].model
	})

	aggregate := make([]AggregateRow, 0, len(order))
	for _, k := range order {
		g := groups[k]
		n := float64(g.row.Iterations)
		if n > 0 {
			g.row.KindRate /= n
			g.row.PayloadRate /= n
			g.row.ValidationCleanRate /= n
			g.row.AvgTurns = float64(g.turnSum) / n
		}
		aggregate = append(aggregate, g.row)
	}

	return Report{
		GeneratedAt: time.Now().UTC(),
		Cases:       cases,
		Models:      models,
		Repeats:     repeats,
		Aggregate:   aggregate,
		Results:     results,
	}
}

// WriteJSON encodes the report as indented JSON.
func (r Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return fmt.Errorf("benchmark: encode report: %w", err)
	}
	return nil
}

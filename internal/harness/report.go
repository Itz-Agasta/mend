package harness

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
)

// Report is the outcome of one harness run. Every number in it was measured
// by that run.
type Report struct {
	Scenario   string    `json:"scenario"`
	Ops        int       `json:"ops"`
	BadAt      int       `json:"bad_at"`
	BadActor   string    `json:"bad_actor"`
	BadSQL     string    `json:"bad_sql"`
	BadEffect  Summary   `json:"bad_effect"`
	Strategies []Outcome `json:"strategies"`
}

// Outcome is one recovery strategy's result compared with the clean run.
type Outcome struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	VsClean     Summary      `json:"vs_clean"`
	Examples    []RowExample `json:"examples,omitempty"`
}

// Summary counts differences per table.
type Summary map[string]Counts

type Counts struct {
	Missing int `json:"missing"`
	Extra   int `json:"extra"`
	Wrong   int `json:"wrong"`
}

func (c Counts) Total() int { return c.Missing + c.Extra + c.Wrong }

// RowExample shows one wrong row, for humans reading the report.
type RowExample struct {
	Table   string            `json:"table"`
	PK      string            `json:"pk"`
	Columns map[string][2]any `json:"columns"` // column → [clean, this strategy]
}

const maxExamples = 3

func summarize(d Diff) Summary {
	s := Summary{}
	for _, td := range d {
		s[td.Table] = Counts{Missing: len(td.Missing), Extra: len(td.Extra), Wrong: len(td.Changed)}
	}
	return s
}

func (r *Report) add(name, desc string, clean, got Snapshot) {
	d := Compare(clean, got)
	o := Outcome{Name: name, Description: desc, VsClean: summarize(d)}
	for _, td := range d {
		for _, ch := range td.Changed {
			if len(o.Examples) == maxExamples {
				break
			}
			o.Examples = append(o.Examples, RowExample{Table: td.Table, PK: ch.PK, Columns: ch.Columns})
		}
	}
	r.Strategies = append(r.Strategies, o)
}

func (r *Report) WriteText(w io.Writer) error {
	var b strings.Builder
	fmt.Fprintf(&b, "scenario %s: %d requests, bad action before request %d by %s\n", r.Scenario, r.Ops, r.BadAt, r.BadActor)
	fmt.Fprintf(&b, "  %s\n", r.BadSQL)
	for _, t := range Tables {
		if c := r.BadEffect[t]; c.Total() > 0 {
			fmt.Fprintf(&b, "  changed %d %s rows\n", c.Total(), t)
		}
	}
	b.WriteString("\ncompared with the clean run (the bad action never happened):\n\n")

	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', tabwriter.AlignRight)
	header := "\t"
	for _, t := range Tables {
		header += fmt.Sprintf("%s missing\t%s extra\t%s wrong\t", t, t, t)
	}
	fmt.Fprintln(tw, header)
	for _, o := range r.Strategies {
		line := o.Name + "\t"
		for _, t := range Tables {
			c := o.VsClean[t]
			line += fmt.Sprintf("%d\t%d\t%d\t", c.Missing, c.Extra, c.Wrong)
		}
		fmt.Fprintln(tw, line)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	for _, o := range r.Strategies {
		if len(o.Examples) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s, e.g.:\n", o.Name)
		for _, ex := range o.Examples {
			fmt.Fprintf(&b, "  %s %s:", ex.Table, ex.PK)
			for _, col := range slices.Sorted(maps.Keys(ex.Columns)) {
				v := ex.Columns[col]
				fmt.Fprintf(&b, " %s clean=%v got=%v;", col, v[0], v[1])
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\nmend's target: every count zero, without losing the requests after the bad action.\n")
	_, err := io.WriteString(w, b.String())
	return err
}

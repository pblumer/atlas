package csvimport

import (
	"fmt"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// A CSV-import task resolved into plain values, and the pure function that runs one.
//
// This is the split ADR-0165 draws, proved here first because CSV import involves no
// credential at all: **the engine decides what to do, the worker does it.** Finding
// the task's detail in the compiled process and reading the source text up the
// task's scope chain is engine work — a worker has neither the compiled process nor
// the scope chain — so [Resolve] runs there and produces a [Job] of plain values.
// [Run] takes only that Job, so it works identically inside the engine and inside
// `atlas worker`, and the two cannot drift about defaults or validation.

// Job is a CSV-import task with everything already looked up: the text to parse and
// the layout to parse it against. It is what travels with a leased job.
type Job struct {
	Source    string   `json:"source"`
	Delimiter string   `json:"delimiter,omitempty"`
	HasHeader bool     `json:"hasHeader"`
	Columns   []string `json:"columns,omitempty"`
	// Result names the process variable the rows are written to; empty means the
	// default.
	Result string `json:"resultVariable,omitempty"`
}

// Result is what running a Job produces: the rows as JSON, how many there were, and
// the variable to write them to.
type Result struct {
	ResultVariable string
	RowsJSON       string
	RowCount       int
}

// Run parses a resolved job. It applies the defaults itself rather than leaving them
// to callers, so the in-process path and a worker cannot disagree about what an
// unset delimiter or result variable means.
//
// A missing source or an unparseable file is an error, never an empty batch: a
// silently empty result is the failure this connector exists to avoid.
func Run(j Job) (Result, error) {
	if strings.TrimSpace(j.Source) == "" {
		return Result{}, fmt.Errorf("csv-import: the source is empty")
	}
	if !j.HasHeader && len(j.Columns) == 0 {
		return Result{}, fmt.Errorf("csv-import: a file without a header row must name its columns")
	}
	hasHeader := j.HasHeader
	cfg := Config{Delimiter: j.Delimiter, HasHeader: &hasHeader}
	for _, name := range j.Columns {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		col := Column{Name: name}
		// Without a header row the listed columns *are* the file's order — that is the
		// only thing "list your columns" can mean for a headerless file, and it is what
		// the modeler asks for. The parser requires an explicit index in that case, and
		// nothing was supplying one, so a headerless CSV connector task failed at
		// runtime with "column needs an index" despite the model being exactly what the
		// compiler demands (ADR-0139).
		if !hasHeader {
			i := len(cfg.Columns)
			col.Index = &i
		}
		cfg.Columns = append(cfg.Columns, col)
	}
	rows, err := ParseRows(cfg, []byte(j.Source))
	if err != nil {
		return Result{}, fmt.Errorf("csv-import: %v", err)
	}
	rowsJSON, err := rowsToJSON(rows)
	if err != nil {
		return Result{}, err
	}
	name := strings.TrimSpace(j.Result)
	if name == "" {
		name = csvRowsVar
	}
	return Result{ResultVariable: name, RowsJSON: rowsJSON, RowCount: len(rows)}, nil
}

// Resolve turns a compiled CSV connector task into a [Job]: the authored layout from
// the detail, and the source text read up the task's scope chain. It is engine work
// by necessity — both of its inputs live only there.
func Resolve(store VarStore, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, elementInstanceKey uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("csv-import: connector task has no detail")
	}
	vars, err := scopeChainVars(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("csv-import: read variables: %w", err)
	}
	sourceName := cp.Intern(detail.CsvSource)
	if sourceName == "" {
		sourceName = csvSourceVar
	}
	text, ok := vars[sourceName]
	if !ok || text.Kind != model.VarString || text.Text == "" {
		return Job{}, fmt.Errorf("csv-import: source variable %q is missing or not a non-empty string", sourceName)
	}
	j := Job{
		Source:    text.Text,
		Delimiter: cp.Intern(detail.CsvDelimiter),
		HasHeader: detail.CsvHasHeader,
		Result:    cp.Intern(detail.CsvResult),
	}
	for _, ci := range detail.CsvColumns {
		if name := cp.Intern(ci); name != "" {
			j.Columns = append(j.Columns, name)
		}
	}
	return j, nil
}

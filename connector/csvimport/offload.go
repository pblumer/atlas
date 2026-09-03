package csvimport

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// A CSV-import task resolved into plain values, and the pure function that runs one.
//
// This is the split ADR-0168 draws, proved here first because CSV import involves no
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
	Columns   []Column `json:"columns,omitempty"`
	// Format is csv (the default and what every model authored before formats
	// existed), fixed-width, or avp. Operation is read (the default) or write.
	Format    string `json:"format,omitempty"`
	Operation string `json:"operation,omitempty"`
	// Result names the process variable the outcome is written to; empty means the
	// default. On a read that is the rows; on a write it is the rendered file.
	Result string `json:"resultVariable,omitempty"`
}

// Writing reports whether the job renders a file rather than parsing one.
func (j Job) Writing() bool { return j.Operation == OperationWrite }

// The two directions a file task runs in.
const (
	OperationRead  = "read"
	OperationWrite = "write"
)

// Result is what running a Job produces: on a read the rows as JSON, on a write the
// rendered file, and either way how many records were involved and the variable to
// put the outcome in.
type Result struct {
	ResultVariable string
	RowsJSON       string
	Text           string
	IsText         bool
	RowCount       int
}

// Variables is what the job completes with. Both the in-process handler and the
// worker go through it, so neither can decide on its own what a result looks like —
// which is the same discipline that keeps Run shared between them.
func (r Result) Variables() (map[string]any, error) {
	if r.IsText {
		return map[string]any{r.ResultVariable: r.Text, "rowCount": r.RowCount}, nil
	}
	var rows any
	if err := json.Unmarshal([]byte(r.RowsJSON), &rows); err != nil {
		return nil, fmt.Errorf("csv-import: parsed rows are not JSON: %w", err)
	}
	return map[string]any{r.ResultVariable: rows, "rowCount": r.RowCount}, nil
}

// Run parses a resolved job. It applies the defaults itself rather than leaving them
// to callers, so the in-process path and a worker cannot disagree about what an
// unset delimiter or result variable means.
//
// A missing source or an unparseable file is an error, never an empty batch: a
// silently empty result is the failure this worker exists to avoid.
func Run(j Job) (Result, error) {
	cfg, err := configOf(j)
	if err != nil {
		return Result{}, err
	}
	name := strings.TrimSpace(j.Result)
	if name == "" {
		name = csvRowsVar
	}
	if j.Writing() {
		var rows []map[string]any
		if err := json.Unmarshal([]byte(j.Source), &rows); err != nil {
			return Result{}, fmt.Errorf("csv-import: the rows to write are not a JSON array of objects: %w", err)
		}
		text, err := Render(cfg, rows)
		if err != nil {
			return Result{}, fmt.Errorf("csv-import: %v", err)
		}
		return Result{ResultVariable: name, Text: text, IsText: true, RowCount: len(rows)}, nil
	}
	if strings.TrimSpace(j.Source) == "" {
		return Result{}, fmt.Errorf("csv-import: the source is empty")
	}
	rows, err := Parse(cfg, []byte(j.Source))
	if err != nil {
		return Result{}, fmt.Errorf("csv-import: %v", err)
	}
	rowsJSON, err := rowsToJSON(rows)
	if err != nil {
		return Result{}, err
	}
	return Result{ResultVariable: name, RowsJSON: rowsJSON, RowCount: len(rows)}, nil
}

// configOf turns the job's authored layout into a parser/renderer configuration,
// applying the defaults here rather than leaving them to callers so the in-process
// path and a worker cannot disagree about what an unset field means.
func configOf(j Job) (Config, error) {
	if !KnownFormat(j.Format) {
		return Config{}, fmt.Errorf("csv-import: unknown format %q (want %s)", j.Format, strings.Join(Formats(), ", "))
	}
	// A delimited file with neither a header row nor a column list has no way to name
	// its fields. The other two formats do: fixed-width is positional and always
	// carries a layout, and an attribute-value file names its own fields.
	if (j.Format == "" || j.Format == FormatCSV) && !j.Writing() && !j.HasHeader && len(j.Columns) == 0 {
		return Config{}, fmt.Errorf("csv-import: a file without a header row must name its columns")
	}
	hasHeader := j.HasHeader
	cfg := Config{Delimiter: j.Delimiter, HasHeader: &hasHeader, Format: j.Format}
	for _, col := range j.Columns {
		col.Name = strings.TrimSpace(col.Name)
		if col.Name == "" {
			continue
		}
		// Without a header row the listed columns *are* the file's order — that is the
		// only thing "list your columns" can mean for a headerless file, and it is what
		// the modeler asks for. The CSV parser requires an explicit index in that case,
		// and nothing was supplying one, so a headerless CSV task failed at
		// runtime with "column needs an index" despite the model being exactly what the
		// compiler demands (ADR-0139).
		if !hasHeader && col.Index == nil {
			i := len(cfg.Columns)
			col.Index = &i
		}
		cfg.Columns = append(cfg.Columns, col)
	}
	return cfg, nil
}

// Resolve turns a compiled CSV task into a [Job]: the authored layout from
// the detail, and the source text read up the task's scope chain. It is engine work
// by necessity — both of its inputs live only there.
func Resolve(store VarStore, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, elementInstanceKey uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("csv-import: task has no detail")
	}
	vars, err := scopeChainVars(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("csv-import: read variables: %w", err)
	}
	sourceName := cp.Intern(detail.CsvSource)
	if sourceName == "" {
		sourceName = csvSourceVar
	}
	src, ok := vars[sourceName]
	if !ok {
		return Job{}, fmt.Errorf("csv-import: source variable %q is missing", sourceName)
	}
	j := Job{
		Source:    src.Text,
		Delimiter: cp.Intern(detail.CsvDelimiter),
		HasHeader: detail.CsvHasHeader,
		Format:    cp.Intern(detail.CsvFormat),
		Operation: cp.Intern(detail.CsvOperation),
		Result:    cp.Intern(detail.CsvResult),
	}
	// Reading takes text; writing takes the rows, which are a structured variable and
	// travel as the JSON they already are.
	if j.Writing() {
		if src.Kind != model.VarJSON || strings.TrimSpace(src.Text) == "" {
			return Job{}, fmt.Errorf("csv-import: source variable %q must hold the rows to write, as a JSON array", sourceName)
		}
	} else if src.Kind != model.VarString || src.Text == "" {
		return Job{}, fmt.Errorf("csv-import: source variable %q is missing or not a non-empty string", sourceName)
	}
	for i, ci := range detail.CsvColumns {
		name := cp.Intern(ci)
		if name == "" {
			continue
		}
		col := Column{Name: name}
		if i < len(detail.CsvWidths) {
			col.Width = int(detail.CsvWidths[i])
		}
		j.Columns = append(j.Columns, col)
	}
	return j, nil
}

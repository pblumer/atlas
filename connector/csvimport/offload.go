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
	Source string `json:"source"`
	// SourceDocument is the id of the uploaded document a reading task's source variable
	// names, when the file was not read where the job was resolved — on the engine's run
	// loop, which does no file or network I/O. The worker reads it under the job's lease
	// and parses it as Source (ADR-0543).
	SourceDocument string   `json:"sourceDocument,omitempty"`
	Delimiter      string   `json:"delimiter,omitempty"`
	HasHeader      bool     `json:"hasHeader"`
	Columns        []Column `json:"columns,omitempty"`
	// Format is csv (the default and what every model authored before formats
	// existed), fixed-width, or avp. Operation is read (the default) or write.
	Format    string `json:"format,omitempty"`
	Operation string `json:"operation,omitempty"`
	// Result names the process variable the outcome is written to; empty means the
	// default. On a read that is the rows; on a write it is the rendered file.
	Result string `json:"resultVariable,omitempty"`
	// apply-corrections (ADR-0548, S2): the corrected records, resolved from the task's
	// corrections variable when it holds a list, or the document they are in when it
	// holds a file; the line numbers to drop; the name of the file written, and the
	// activating server's document budget.
	Corrections         []map[string]any `json:"corrections,omitempty"`
	CorrectionsDocument string           `json:"correctionsDocument,omitempty"`
	Drop                []int            `json:"drop,omitempty"`
	DocumentName        string           `json:"documentName,omitempty"`
	DocumentLimit       int64            `json:"documentLimit,omitempty"`
}

// Correcting reports whether the job writes a corrected copy of an uploaded file.
func (j Job) Correcting() bool { return j.Operation == OperationApplyCorrections }

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
	if j.Correcting() {
		return Result{}, fmt.Errorf("csv-import: apply-corrections writes a new document, and where it runs there is no document store to write it to")
	}
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
	if strings.TrimSpace(j.Source) == "" && j.SourceDocument != "" {
		return Result{}, fmt.Errorf("csv-import: the source is document %s, which was not read", j.SourceDocument)
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
	return ResolveWithDocuments(store, cp, detail, elementInstanceKey, nil)
}

// ResolveWithDocuments is [Resolve] with a document reader, for a reading task whose
// source variable names an uploaded document rather than holding its text.
func ResolveWithDocuments(store VarStore, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, elementInstanceKey uint64, docs func(string) ([]byte, error)) (Job, error) {
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
	if j.Correcting() {
		return resolveCorrections(j, src, sourceName, vars, cp, detail)
	}
	// Reading takes text; writing takes the rows, which are a structured variable and
	// travel as the JSON they already are.
	if j.Writing() {
		if src.Kind != model.VarJSON || strings.TrimSpace(src.Text) == "" {
			return Job{}, fmt.Errorf("csv-import: source variable %q must hold the rows to write, as a JSON array", sourceName)
		}
	} else if id := documentID(src); id != "" && docs == nil {
		// Resolved where no document can be read: the worker reads it under its lease.
		j.Source, j.SourceDocument = "", id
	} else {
		text, err := sourceText(src, sourceName, docs)
		if err != nil {
			return Job{}, err
		}
		j.Source = text
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

// resolveCorrections is Resolve for apply-corrections: the source has to be an
// uploaded document, which is read where the job runs, never here; the corrections are
// a list that travels resolved, or a document read where the job runs; the drop list is
// line numbers.
func resolveCorrections(j Job, src model.VariableValue, sourceName string, vars map[string]model.VariableValue, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail) (Job, error) {
	j.Source = ""
	if j.SourceDocument = documentID(src); j.SourceDocument == "" {
		return Job{}, fmt.Errorf("csv-import: source variable %q holds no document; apply-corrections corrects an uploaded file", sourceName)
	}
	j.DocumentName = cp.Intern(detail.CsvDocumentName)
	name := cp.Intern(detail.CsvCorrections)
	c, ok := vars[name]
	if !ok {
		return Job{}, fmt.Errorf("csv-import: corrections variable %q is missing", name)
	}
	if id := documentID(c); id != "" {
		j.CorrectionsDocument = id
	} else {
		var list []map[string]any
		if c.Kind != model.VarJSON || json.Unmarshal([]byte(c.Text), &list) != nil {
			return Job{}, fmt.Errorf("csv-import: corrections variable %q must hold a list of corrected records, or a corrected file", name)
		}
		if list == nil {
			list = []map[string]any{}
		}
		j.Corrections = list
	}
	if name := cp.Intern(detail.CsvDrop); name != "" {
		if d, ok := vars[name]; ok && strings.TrimSpace(d.Text) != "" && d.Text != "null" {
			if d.Kind != model.VarJSON || json.Unmarshal([]byte(d.Text), &j.Drop) != nil {
				return Job{}, fmt.Errorf("csv-import: drop variable %q must hold a list of line numbers", name)
			}
		}
	}
	return j, nil
}

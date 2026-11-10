package dmn

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/csvimport"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// A business rule task over a file (ADR-0548).
//
// An ordinary business rule task evaluates its decision once, over the instance's
// variables. One with <atlas:rowsFrom> evaluates it once per record of a document, in
// this one job, and completes with a summary instead: how many records there were,
// how many passed and failed, a bounded sample of the failures, and a reference to a
// result document holding every record with its verdict. The engine records one job
// and one evaluation, whatever the size of the file — which is the point: the
// multi-instance alternative records every row and costs the square of their number
// (benchmarks/results/measurement-95bce7d.md).

// RowDocuments is the document store as the DMN worker reaches it for a business rule
// task over a file: the source read for the job's own instance only, and the result
// stored as a new document, which the job's completion binds to that instance.
type RowDocuments interface {
	ReadDocument(instance uint64, id string) ([]byte, error)
	StoreDocument(ctx context.Context, name, contentType string, data []byte) (map[string]any, error)
}

// RowsOptions is what a business rule task over a file needs beyond an ordinary one.
type RowsOptions struct {
	Docs RowDocuments
	// MaxSummary is the largest the summary variable may be, in bytes: the failure
	// sample is cut to fit. Zero means 1 MiB, the default variable budget.
	MaxSummary int
}

// The columns a result document adds after the record's own.
const (
	rowColumn   = "_row"
	validColumn = "_valid"
	errorColumn = "_error"
	// dropColumn is the failures document's last column: x there drops the record when
	// the corrected file is applied (csvimport, apply-corrections).
	dropColumn = "_drop"
)

// rowsSummary is the variable a business rule task over a file completes with.
type rowsSummary struct {
	Total    int             `json:"total"`
	Valid    int             `json:"valid"`
	Invalid  int             `json:"invalid"`
	Errors   int             `json:"errors"`
	Decision summaryDecision `json:"decision"`
	Source   map[string]any  `json:"source"`
	Result   map[string]any  `json:"result"`
	// FailuresDocument holds only the records that failed, in the result document's
	// layout: the file to download, correct and upload again when there are too many
	// failures for a task each (ADR-0548, S2). Absent when nothing failed.
	FailuresDocument map[string]any   `json:"failuresDocument,omitempty"`
	Failures         []map[string]any `json:"failures"`
	// Truncated says the failures shown are fewer than the failed records: the
	// result document has the rest.
	Truncated bool `json:"truncated,omitempty"`
}

type summaryDecision struct {
	ID            string `json:"id"`
	DeploymentKey uint64 `json:"deploymentKey,omitempty"`
}

// evaluateRows runs a business rule task over a file. The decision is resolved once,
// by the first record, and every later record is evaluated against that same
// deployment, so one file is never checked against two versions of the rules.
func evaluateRows(ctx context.Context, store state.Reader, j job.Job, ei *model.ElementInstanceValue, cp *compiler.CompiledProcess,
	detail *compiler.BusinessRuleTaskDetail, decisionId string, evaluate Evaluator, opts RowsOptions) (job.Completion, error) {
	rows := detail.Rows
	if opts.Docs == nil {
		return job.Completion{}, fmt.Errorf("dmn: business rule task %q evaluates over a file, and this worker has no document store", ei.ElementId)
	}
	resultVar := cp.Intern(detail.ResultVar)
	if resultVar == "" {
		return job.Completion{}, fmt.Errorf("dmn: business rule task %q evaluates over a file and names no result variable for its summary", ei.ElementId)
	}
	vars, err := readScopeChainVars(store, j.ElementInstanceKey)
	if err != nil {
		return job.Completion{}, fmt.Errorf("dmn: read variables: %w", err)
	}
	name, path, _ := strings.Cut(rows.Source, ".")
	src, ok := vars[name]
	if !ok {
		return job.Completion{}, fmt.Errorf("dmn: the file variable %q is missing", name)
	}
	ref, id := documentRefAt(src, path)
	if id == "" {
		return job.Completion{}, fmt.Errorf("dmn: variable %q holds no document; a business rule task over a file reads an uploaded document", rows.Source)
	}
	data, err := opts.Docs.ReadDocument(ei.ProcessInstanceKey, id)
	if err != nil {
		return job.Completion{}, fmt.Errorf("dmn: read document %s: %w", id, err)
	}
	base, err := decodeInputs(cp.Intern(detail.Inputs))
	if err != nil {
		return job.Completion{}, err
	}

	cfg := csvimport.Config{Delimiter: rows.Delimiter, HasHeader: &rows.HasHeader}
	for i, name := range rows.Columns {
		col := csvimport.Column{Name: name}
		if !rows.HasHeader {
			idx := i
			col.Index = &idx
		}
		cfg.Columns = append(cfg.Columns, col)
	}
	validOutput := rows.ValidOutput
	if validOutput == "" {
		validOutput = compiler.DefaultRowsValidOutput
	}

	sum := rowsSummary{Decision: summaryDecision{ID: decisionId}, Source: ref, Failures: []map[string]any{}}
	out := newResultWriter(rows.Delimiter)
	// fails is the same layout, filtered to the records that failed, with a _drop
	// column to mark a record to leave out.
	fails := newResultWriter(rows.Delimiter)
	fails.drop = true
	var key uint64
	resolved := false
	pinned := evaluate
	header, err := csvimport.EachRow(cfg, bytes.NewReader(data), func(rec csvimport.Record) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		inputs := rowInputs(ei.ProcessInstanceKey, base, vars, detail.InputMappings, rec.Row)
		eval, err := pinned(ctx, decisionId, inputs)
		if err == nil && !resolved {
			// Every later record goes to the deployment the first one was answered by.
			resolved, key = true, eval.Key
			if key != 0 {
				pinned = func(ctx context.Context, id string, in map[string]any) (Evaluation, error) {
					return evaluate(withPinnedKey(ctx, key), id, in)
				}
			}
		}
		sum.Total++
		if err != nil {
			sum.Errors++
			if werr := out.add(rec.Header, rows.Columns, rec, false, nil, err.Error()); werr != nil {
				return werr
			}
			if werr := fails.add(rec.Header, rows.Columns, rec, false, nil, err.Error()); werr != nil {
				return werr
			}
			sum.addFailure(rec, nil, err.Error(), int(rows.FailureSample))
			return nil
		}
		valid, reasons, verr := verdictOf(eval.Outputs, decisionId, validOutput)
		if verr != nil {
			sum.Errors++
			if werr := out.add(rec.Header, rows.Columns, rec, false, nil, verr.Error()); werr != nil {
				return werr
			}
			if werr := fails.add(rec.Header, rows.Columns, rec, false, nil, verr.Error()); werr != nil {
				return werr
			}
			sum.addFailure(rec, nil, verr.Error(), int(rows.FailureSample))
			return nil
		}
		if valid {
			sum.Valid++
		} else {
			sum.Invalid++
			sum.addFailure(rec, reasons, "", int(rows.FailureSample))
			if werr := fails.add(rec.Header, rows.Columns, rec, false, reasons, ""); werr != nil {
				return werr
			}
		}
		return out.add(rec.Header, rows.Columns, rec, valid, reasons, "")
	})
	if err != nil {
		return job.Completion{}, fmt.Errorf("dmn: read %s: %w", rows.Source, err)
	}
	sum.Decision.DeploymentKey = key
	result, err := out.bytes(header, rows.Columns)
	if err != nil {
		return job.Completion{}, err
	}
	stored, err := opts.Docs.StoreDocument(ctx, resultName(ref), "text/csv; charset=utf-8", result)
	if err != nil {
		return job.Completion{}, fmt.Errorf("dmn: store the result document: %w", err)
	}
	sum.Result = stored
	if sum.Invalid+sum.Errors > 0 {
		failed, err := fails.bytes(header, rows.Columns)
		if err != nil {
			return job.Completion{}, err
		}
		ref, err := opts.Docs.StoreDocument(ctx, failuresName(ref), "text/csv; charset=utf-8", failed)
		if err != nil {
			return job.Completion{}, fmt.Errorf("dmn: store the failures document: %w", err)
		}
		sum.FailuresDocument = ref
	}
	summary, err := sum.encode(opts.MaxSummary)
	if err != nil {
		return job.Completion{}, err
	}
	// One evaluation record for the run (ADR-0066): which file, which decision, the
	// counts. A trace per record is exactly the volume this task exists to avoid; the
	// result document says why each record failed.
	inputsJSON, _ := json.Marshal(map[string]any{"source": ref, "records": sum.Total})
	outputsJSON, _ := json.Marshal(map[string]any{"total": sum.Total, "valid": sum.Valid, "invalid": sum.Invalid, "errors": sum.Errors, "result": stored})
	return job.Completion{
		Outputs: []model.VariableValue{{Name: resultVar, Kind: model.VarJSON, Text: summary}},
		Decision: &model.DecisionEvaluationValue{
			ProcessInstanceKey: ei.ProcessInstanceKey,
			ElementInstanceKey: j.ElementInstanceKey,
			ProcessDefKey:      cp.Key,
			ElementId:          ei.ElementId,
			DecisionId:         decisionId,
			InputsJSON:         string(inputsJSON),
			OutputsJSON:        string(outputsJSON),
			DecisionKey:        key,
		},
	}, nil
}

// rowInputs is one record's decision input context. With input mappings, each is
// evaluated with the record bound as `row` beside the instance's variables; without,
// the record's fields are the inputs, by name. Static inputs are the base either way.
func rowInputs(piKey uint64, base map[string]any, vars map[string]model.VariableValue, mappings []compiler.DecisionInputMapping, row map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(row)+len(mappings))
	for k, v := range base {
		out[k] = v
	}
	if len(mappings) == 0 {
		for k, v := range row {
			out[k] = v
		}
		return out
	}
	var bound expr.Value
	for _, m := range mappings {
		binding := bindStoredVars(piKey, vars, m.Source.Inputs())
		for _, name := range m.Source.Inputs() {
			if name == "row" {
				if bound == nil {
					bound = expr.FromJSON(row)
				}
				if binding == nil {
					binding = map[string]expr.Value{}
				}
				binding["row"] = bound
			}
		}
		v, err := m.Source.Eval(binding)
		if err != nil {
			out[m.Target] = nil
			continue
		}
		out[m.Target] = feelToInput(v)
	}
	return out
}

// verdictOf reads a record's verdict out of a decision's outputs. A decision with one
// boolean output is the verdict itself; one with several names the verdict in
// validOutput, and its other outputs are the reasons.
func verdictOf(outputs map[string]any, decisionId, validOutput string) (bool, map[string]any, error) {
	val, ok := outputs[decisionId]
	if !ok && len(outputs) == 1 {
		for _, v := range outputs {
			val, ok = v, true
		}
	}
	if !ok {
		return false, nil, fmt.Errorf("decision %q answered no output", decisionId)
	}
	switch v := val.(type) {
	case bool:
		return v, nil, nil
	case map[string]any:
		valid, ok := v[validOutput].(bool)
		if !ok {
			return false, nil, fmt.Errorf("decision output %q is not true or false (%v)", validOutput, v[validOutput])
		}
		reasons := make(map[string]any, len(v))
		for k, x := range v {
			if k != validOutput {
				reasons[k] = x
			}
		}
		return valid, reasons, nil
	default:
		return false, nil, fmt.Errorf("decision %q answered %v, not a verdict", decisionId, val)
	}
}

// addFailure adds a failed record to the summary's sample while it has room.
func (s *rowsSummary) addFailure(rec csvimport.Record, reasons map[string]any, errText string, limit int) {
	if len(s.Failures) >= limit {
		s.Truncated = true
		return
	}
	f := make(map[string]any, len(rec.Row)+len(reasons)+2)
	for k, v := range rec.Row {
		f[k] = v
	}
	f[rowColumn] = rec.Line
	for k, v := range reasons {
		f["_"+k] = v
	}
	if errText != "" {
		f[errorColumn] = errText
	}
	s.Failures = append(s.Failures, f)
}

// encode renders the summary, dropping failures from the end of the sample until it
// fits max bytes: the counts and the result document are what the summary is for,
// and the sample is the part that can always be read in full from the document.
func (s *rowsSummary) encode(max int) (string, error) {
	if max <= 0 {
		max = 1 << 20
	}
	for {
		b, err := json.Marshal(s)
		if err != nil {
			return "", fmt.Errorf("dmn: encode summary: %w", err)
		}
		if len(b) <= max || len(s.Failures) == 0 {
			return string(b), nil
		}
		s.Failures = s.Failures[:len(s.Failures)/2]
		s.Truncated = true
	}
}

// resultWriter writes the result document as the records are checked: each record's
// cells as they were, then its line, its verdict, the decision's other outputs, and
// any error. The other outputs are the decision's own and the same for every record,
// so their columns are fixed by the first record that is answered; the records before
// it — ones the decision failed on — wait until then, and nothing else is held.
type resultWriter struct {
	delim   rune
	buf     bytes.Buffer
	cw      *csv.Writer
	width   int
	names   []string
	started bool
	waiting []resultRecord
	line    []string
	// drop adds an empty _drop column at the end: the failures document's, where a
	// person correcting the file marks a record to drop (ADR-0548, S2).
	drop bool
}

type resultRecord struct {
	cells   []string
	line    int
	valid   bool
	reasons map[string]any
	err     string
}

func newResultWriter(delimiter string) *resultWriter {
	w := &resultWriter{delim: ','}
	if r := []rune(delimiter); len(r) == 1 {
		w.delim = r[0]
	}
	w.cw = csv.NewWriter(&w.buf)
	w.cw.Comma = w.delim
	return w
}

// add writes one record's outcome, or keeps it until the columns are known. The
// header is the file's own, or nil for a file without one, whose columns are then
// named by the layout.
func (w *resultWriter) add(header, columns []string, rec csvimport.Record, valid bool, reasons map[string]any, errText string) error {
	r := resultRecord{line: rec.Line, valid: valid, reasons: reasons, err: errText}
	if !w.started && errText != "" {
		r.cells = append([]string(nil), rec.Cells...) // the reader reuses them
		w.waiting = append(w.waiting, r)
		return nil
	}
	r.cells = rec.Cells
	if !w.started {
		if err := w.start(header, columns, reasons); err != nil {
			return err
		}
	}
	return w.write(r)
}

// start writes the header row once the decision's outputs are known.
func (w *resultWriter) start(header, columns []string, reasons map[string]any) error {
	w.started = true
	for k := range reasons {
		w.names = append(w.names, k)
	}
	sort.Strings(w.names)
	head := append([]string(nil), header...)
	if header == nil {
		head = append(head, columns...)
	}
	w.width = len(head)
	head = append(head, rowColumn, validColumn)
	for _, n := range w.names {
		head = append(head, "_"+n)
	}
	head = append(head, errorColumn)
	if w.drop {
		head = append(head, dropColumn)
	}
	if err := w.cw.Write(head); err != nil {
		return err
	}
	waiting := w.waiting
	w.waiting = nil
	for _, r := range waiting {
		if err := w.write(r); err != nil {
			return err
		}
	}
	return nil
}

func (w *resultWriter) write(r resultRecord) error {
	line := w.line[:0]
	// Each record keeps the file's width, so its outcome lines up under the header
	// even where the record was short.
	for i := 0; i < w.width; i++ {
		cell := ""
		if i < len(r.cells) {
			cell = r.cells[i]
		}
		line = append(line, cell)
	}
	line = append(line, strconv.Itoa(r.line), strconv.FormatBool(r.valid))
	for _, n := range w.names {
		line = append(line, cellText(r.reasons[n]))
	}
	line = append(line, r.err)
	if w.drop {
		line = append(line, "")
	}
	w.line = line
	return w.cw.Write(line)
}

// bytes finishes the document. A file the decision answered for no record still gets
// its header and every record, with the error each failed on.
func (w *resultWriter) bytes(header, columns []string) ([]byte, error) {
	if !w.started {
		if err := w.start(header, columns, nil); err != nil {
			return nil, err
		}
	}
	w.cw.Flush()
	return w.buf.Bytes(), w.cw.Error()
}

// cellText renders a decision output as a cell: text as it is, anything else as JSON.
func cellText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// documentRefAt reads a document reference out of a variable, at a dotted path of field
// names inside it ("" for the variable itself): the reference and its id, or "" where
// there is none.
func documentRefAt(v model.VariableValue, path string) (map[string]any, string) {
	if v.Kind != model.VarJSON {
		return nil, ""
	}
	var ref map[string]any
	if json.Unmarshal([]byte(v.Text), &ref) != nil {
		return nil, ""
	}
	if path != "" {
		for _, field := range strings.Split(path, ".") {
			next, ok := ref[field].(map[string]any)
			if !ok {
				return nil, ""
			}
			ref = next
		}
	}
	id, _ := ref["$document"].(string)
	if !strings.HasPrefix(id, "doc_") {
		return nil, ""
	}
	return ref, id
}

// resultName names the result document after the file it checked: antrag.csv becomes
// antrag.result.csv, so the two sort side by side wherever they are downloaded to.
// failuresName is the failures document's name beside the result's: data.csv gives
// data.failures.csv.
func failuresName(ref map[string]any) string {
	return strings.TrimSuffix(resultName(ref), ".result.csv") + ".failures.csv"
}

func resultName(ref map[string]any) string {
	name, _ := ref["name"].(string)
	name = path.Base(strings.TrimSpace(name))
	if name == "" || name == "." || name == "/" {
		name = "records.csv"
	}
	ext := path.Ext(name)
	return strings.TrimSuffix(name, ext) + ".result.csv"
}

package csvimport

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// VarStore is the slice of the state store the CSV-import worker reads — a scope's
// variables and an element instance's parent scope. A narrow interface so the
// scope-chain walk (and its error paths) are testable with a fake (*state.Store
// satisfies it).
type VarStore interface {
	VariablesOfScope(scope uint64, fn func(v *model.VariableValue) error) error
	GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error)
}

// ProcessLookup resolves a process-definition key to its compiled process, so the
// worker can read a CSV worker task's authored layout from the model it belongs to
// (ADR-0139) — mirroring the mail/rest/DMN workers' ProcessLookup.
type ProcessLookup func(defKey uint64) *compiler.CompiledProcess

// csvSourceVar / csvConfigVar / csvRowsVar / csvRowCountVar are the conventional
// variable names the CSV-import worker reads and writes on the legacy path (ADR-0087):
// the upload user-task form supplies csvSourceVar; a preceding script task supplies
// csvConfigVar. csvSourceVar and csvRowsVar are also the defaults a csvConnector uses
// when its source/result variables are unset (ADR-0139).
const (
	csvSourceVar   = "csvText"
	csvConfigVar   = "columnConfig"
	csvRowsVar     = "rows"
	csvRowCountVar = "rowCount"
)

// Handler is the in-process worker for a CSV-import service task
// (compiler.CsvImportJobType). It converts an uploaded CSV into a JSON `rows`
// collection so a batch of records is ingested entirely within the process, the file
// having arrived through a user-task form.
//
// It serves two authoring shapes on the one reserved job type:
//
//   - A first-class CSV-to-JSON task (ADR-0139): the source variable,
//     delimiter, header handling, columns, and result variable are authored on the
//     task and compiled into a worker detail, which the worker reads from the
//     compiled process (like the mail/rest workers). This is preferred when present.
//   - The ADR-0087 variable convention: with no worker detail, the worker reads
//     `csvText` and `columnConfig` up the task's scope chain and writes `rows` +
//     `rowCount`, so already-deployed models keep running unchanged.
//
// A missing/empty source, an absent or malformed layout, or an unparseable CSV is a
// worker error: the job fails and (retries exhausted) raises an incident, rather than
// silently producing an empty batch.
func Handler(store VarStore, lookup ProcessLookup) job.OutputHandler {
	return HandlerWithDocuments(store, lookup, nil)
}

// DocumentReader reads the bytes of a document by its id, for the job's process
// instance (ADR-draft-a-file-is-a-document-not-a-variable). A source variable may hold a
// reference to an uploaded file instead of the file's text; the server passes a reader
// for its document store that answers only for a document that instance was given, and
// a worker without one says so rather than parsing nothing.
type DocumentReader func(instance uint64, id string) ([]byte, error)

// HandlerWithDocuments is [Handler] with a document reader, so a source variable that
// names a document is parsed from the document's bytes.
func HandlerWithDocuments(store VarStore, lookup ProcessLookup, docs DocumentReader) job.OutputHandler {
	return func(j job.Job) ([]model.VariableValue, error) {
		ei, ok, err := store.GetElementInstance(j.ElementInstanceKey)
		if err != nil {
			return nil, fmt.Errorf("csv-import: get element instance: %w", err)
		}
		if !ok {
			return nil, nil // element instance gone (e.g. already completed); nothing to do
		}
		// Prefer the compiled worker detail (ADR-0139) when the task is an
		// atlas:csvConnector; fall back to the ADR-0087 variable convention otherwise.
		// The runner dispatches this worker by the CSV job type alone, so a
		// TypeConnectorTask reaching it is always a CSV worker.
		if lookup != nil {
			if cp := lookup(ei.ProcessDefKey); cp != nil {
				if node := cp.Node(ei.ElementId); node.Type == compiler.TypeConnectorTask {
					return rowsFromConnector(store, cp, cp.ConnectorTask(node.Detail), j.ElementInstanceKey, forInstance(docs, j.ProcessInstanceKey))
				}
			}
		}
		vars, err := scopeChainVars(store, j.ElementInstanceKey)
		if err != nil {
			return nil, fmt.Errorf("csv-import: read variables: %w", err)
		}
		return rowsFromVarsWith(vars, forInstance(docs, j.ProcessInstanceKey))
	}
}

// forInstance binds a reader to the job's instance, so the parsing below asks by id only.
func forInstance(docs DocumentReader, instance uint64) func(string) ([]byte, error) {
	if docs == nil {
		return nil
	}
	return func(id string) ([]byte, error) { return docs(instance, id) }
}

// rowsFromConnector runs a CSV-to-JSON task (ADR-0139) in process. It is
// the same two steps a worker takes — [Resolve] the task into plain values, then
// [Run] them — so the in-process path and an out-of-process one cannot disagree
// about defaults, validation, or what a headerless file's column list means
// (ADR-0168).
func rowsFromConnector(store VarStore, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, elementInstanceKey uint64, docs func(string) ([]byte, error)) ([]model.VariableValue, error) {
	j, err := ResolveWithDocuments(store, cp, detail, elementInstanceKey, docs)
	if err != nil {
		return nil, err
	}
	res, err := Run(j)
	if err != nil {
		return nil, err
	}
	// A read produces structured rows; a write produces the rendered file as text.
	// Both go through Result so the in-process path and a worker cannot decide
	// separately what a result looks like.
	out := model.VariableValue{Name: res.ResultVariable, Kind: model.VarJSON, Text: res.RowsJSON}
	if res.IsText {
		out = model.VariableValue{Name: res.ResultVariable, Kind: model.VarString, Text: res.Text}
	}
	return []model.VariableValue{
		out,
		{Name: csvRowCountVar, Kind: model.VarNumber, Text: strconv.Itoa(res.RowCount)},
	}, nil
}

// rowsFromVars is the legacy CSV-import worker's pure core (ADR-0087): given the
// variables a task sees, it validates the csvText/columnConfig inputs, parses the CSV
// against the layout, and returns the rows/rowCount output variables. Split out so its
// input and parse-error branches are unit-testable without the engine.
func rowsFromVars(vars map[string]model.VariableValue) ([]model.VariableValue, error) {
	return rowsFromVarsWith(vars, nil)
}

// rowsFromVarsWith is rowsFromVars with a document reader: csvText may hold the file's
// text, as it always could, or a reference to the uploaded file.
func rowsFromVarsWith(vars map[string]model.VariableValue, docs func(string) ([]byte, error)) ([]model.VariableValue, error) {
	src, ok := vars[csvSourceVar]
	if !ok {
		return nil, fmt.Errorf("csv-import: variable %q is missing or not a non-empty string", csvSourceVar)
	}
	content, err := sourceText(src, csvSourceVar, docs)
	if err != nil {
		return nil, err
	}
	cfgVar, ok := vars[csvConfigVar]
	if !ok || cfgVar.Kind != model.VarJSON {
		return nil, fmt.Errorf("csv-import: variable %q is missing or not a JSON object", csvConfigVar)
	}
	var cfg Config
	if err := json.Unmarshal([]byte(cfgVar.Text), &cfg); err != nil {
		return nil, fmt.Errorf("csv-import: invalid %s: %v", csvConfigVar, err)
	}
	rows, err := ParseRows(cfg, []byte(content))
	if err != nil {
		return nil, fmt.Errorf("csv-import: %v", err)
	}
	rowsJSON, err := rowsToJSON(rows)
	if err != nil {
		return nil, err
	}
	return []model.VariableValue{
		{Name: csvRowsVar, Kind: model.VarJSON, Text: rowsJSON},
		{Name: csvRowCountVar, Kind: model.VarNumber, Text: strconv.Itoa(len(rows))},
	}, nil
}

// rowsToJSON canonicalizes the parsed rows into the JSON text a JSON process
// variable stores, through the same expr path as any other variable so it round-trips
// on replay. Shared by both worker paths.
func rowsToJSON(rows []map[string]any) (string, error) {
	rowsAny := make([]any, len(rows))
	for i := range rows {
		rowsAny[i] = rows[i]
	}
	rowsJSON, ok := expr.ToJSON(expr.FromJSON(rowsAny))
	if !ok {
		return "", fmt.Errorf("csv-import: parsed rows are not encodable as JSON")
	}
	return rowsJSON, nil
}

// scopeChainVars reads the variables an element sees up its scope chain (nearest
// scope wins), through the shared walk every job worker uses (ADR-0068), so a
// CSV-import task nested in a subprocess still reads its enclosing scope's
// csvText/columnConfig (or a worker's source variable).
func scopeChainVars(store VarStore, elementInstanceKey uint64) (map[string]model.VariableValue, error) {
	return state.VisibleVariablesMap(store, elementInstanceKey)
}

// documentID returns the id a JSON variable names when it is a document reference —
// {"$document": "doc_…", …} — and "" otherwise. The shape is the document store's
// (api/docstore); it is read here by its key alone, so this package does not depend on
// the server's.
func documentID(v model.VariableValue) string {
	if v.Kind != model.VarJSON || !strings.HasPrefix(strings.TrimSpace(v.Text), "{") {
		return ""
	}
	var ref struct {
		ID string `json:"$document"`
	}
	if json.Unmarshal([]byte(v.Text), &ref) != nil || !strings.HasPrefix(ref.ID, "doc_") {
		return ""
	}
	return ref.ID
}

// sourceText is a reading task's input: the variable's text, or the bytes of the
// document it names. Anything else — a missing reader, an empty file, a value of
// another kind — is an error naming the variable, never an empty batch.
func sourceText(src model.VariableValue, name string, docs func(string) ([]byte, error)) (string, error) {
	if id := documentID(src); id != "" {
		if docs == nil {
			return "", fmt.Errorf("csv-import: source variable %q names document %s, and this worker cannot read documents", name, id)
		}
		data, err := docs(id)
		if err != nil {
			return "", fmt.Errorf("csv-import: source variable %q names document %s: %v", name, id, err)
		}
		if len(data) == 0 {
			return "", fmt.Errorf("csv-import: source variable %q names an empty document", name)
		}
		return string(data), nil
	}
	if src.Kind != model.VarString || src.Text == "" {
		return "", fmt.Errorf("csv-import: variable %q is missing or not a non-empty string", name)
	}
	return src.Text, nil
}

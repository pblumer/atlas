package csvimport

import (
	"encoding/json"
	"fmt"
	"strconv"

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
// worker can read a CSV connector task's authored layout from the model it belongs to
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
//   - A first-class CSV-to-JSON connector task (ADR-0139): the source variable,
//     delimiter, header handling, columns, and result variable are authored on the
//     task and compiled into a connector detail, which the worker reads from the
//     compiled process (like the mail/rest workers). This is preferred when present.
//   - The ADR-0087 variable convention: with no connector detail, the worker reads
//     `csvText` and `columnConfig` up the task's scope chain and writes `rows` +
//     `rowCount`, so already-deployed models keep running unchanged.
//
// A missing/empty source, an absent or malformed layout, or an unparseable CSV is a
// worker error: the job fails and (retries exhausted) raises an incident, rather than
// silently producing an empty batch.
func Handler(store VarStore, lookup ProcessLookup) job.OutputHandler {
	return func(j job.Job) ([]model.VariableValue, error) {
		ei, ok, err := store.GetElementInstance(j.ElementInstanceKey)
		if err != nil {
			return nil, fmt.Errorf("csv-import: get element instance: %w", err)
		}
		if !ok {
			return nil, nil // element instance gone (e.g. already completed); nothing to do
		}
		// Prefer the compiled connector detail (ADR-0139) when the task is an
		// atlas:csvConnector; fall back to the ADR-0087 variable convention otherwise.
		// The runner dispatches this worker by the CSV job type alone, so a
		// TypeConnectorTask reaching it is always a CSV connector.
		if lookup != nil {
			if cp := lookup(ei.ProcessDefKey); cp != nil {
				if node := cp.Node(ei.ElementId); node.Type == compiler.TypeConnectorTask {
					return rowsFromConnector(store, cp, cp.ConnectorTask(node.Detail), j.ElementInstanceKey)
				}
			}
		}
		vars, err := scopeChainVars(store, j.ElementInstanceKey)
		if err != nil {
			return nil, fmt.Errorf("csv-import: read variables: %w", err)
		}
		return rowsFromVars(vars)
	}
}

// rowsFromConnector runs a CSV-to-JSON connector task (ADR-0139) in process. It is
// the same two steps a worker takes — [Resolve] the task into plain values, then
// [Run] them — so the in-process path and an out-of-process one cannot disagree
// about defaults, validation, or what a headerless file's column list means
// (ADR-0168).
func rowsFromConnector(store VarStore, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, elementInstanceKey uint64) ([]model.VariableValue, error) {
	j, err := Resolve(store, cp, detail, elementInstanceKey)
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
	text, ok := vars[csvSourceVar]
	if !ok || text.Kind != model.VarString || text.Text == "" {
		return nil, fmt.Errorf("csv-import: variable %q is missing or not a non-empty string", csvSourceVar)
	}
	cfgVar, ok := vars[csvConfigVar]
	if !ok || cfgVar.Kind != model.VarJSON {
		return nil, fmt.Errorf("csv-import: variable %q is missing or not a JSON object", csvConfigVar)
	}
	var cfg Config
	if err := json.Unmarshal([]byte(cfgVar.Text), &cfg); err != nil {
		return nil, fmt.Errorf("csv-import: invalid %s: %v", csvConfigVar, err)
	}
	rows, err := ParseRows(cfg, []byte(text.Text))
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
// csvText/columnConfig (or a connector's source variable).
func scopeChainVars(store VarStore, elementInstanceKey uint64) (map[string]model.VariableValue, error) {
	return state.VisibleVariablesMap(store, elementInstanceKey)
}

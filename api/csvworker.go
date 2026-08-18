package api

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
)

// csvVarStore is the slice of the state store the CSV-import worker reads — a scope's
// variables and an element instance's parent scope. A narrow interface so the
// scope-chain walk (and its error paths) are testable with a fake (*state.Store
// satisfies it).
type csvVarStore interface {
	VariablesOfScope(scope uint64, fn func(v *model.VariableValue) error) error
	GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error)
}

// csvProcessLookup resolves a process-definition key to its compiled process, so the
// worker can read a CSV connector task's authored layout from the model it belongs to
// (ADR-0139) — mirroring the mail/rest/DMN workers' ProcessLookup.
type csvProcessLookup func(defKey uint64) *compiler.CompiledProcess

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

// csvMaxScopeDepth bounds the scope-chain walk, a defensive guard against a cyclic
// or corrupt FlowScopeKey chain (mirrors the worker-side guards elsewhere).
const csvMaxScopeDepth = 64

// csvImportHandler is the in-process worker for a CSV-import service task
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
func csvImportHandler(store csvVarStore, lookup csvProcessLookup) job.OutputHandler {
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
					return csvRowsFromConnector(store, cp, cp.ConnectorTask(node.Detail), j.ElementInstanceKey)
				}
			}
		}
		vars, err := csvScopeChainVars(store, j.ElementInstanceKey)
		if err != nil {
			return nil, fmt.Errorf("csv-import: read variables: %w", err)
		}
		return csvRowsFromVars(vars)
	}
}

// csvRowsFromConnector runs a CSV-to-JSON connector task (ADR-0139): it reads the raw
// text from the authored source variable up the task's scope chain, builds the column
// layout from the compiled detail (delimiter, header flag, columns), parses it with
// the same parser the endpoint uses, and returns the JSON rows under the authored
// result variable plus rowCount. Source/result/delimiter default to csvText/rows/","
// when unset; an empty column list derives the columns from the header row.
func csvRowsFromConnector(store csvVarStore, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, elementInstanceKey uint64) ([]model.VariableValue, error) {
	if detail == nil {
		return nil, fmt.Errorf("csv-import: connector task has no detail")
	}
	vars, err := csvScopeChainVars(store, elementInstanceKey)
	if err != nil {
		return nil, fmt.Errorf("csv-import: read variables: %w", err)
	}
	sourceName := cp.Intern(detail.CsvSource)
	if sourceName == "" {
		sourceName = csvSourceVar
	}
	text, ok := vars[sourceName]
	if !ok || text.Kind != model.VarString || text.Text == "" {
		return nil, fmt.Errorf("csv-import: source variable %q is missing or not a non-empty string", sourceName)
	}
	hasHeader := detail.CsvHasHeader
	cfg := csvConfig{
		Delimiter: cp.Intern(detail.CsvDelimiter),
		HasHeader: &hasHeader,
	}
	for _, ci := range detail.CsvColumns {
		if name := cp.Intern(ci); name != "" {
			cfg.Columns = append(cfg.Columns, csvColumn{Name: name})
		}
	}
	rows, err := parseCSVRows(cfg, []byte(text.Text))
	if err != nil {
		return nil, fmt.Errorf("csv-import: %v", err)
	}
	rowsJSON, err := csvRowsToJSON(rows)
	if err != nil {
		return nil, err
	}
	resultName := cp.Intern(detail.CsvResult)
	if resultName == "" {
		resultName = csvRowsVar
	}
	return []model.VariableValue{
		{Name: resultName, Kind: model.VarJSON, Text: rowsJSON},
		{Name: csvRowCountVar, Kind: model.VarNumber, Text: strconv.Itoa(len(rows))},
	}, nil
}

// csvRowsFromVars is the legacy CSV-import worker's pure core (ADR-0087): given the
// variables a task sees, it validates the csvText/columnConfig inputs, parses the CSV
// against the layout, and returns the rows/rowCount output variables. Split out so its
// input and parse-error branches are unit-testable without the engine.
func csvRowsFromVars(vars map[string]model.VariableValue) ([]model.VariableValue, error) {
	text, ok := vars[csvSourceVar]
	if !ok || text.Kind != model.VarString || text.Text == "" {
		return nil, fmt.Errorf("csv-import: variable %q is missing or not a non-empty string", csvSourceVar)
	}
	cfgVar, ok := vars[csvConfigVar]
	if !ok || cfgVar.Kind != model.VarJSON {
		return nil, fmt.Errorf("csv-import: variable %q is missing or not a JSON object", csvConfigVar)
	}
	var cfg csvConfig
	if err := json.Unmarshal([]byte(cfgVar.Text), &cfg); err != nil {
		return nil, fmt.Errorf("csv-import: invalid %s: %v", csvConfigVar, err)
	}
	rows, err := parseCSVRows(cfg, []byte(text.Text))
	if err != nil {
		return nil, fmt.Errorf("csv-import: %v", err)
	}
	rowsJSON, err := csvRowsToJSON(rows)
	if err != nil {
		return nil, err
	}
	return []model.VariableValue{
		{Name: csvRowsVar, Kind: model.VarJSON, Text: rowsJSON},
		{Name: csvRowCountVar, Kind: model.VarNumber, Text: strconv.Itoa(len(rows))},
	}, nil
}

// csvRowsToJSON canonicalizes the parsed rows into the JSON text a JSON process
// variable stores, through the same expr path as any other variable so it round-trips
// on replay. Shared by both worker paths.
func csvRowsToJSON(rows []map[string]any) (string, error) {
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

// csvScopeChainVars reads the variables an element sees up its scope chain (nearest
// scope wins), mirroring the worker scope-chain reads elsewhere (dmn/script), so a
// CSV-import task nested in a subprocess still reads its enclosing scope's
// csvText/columnConfig (or a connector's source variable).
func csvScopeChainVars(store csvVarStore, elementInstanceKey uint64) (map[string]model.VariableValue, error) {
	vars := map[string]model.VariableValue{}
	scope := elementInstanceKey
	for depth := 0; depth <= csvMaxScopeDepth; depth++ {
		if err := store.VariablesOfScope(scope, func(v *model.VariableValue) error {
			if _, seen := vars[v.Name]; !seen {
				vars[v.Name] = *v
			}
			return nil
		}); err != nil {
			return nil, err
		}
		ei, ok, err := store.GetElementInstance(scope)
		if err != nil {
			return nil, err
		}
		if !ok || ei.FlowScopeKey == 0 || ei.FlowScopeKey == scope {
			break
		}
		scope = ei.FlowScopeKey
	}
	return vars, nil
}

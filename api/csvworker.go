package api

import (
	"encoding/json"
	"fmt"
	"strconv"

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

// csvSourceVar / csvConfigVar / csvRowsVar / csvRowCountVar are the conventional
// variable names the CSV-import worker reads and writes. The upload user-task form
// supplies csvSourceVar; a preceding script task supplies csvConfigVar (ADR-0087).
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
// (compiler.CsvImportJobType, ADR-0087). It reads the raw CSV (`csvText`) and the
// predefined column layout (`columnConfig`) from the task's scope chain, parses the
// text against the layout with the same parser the ingestion endpoint uses, and
// writes `rows` (a JSON array of row objects) and `rowCount` back as process
// variables — so a batch of records is ingested and validated entirely within the
// process, the file having arrived through a user-task form.
//
// A missing/empty source, an absent or malformed layout, or an unparseable CSV is a
// worker error: the job fails and (retries exhausted) raises an incident, the same
// as any other worker, rather than silently producing an empty batch.
func csvImportHandler(store csvVarStore) job.OutputHandler {
	return func(j job.Job) ([]model.VariableValue, error) {
		vars, err := csvScopeChainVars(store, j.ElementInstanceKey)
		if err != nil {
			return nil, fmt.Errorf("csv-import: read variables: %w", err)
		}
		return csvRowsFromVars(vars)
	}
}

// csvRowsFromVars is the CSV-import worker's pure core: given the variables a task
// sees, it validates the csvText/columnConfig inputs, parses the CSV against the
// layout, and returns the rows/rowCount output variables. Split out so its input and
// parse-error branches are unit-testable without the engine.
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
	rowsAny := make([]any, len(rows))
	for i := range rows {
		rowsAny[i] = rows[i]
	}
	rowsJSON, ok := expr.ToJSON(expr.FromJSON(rowsAny))
	if !ok {
		return nil, fmt.Errorf("csv-import: parsed rows are not encodable as JSON")
	}
	return []model.VariableValue{
		{Name: csvRowsVar, Kind: model.VarJSON, Text: rowsJSON},
		{Name: csvRowCountVar, Kind: model.VarNumber, Text: strconv.Itoa(len(rows))},
	}, nil
}

// csvScopeChainVars reads the variables an element sees up its scope chain (nearest
// scope wins), mirroring the worker scope-chain reads elsewhere (dmn/script), so a
// CSV-import task nested in a subprocess still reads its enclosing scope's
// csvText/columnConfig.
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

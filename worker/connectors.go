package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/csvimport"
)

// Connector kinds this worker can serve out of process (ADR-0165).
//
// A connector job arrives already resolved: the engine found the task's detail in
// the compiled process and evaluated it against the instance's variables, because it
// is the only one who can, and what travels is plain values. So the code here does
// only the work itself — for CSV, parsing — and needs nothing from the engine but
// the payload it was handed.
//
// CSV import is the first kind to move because no credential is involved in it at
// all, so the mechanism could be built and reviewed before any secret rode on it.
// The kinds that follow are the ones whose endpoint and credential this worker will
// hold, which is the substance of ADR-0165's decision.

// BuiltinConnectors returns handlers for the named connector kinds, keyed by the
// job type each serves. An unknown name yields nothing: a worker is configured from
// its own command line, so a name it does not implement is a mistake to report at
// startup rather than a queue to lease work from and then fail.
func BuiltinConnectors(kinds ...string) map[string]Exec {
	out := map[string]Exec{}
	for _, kind := range kinds {
		switch kind {
		case "csv":
			out[compiler.CsvImportJobType] = ExecFunc(runCSV)
		}
	}
	return out
}

// runCSV parses a resolved CSV-import job and returns the variables it completes
// with. It shares [csvimport.Run] with the in-process path, so the two cannot
// disagree about defaults, validation, or what a headerless file's column list
// means.
func runCSV(_ context.Context, j Job) (map[string]any, error) {
	if j.Connector == nil {
		return nil, fmt.Errorf("csv: the job carried no resolved connector detail; is this server offloading the csv kind?")
	}
	raw, err := json.Marshal(j.Connector.Fields)
	if err != nil {
		return nil, err
	}
	var task csvimport.Job
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, fmt.Errorf("csv: cannot read the resolved detail: %w", err)
	}
	res, err := csvimport.Run(task)
	if err != nil {
		return nil, err
	}
	var rows any
	if err := json.Unmarshal([]byte(res.RowsJSON), &rows); err != nil {
		return nil, fmt.Errorf("csv: parsed rows are not JSON: %w", err)
	}
	return map[string]any{res.ResultVariable: rows, "rowCount": res.RowCount}, nil
}

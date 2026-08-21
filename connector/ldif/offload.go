package ldif

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
)

// maxScopeDepth bounds the walk up an element's scope chain, matching the other
// connectors' bound so a malformed chain cannot loop.
const maxScopeDepth = 64

// VarStore is the slice of the state store the engine half reads.
type VarStore interface {
	VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error
	GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error)
}

// ProcessLookup resolves a process-definition key to its compiled process.
type ProcessLookup func(defKey uint64) *compiler.CompiledProcess

// Job is a directory-file task with everything already looked up. It carries no
// credential because there is none: the work is a transform over text the process
// already holds.
type Job struct {
	Format    string `json:"format"`
	Operation string `json:"operation,omitempty"`
	// Source is the file text on a read, and the entries as JSON on a write.
	Source string `json:"source"`
	Result string `json:"resultVariable"`
}

// Writing reports whether the job renders a file rather than parsing one.
func (j Job) Writing() bool { return j.Operation == OperationWrite }

// Result is what running a Job produces: on a read the entries, on a write the
// rendered file, plus how many entries were involved.
type Result struct {
	ResultVariable string
	Entries        []Entry
	Text           string
	IsText         bool
	EntryCount     int
}

// Variables is what the job completes with. Both the in-process handler and the
// worker go through it, so neither decides on its own what a result looks like.
func (r Result) Variables() map[string]any {
	if r.IsText {
		return map[string]any{r.ResultVariable: r.Text, "entryCount": r.EntryCount}
	}
	return map[string]any{r.ResultVariable: EntriesToJSON(r.Entries), "entryCount": r.EntryCount}
}

// Run performs a resolved job. It is pure — no network, no credential, no clock — so
// it behaves identically in the engine and in a worker.
func Run(j Job) (Result, error) {
	if !KnownFormat(j.Format) {
		return Result{}, fmt.Errorf("ldif: unknown format %q (want %s)", j.Format, strings.Join(Formats(), " or "))
	}
	name := strings.TrimSpace(j.Result)
	if name == "" {
		return Result{}, fmt.Errorf("ldif: the task names no result variable")
	}
	if j.Writing() {
		entries, err := entriesFromJSON(j.Source)
		if err != nil {
			return Result{}, err
		}
		text, err := Render(j.Format, entries)
		if err != nil {
			return Result{}, fmt.Errorf("ldif: %v", err)
		}
		return Result{ResultVariable: name, Text: text, IsText: true, EntryCount: len(entries)}, nil
	}
	if strings.TrimSpace(j.Source) == "" {
		return Result{}, fmt.Errorf("ldif: the source is empty")
	}
	entries, err := Parse(j.Format, []byte(j.Source))
	if err != nil {
		return Result{}, fmt.Errorf("ldif: %v", err)
	}
	return Result{ResultVariable: name, Entries: entries, EntryCount: len(entries)}, nil
}

// wireEntry is the JSON shape entries travel in — the one the directory connectors
// write, so a process can hand a live directory's result straight to a file writer.
type wireEntry struct {
	DN         string              `json:"dn"`
	Attributes map[string][]string `json:"attributes"`
}

// entriesFromJSON reads the entries a write was handed.
func entriesFromJSON(raw string) ([]Entry, error) {
	var wire []wireEntry
	if err := json.Unmarshal([]byte(raw), &wire); err != nil {
		return nil, fmt.Errorf("ldif: the entries to write are not the {dn, attributes} array a directory read produces: %w", err)
	}
	out := make([]Entry, 0, len(wire))
	for _, w := range wire {
		out = append(out, Entry{DN: w.DN, Attributes: w.Attributes})
	}
	return out, nil
}

// Resolve turns a compiled directory-file task into a [Job]: the authored format and
// direction, and the source read up the task's scope chain. It is engine work by
// necessity — the compiled process and the scope live only there.
func Resolve(store VarStore, cp *compiler.CompiledProcess, detail *compiler.ConnectorTaskDetail, elementInstanceKey uint64) (Job, error) {
	if detail == nil {
		return Job{}, fmt.Errorf("ldif: connector task has no detail")
	}
	vars, err := scopeChainVars(store, elementInstanceKey)
	if err != nil {
		return Job{}, fmt.Errorf("ldif: read variables: %w", err)
	}
	sourceName := cp.Intern(detail.LdifSource)
	src, ok := vars[sourceName]
	if !ok {
		return Job{}, fmt.Errorf("ldif: source variable %q is not set at this task", sourceName)
	}
	j := Job{
		Format:    cp.Intern(detail.LdifFormat),
		Operation: cp.Intern(detail.LdifOperation),
		Source:    src.Text,
		Result:    cp.Intern(detail.LdifResult),
	}
	if j.Writing() {
		if src.Kind != model.VarJSON || strings.TrimSpace(src.Text) == "" {
			return Job{}, fmt.Errorf("ldif: source variable %q must hold the entries to write, as a JSON array", sourceName)
		}
	} else if src.Kind != model.VarString || src.Text == "" {
		return Job{}, fmt.Errorf("ldif: source variable %q must hold the file text", sourceName)
	}
	return j, nil
}

// Handler builds the in-process job handler. Register it for the reserved
// [compiler.LdifJobTypeIndex]; it resolves the task and runs the transform off the
// run loop and after fsync, then completes the job with what Result says.
func Handler(store VarStore, lookup ProcessLookup) job.OutputHandler {
	return func(j job.Job) ([]model.VariableValue, error) {
		ei, ok, err := store.GetElementInstance(j.ElementInstanceKey)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil // element instance gone; nothing to do
		}
		cp := lookup(ei.ProcessDefKey)
		if cp == nil {
			return nil, fmt.Errorf("ldif: no compiled process for def %d", ei.ProcessDefKey)
		}
		detail, err := cp.ConnectorTaskOf(ei.ElementId)
		if err != nil {
			return nil, fmt.Errorf("ldif: %w", err)
		}
		task, err := Resolve(store, cp, detail, j.ElementInstanceKey)
		if err != nil {
			return nil, err
		}
		res, err := Run(task)
		if err != nil {
			return nil, err
		}
		return variablesOf(res)
	}
}

// variablesOf encodes a result as the process variables it becomes.
func variablesOf(res Result) ([]model.VariableValue, error) {
	if res.IsText {
		return []model.VariableValue{
			{Name: res.ResultVariable, Kind: model.VarString, Text: res.Text},
			{Name: "entryCount", Kind: model.VarNumber, Text: fmt.Sprint(res.EntryCount)},
		}, nil
	}
	raw, err := json.Marshal(EntriesToJSON(res.Entries))
	if err != nil {
		return nil, fmt.Errorf("ldif: encode entries: %w", err)
	}
	return []model.VariableValue{
		{Name: res.ResultVariable, Kind: model.VarJSON, Text: string(raw)},
		{Name: "entryCount", Kind: model.VarNumber, Text: fmt.Sprint(res.EntryCount)},
	}, nil
}

// scopeChainVars reads the variables an element sees up its scope chain (nearest
// wins), the same walk the other connectors make.
func scopeChainVars(store VarStore, elementInstanceKey uint64) (map[string]model.VariableValue, error) {
	vars := map[string]model.VariableValue{}
	scope := elementInstanceKey
	for depth := 0; depth <= maxScopeDepth; depth++ {
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

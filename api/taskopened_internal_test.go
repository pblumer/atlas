package api

import (
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// fakeTaskReader answers the reads enriching a task makes, from fixed data.
type fakeTaskReader struct {
	replay    []state.ElementReplayValue
	replayTS  []int64
	replayErr error
	vars      []model.VariableValue
	varsErr   error
}

func (f fakeTaskReader) GetElementInstance(uint64) (*model.ElementInstanceValue, bool, error) {
	return nil, false, nil
}

func (f fakeTaskReader) ElementReplayHistory(_ uint64, fn func(ts int64, pos uint64, v state.ElementReplayValue) error) error {
	for i, v := range f.replay {
		if err := fn(f.replayTS[i], uint64(i), v); err != nil {
			return err
		}
	}
	return f.replayErr
}

func (f fakeTaskReader) VisibleVariablesOfScope(_ uint64, fn func(v *model.VariableValue) error) error {
	for i := range f.vars {
		if err := fn(&f.vars[i]); err != nil {
			return err
		}
	}
	return f.varsErr
}

// A job written before the job carried its creation time still says when it was
// opened: the activation of the element it waits on, from the instance's token
// history — the same moment, since the two are written in one batch.
func TestAnOlderJobIsOpenedWhenItsElementActivated(t *testing.T) {
	jv := &model.JobValue{ProcessInstanceKey: 1, ElementInstanceKey: 20}
	r := fakeTaskReader{
		replay: []state.ElementReplayValue{
			{ElementInstanceKey: 10, Action: state.ReplayActivated},
			{ElementInstanceKey: 20, Action: state.ReplayCompleted}, // not the activation
			{ElementInstanceKey: 20, Action: state.ReplayActivated},
			{ElementInstanceKey: 20, Action: state.ReplayActivated}, // the scan stopped before this
		},
		replayTS: []int64{100, 150, 2_000_000_000, 9_000_000_000},
	}
	if got := activatedAt(r, jv); got != 2_000_000_000 {
		t.Errorf("activatedAt = %d, want the element's activation 2000000000", got)
	}
	tr := enrichTaskWith(r, func(uint64) (string, string, *compiler.CompiledProcess, bool) { return "", "", nil, false }, 5, jv)
	if tr.CreatedAt != 2_000 {
		t.Errorf("createdAt = %d, want 2000 (ms)", tr.CreatedAt)
	}

	// The job's own stamp wins over the history.
	jv.CreatedAt = 7_000_000_000
	if tr := enrichTaskWith(r, func(uint64) (string, string, *compiler.CompiledProcess, bool) { return "", "", nil, false }, 5, jv); tr.CreatedAt != 7_000 {
		t.Errorf("createdAt = %d, want the job's own 7000", tr.CreatedAt)
	}

	// Nothing in the history, or a history that cannot be read: no time rather than a wrong one.
	if got := activatedAt(fakeTaskReader{}, jv); got != 0 {
		t.Errorf("activatedAt with no history = %d, want 0", got)
	}
	if got := activatedAt(fakeTaskReader{replayErr: errors.New("boom")}, jv); got != 0 {
		t.Errorf("activatedAt on a read error = %d, want 0", got)
	}
}

// A task's content is the non-empty text and numbers its form asks for, cut to a
// length and a count that keep a list row a list row.
func TestTaskContentIsShortTextAndNumbers(t *testing.T) {
	long := strings.Repeat("x", maxTaskContentLen+50)
	vars := []model.VariableValue{
		{Name: "recipient", Kind: model.VarString, Text: " usr_1 "},
		{Name: "anzahl", Kind: model.VarNumber, Text: "3"},
		{Name: "eilig", Kind: model.VarBool, Bool: true},
		{Name: "leer", Kind: model.VarString, Text: "  "},
		{Name: "lang", Kind: model.VarString, Text: long},
		{Name: "secret", Kind: model.VarString, Text: "not-on-the-form"},
	}
	form := map[string]bool{"recipient": true, "anzahl": true, "eilig": true, "leer": true, "lang": true}
	got := taskContent(fakeTaskReader{vars: vars}, taskResp{ProcessInstanceKey: 1}, form)
	if len(got) != 3 || got[0] != "usr_1" || got[1] != "3" || len(got[2]) != maxTaskContentLen {
		t.Errorf("content = %q, want [usr_1 3 <%d x>]", got, maxTaskContentLen)
	}

	many := make([]model.VariableValue, maxTaskContentValues+10)
	for i := range many {
		many[i] = model.VariableValue{Name: "v", Kind: model.VarString, Text: "v"}
	}
	if got := taskContent(fakeTaskReader{vars: many}, taskResp{ElementInstanceKey: 2}, map[string]bool{"v": true}); len(got) != maxTaskContentValues {
		t.Errorf("content has %d values, want the cap %d", len(got), maxTaskContentValues)
	}
	if got := taskContent(fakeTaskReader{vars: vars[:1], varsErr: errors.New("boom")}, taskResp{}, form); got != nil {
		t.Errorf("content on a read error = %q, want none", got)
	}
}

// What a form does not ask for is not content, and a task with no form has none:
// the row is held to the same allowlist the variables endpoint gives a task holder.
func TestTaskContentIsOnlyWhatTheFormAsksFor(t *testing.T) {
	vars := []model.VariableValue{
		{Name: "customer", Kind: model.VarString, Text: "Muster AG"},
		{Name: "secret", Kind: model.VarString, Text: "only-for-the-project"},
	}
	if got := taskContent(fakeTaskReader{vars: vars}, taskResp{ProcessInstanceKey: 1}, map[string]bool{"customer": true}); len(got) != 1 || got[0] != "Muster AG" {
		t.Errorf("content = %q, want only [Muster AG]", got)
	}
	for _, fields := range []map[string]bool{nil, {}} {
		if got := taskContent(fakeTaskReader{vars: vars}, taskResp{ProcessInstanceKey: 1}, fields); got != nil {
			t.Errorf("content with no form = %q, want none", got)
		}
	}
}

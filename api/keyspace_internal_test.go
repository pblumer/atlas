package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// twoPoolCollaboration is one model that deploys two definitions, so a test can
// check that a deploy spends the whole span of keys it takes and not just the
// first of them.
const twoPoolCollaboration = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <collaboration id="Collab_keys">
    <participant id="P_left" name="Left" processRef="left"/>
    <participant id="P_right" name="Right" processRef="right"/>
  </collaboration>
  <process id="left" isExecutable="true">
    <startEvent id="ls"/><userTask id="lw"/><endEvent id="le"/>
    <sequenceFlow id="lf1" sourceRef="ls" targetRef="lw"/>
    <sequenceFlow id="lf2" sourceRef="lw" targetRef="le"/>
  </process>
  <process id="right" isExecutable="true">
    <startEvent id="rs"/><userTask id="rw"/><endEvent id="re"/>
    <sequenceFlow id="rf1" sourceRef="rs" targetRef="rw"/>
    <sequenceFlow id="rf2" sourceRef="rw" targetRef="re"/>
  </process>
</definitions>`

// removeKeySpaceMark erases the durable floor, which is the state an installation
// deployed before the floor existed comes back in.
func removeKeySpaceMark(dir string) error {
	entries, err := os.ReadDir(filepath.Join(dir, "keyspace"))
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.Remove(filepath.Join(dir, "keyspace", e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// The acceptance suite for
// ADR-0339.
//
// The defect these pin is not a crash and not a refusal: a deleted definition's
// key came back, and the next definition to get it inherited the instance history
// filed under that key. It is only reachable across a restart, because the counter
// was rebuilt there — so every case here shuts the stack down and boots another
// over the same directory, which is the only instrument that can see it.

// plainProcess is a diagram that starts, waits at a user task, and ends. Two of
// them with different ids are two definitions that cannot be confused for each
// other, which is what makes an inherited history visible.
func plainProcess(id string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="` + id + `" isExecutable="true">
    <startEvent id="s"/><userTask id="wait"/><endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="wait"/>
    <sequenceFlow id="f2" sourceRef="wait" targetRef="e"/>
  </process>
</definitions>`
}

// runThroughProcess is a diagram that completes the moment it starts, so one
// instance leaves a finished count and an element visit behind on its key.
func runThroughProcess(id string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="` + id + `" isExecutable="true">
    <startEvent id="s"/><endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`
}

// runtimeOf reads one definition's live and finished counts and its per-element
// visits — the rows the state store files under a definition key.
func runtimeOf(t *testing.T, x deployTestHarness, key uint64) struct {
	Instances int `json:"instances"`
	Finished  int `json:"finished"`
	Elements  []struct {
		ElementID string `json:"elementId"`
		Visits    int    `json:"visits"`
	} `json:"elements"`
} {
	t.Helper()
	var out struct {
		Instances int `json:"instances"`
		Finished  int `json:"finished"`
		Elements  []struct {
			ElementID string `json:"elementId"`
			Visits    int    `json:"visits"`
		} `json:"elements"`
	}
	code, b := x.do(http.MethodGet, fmt.Sprintf("/api/v1/processes/%d/runtime", key), "")
	if code != http.StatusOK {
		t.Fatalf("runtime %d: %d %s", key, code, b)
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode runtime: %v (%s)", err, b)
	}
	return out
}

// TestADeletedDefinitionsKeyIsNeverIssuedAgain is the headline, and it is written
// as the corruption rather than as the counter: a brand-new process must not
// report a finished instance it never had.
//
// Before the durable floor, the second deploy took the first one's key and with it
// every row the state store had filed under it — measured as finished=1 and a
// visit on an element the new definition had never reached.
func TestADeletedDefinitionsKeyIsNeverIssuedAgain(t *testing.T) {
	dir := t.TempDir()
	stack := bootDecisionStack(t, dir)

	first := deployProcess(t, stack.x, runThroughProcess("alpha"))
	if code, b := stack.x.do(http.MethodPost, "/api/v1/instances", `{"processId":"alpha"}`); code != http.StatusOK {
		t.Fatalf("start instance: %d %s", code, b)
	}
	if got := runtimeOf(t, stack.x, first); got.Finished != 1 {
		t.Fatalf("alpha finished = %d, want 1 — the history this key now carries", got.Finished)
	}
	if code, b := stack.x.do(http.MethodDelete, fmt.Sprintf("/api/v1/processes/%d", first), ""); code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", code, b)
	}
	stack.shutdown()

	rebooted := bootDecisionStack(t, dir)
	defer rebooted.shutdown()
	second := deployProcess(t, rebooted.x, plainProcess("beta"))

	if second == first {
		t.Fatalf("the new definition got key %d, the deleted one's — a key is spent when it is issued, not while a record holds it", second)
	}
	got := runtimeOf(t, rebooted.x, second)
	if got.Finished != 0 || got.Instances != 0 {
		t.Fatalf("beta runtime = %+v, want a definition that has never run to say so", got)
	}
	for _, el := range got.Elements {
		if el.Visits != 0 {
			t.Errorf("beta element %s has %d visits; it has never been reached", el.ElementID, el.Visits)
		}
	}
}

// TestTheKeyFloorSurvivesEveryDefinitionBeingDeleted: with nothing left on disk to
// derive a counter from, the floor is the only thing that remembers. This is the
// case the old derivation got most wrong — an empty store restarted at key 1.
func TestTheKeyFloorSurvivesEveryDefinitionBeingDeleted(t *testing.T) {
	dir := t.TempDir()
	stack := bootDecisionStack(t, dir)

	var keys []uint64
	for _, id := range []string{"one", "two", "three"} {
		keys = append(keys, deployProcess(t, stack.x, plainProcess(id)))
	}
	for _, k := range keys {
		if code, b := stack.x.do(http.MethodDelete, fmt.Sprintf("/api/v1/processes/%d", k), ""); code != http.StatusNoContent {
			t.Fatalf("delete %d: %d %s", k, code, b)
		}
	}
	stack.shutdown()

	rebooted := bootDecisionStack(t, dir)
	defer rebooted.shutdown()
	next := deployProcess(t, rebooted.x, plainProcess("four"))
	for _, k := range keys {
		if next == k {
			t.Fatalf("the next definition got key %d, which %s already spent", next, "a deleted definition")
		}
	}
}

// TestADecisionDeploymentSpendsAKeyThatStaysSpent: decision deployments draw from
// the same counter, so removing one must not free its key either — and the two
// kinds must not collide with each other after a delete on one of them.
func TestADecisionDeploymentSpendsAKeyThatStaysSpent(t *testing.T) {
	dir := t.TempDir()
	stack := bootDecisionStack(t, dir)

	dec := deployOneDecision(t, stack.x, "", eligibilityDMN("approve"))
	if code, b := deleteDecisionDeployment(t, stack.x, dec.Key); code != http.StatusNoContent {
		t.Fatalf("delete decision: %d %s", code, b)
	}
	stack.shutdown()

	rebooted := bootDecisionStack(t, dir)
	defer rebooted.shutdown()
	if again := deployOneDecision(t, rebooted.x, "", eligibilityDMN("vip")); again.Key == dec.Key {
		t.Errorf("the next decision deployment got key %d, the deleted one's", again.Key)
	}
	if proc := deployProcess(t, rebooted.x, plainProcess("gamma")); proc == dec.Key {
		t.Errorf("a process definition got key %d, which a deleted decision deployment spent", proc)
	}
}

// TestAModelDeployingSeveralProcessesSpendsEveryKeyItTakes: a collaboration is one
// deploy and several definitions, so the reservation has to cover the span rather
// than the first of it.
func TestAModelDeployingSeveralProcessesSpendsEveryKeyItTakes(t *testing.T) {
	dir := t.TempDir()
	stack := bootDecisionStack(t, dir)

	code, b := stack.x.do(http.MethodPost, "/api/v1/deployments", twoPoolCollaboration)
	if code != http.StatusOK {
		t.Fatalf("deploy collaboration: %d %s", code, b)
	}
	var rep deployResp
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, b)
	}
	if len(rep.Deployments) != 2 {
		t.Fatalf("deploy = %+v, want two definitions from one model", rep.Deployments)
	}
	spent := map[uint64]bool{}
	for _, d := range rep.Deployments {
		spent[d.Key] = true
		if code, b := stack.x.do(http.MethodDelete, fmt.Sprintf("/api/v1/processes/%d", d.Key), ""); code != http.StatusNoContent {
			t.Fatalf("delete %d: %d %s", d.Key, code, b)
		}
	}
	stack.shutdown()

	rebooted := bootDecisionStack(t, dir)
	defer rebooted.shutdown()
	if next := deployProcess(t, rebooted.x, plainProcess("delta")); spent[next] {
		t.Errorf("the next definition got key %d, which the collaboration already spent", next)
	}
}

// TestAnInstallationWithNoFloorYetKeepsItsKeys: the floor is new, so the first boot
// after an upgrade finds none. The surviving records must still raise the counter
// exactly as they always did — otherwise the fix would re-issue every key it was
// written to protect.
func TestAnInstallationWithNoFloorYetKeepsItsKeys(t *testing.T) {
	dir := t.TempDir()
	stack := bootDecisionStack(t, dir)
	first := deployProcess(t, stack.x, plainProcess("alpha"))
	stack.shutdown()

	// Erase the floor, which is the state an installation deployed before it existed
	// comes back in.
	if err := removeKeySpaceMark(dir); err != nil {
		t.Fatalf("remove the floor: %v", err)
	}

	rebooted := bootDecisionStack(t, dir)
	defer rebooted.shutdown()
	if second := deployProcess(t, rebooted.x, plainProcess("beta")); second <= first {
		t.Fatalf("the next definition got key %d with %d already deployed — the records must still raise the counter", second, first)
	}
}

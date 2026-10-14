package api_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The refusal paths of the definition and draft handlers, end to end against a real
// data directory.
//
// Every write these handlers make goes to a sidecar record first and to the registry
// second (durable before visible, I2 / ADR-0019), and every one of them reads a record
// or two before it decides. What had no test was the answer when one of those disk
// steps fails: the handler has to refuse with the step that failed, and — the part that
// matters — leave nothing changed behind it. Each test below breaks exactly one disk
// step, sends the request, checks the refusal, and then reads the resource back to show
// the refusal was not a half-applied write.
//
// The faults are ones a data directory really takes and that behave the same on every
// platform CI runs: a record cut short (a crash mid-write, a bad restore), a store whose
// directory has gone, and a write blocked by a directory sitting where its temp file has
// to go — the same "a directory where a file is expected" shape, which no OS will open
// for writing. None needs a permission trick.

// handlersCutRecord is a record truncated mid-write.
const handlersCutRecord = `{"id":`

// handlersHexName is the default sidecar filename for a key.
func handlersHexName(key string) string { return hex.EncodeToString([]byte(key)) + ".json" }

// handlersWrite writes one file under the data directory, creating its parents.
func handlersWrite(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// handlersBlockWrite makes the next atomic write of one record fail while the record
// stays readable: its temp file's path is occupied by a directory.
func handlersBlockWrite(t *testing.T, dir, rel string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, rel+".tmp"), 0o755); err != nil {
		t.Fatalf("block write of %s: %v", rel, err)
	}
}

// handlersRefusal asserts a status and a fragment of the error a refusal carries.
func handlersRefusal(t *testing.T, what string, code int, body []byte, wantCode int, wantText string) {
	t.Helper()
	if code != wantCode {
		t.Fatalf("%s: status=%d body=%s, want %d", what, code, body, wantCode)
	}
	if !strings.Contains(string(body), wantText) {
		t.Errorf("%s: body=%s, want it to say %q", what, body, wantText)
	}
}

// handlersProcessKeys lists the deployed definition keys.
func handlersProcessKeys(t *testing.T, ts *httptest.Server) []uint64 {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/processes", "", "")
	if code != http.StatusOK {
		t.Fatalf("list processes: status=%d body=%s", code, body)
	}
	var procs []struct {
		Key    uint64 `json:"key"`
		Active bool   `json:"active"`
	}
	if err := json.Unmarshal(body, &procs); err != nil {
		t.Fatalf("decode processes: %v (%s)", err, body)
	}
	keys := make([]uint64, 0, len(procs))
	for _, p := range procs {
		keys = append(keys, p.Key)
	}
	return keys
}

// handlersDeploy deploys a model and returns the key of its first definition.
func handlersDeploy(t *testing.T, ts *httptest.Server, xml string) uint64 {
	t.Helper()
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", xml, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil || dep.Key == 0 {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	return dep.Key
}

// handlersFailingBody is a request body whose read fails, as a dropped connection's does.
type handlersFailingBody struct{}

func (handlersFailingBody) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

// TestHandlersDiagramSaveThatCannotBeWrittenChangesNothing: the record the adjusted
// layout has to be written into cannot be written, so the save is refused and the
// definition keeps drawing the picture it was deployed with — in the registry, in the
// listing's adjustment stamp, and on disk.
func TestHandlersDiagramSaveThatCannotBeWrittenChangesNothing(t *testing.T) {
	dir := t.TempDir()
	s := boot(t, dir)
	defer s.shutdown()
	handlersDeploy(t, s.ts, diagramBPMN)
	before, err := os.ReadFile(filepath.Join(dir, "deployments", "1.json"))
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	handlersBlockWrite(t, dir, filepath.Join("deployments", "1.json"))

	code, body := doReq(t, s.ts, http.MethodPut, "/api/v1/processes/1/diagram", moved(diagramBPMN), "application/xml")
	handlersRefusal(t, "diagram save", code, body, http.StatusInternalServerError, "persist deployment")

	if got := processXML(t, s.ts, "1"); !strings.Contains(got, `x="240" y="78"`) {
		t.Errorf("a refused save changed the rendered diagram:\n%s", got)
	}
	if _, stamp := listedVersion(t, s.ts, 1); stamp != 0 {
		t.Errorf("a refused save stamped the definition as adjusted at %d", stamp)
	}
	after, err := os.ReadFile(filepath.Join(dir, "deployments", "1.json"))
	if err != nil || string(after) != string(before) {
		t.Errorf("a refused save changed the record on disk (err %v)", err)
	}
}

// TestHandlersDiagramSaveLeavesOtherDefinitionsAlone: a layout belongs to the deploy
// it went out with. Saving one definition's picture walks the registry for the pools
// that share it, and a definition of another model is not one of them.
func TestHandlersDiagramSaveLeavesOtherDefinitionsAlone(t *testing.T) {
	ts := newTestServer(t)
	drawn := handlersDeploy(t, ts, diagramBPMN)
	other := handlersDeploy(t, ts, sampleBPMN)
	otherBefore := processXML(t, ts, strconv.FormatUint(other, 10))

	code, body := doReq(t, ts, http.MethodPut, fmt.Sprintf("/api/v1/processes/%d/diagram", drawn), moved(diagramBPMN), "application/xml")
	if code != http.StatusOK {
		t.Fatalf("save diagram: status=%d body=%s", code, body)
	}
	var saved struct {
		Updated []uint64 `json:"updated"`
	}
	if err := json.Unmarshal(body, &saved); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(saved.Updated) != 1 || saved.Updated[0] != drawn {
		t.Errorf("updated=%v, want only %d", saved.Updated, drawn)
	}
	if got := processXML(t, ts, strconv.FormatUint(other, 10)); got != otherBefore {
		t.Errorf("saving one definition's layout changed another's:\n%s", got)
	}
}

// TestHandlersDiagramSaveSkipsAPoolWhoseRecordIsGone: the registry and the sidecar
// have diverged for one pool of a collaboration. The pool that was addressed is still
// saved; the other is skipped rather than written from what the registry holds, which
// would put a record on disk that no deploy ever made.
func TestHandlersDiagramSaveSkipsAPoolWhoseRecordIsGone(t *testing.T) {
	dir := t.TempDir()
	s := boot(t, dir)
	defer s.shutdown()
	if code, body := doReq(t, s.ts, http.MethodPost, "/api/v1/deployments", collabBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	sibling := filepath.Join(dir, "deployments", "2.json")
	if err := os.Remove(sibling); err != nil {
		t.Fatalf("remove sibling record: %v", err)
	}
	adjusted := bumpFirstY(t, processXML(t, s.ts, "1"))

	code, body := doReq(t, s.ts, http.MethodPut, "/api/v1/processes/1/diagram", adjusted, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("save diagram: status=%d body=%s", code, body)
	}
	var saved struct {
		Updated []uint64 `json:"updated"`
	}
	if err := json.Unmarshal(body, &saved); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(saved.Updated) != 1 || saved.Updated[0] != 1 {
		t.Errorf("updated=%v, want only the pool whose record exists", saved.Updated)
	}
	if _, err := os.Stat(sibling); !os.IsNotExist(err) {
		t.Errorf("the save wrote a record for the pool that had none (stat err %v)", err)
	}
}

// TestHandlersDiagramSaveWithAnUnreadableBody: a body that cannot be read is the
// caller's failure, reported as such, and the definition is untouched.
func TestHandlersDiagramSaveWithAnUnreadableBody(t *testing.T) {
	dir := t.TempDir()
	s := boot(t, dir)
	defer s.shutdown()
	handlersDeploy(t, s.ts, diagramBPMN)

	rec := httptest.NewRecorder()
	s.srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/processes/1/diagram", handlersFailingBody{}))
	handlersRefusal(t, "diagram save", rec.Code, rec.Body.Bytes(), http.StatusBadRequest, "read body")
	if got := processXML(t, s.ts, "1"); !strings.Contains(got, `x="240" y="78"`) {
		t.Errorf("an unreadable request changed the diagram:\n%s", got)
	}
}

// TestHandlersDeleteProcessRefusedWhenItsRecordCannotBeRemoved: a deletion is
// acknowledged only once its record is gone from disk, or it would reappear on the
// next restart. A record that cannot be removed leaves the definition deployed.
func TestHandlersDeleteProcessRefusedWhenItsRecordCannotBeRemoved(t *testing.T) {
	dir := t.TempDir()
	s := boot(t, dir)
	defer s.shutdown()
	handlersDeploy(t, s.ts, diagramBPMN)
	// A non-empty directory in the record's place: removing it fails on every OS.
	record := filepath.Join(dir, "deployments", "1.json")
	if err := os.Remove(record); err != nil {
		t.Fatalf("remove record: %v", err)
	}
	handlersWrite(t, dir, filepath.Join("deployments", "1.json", "keep"), "x")

	code, body := doReq(t, s.ts, http.MethodDelete, "/api/v1/processes/1", "", "")
	handlersRefusal(t, "delete", code, body, http.StatusInternalServerError, "remove deployment")
	if keys := handlersProcessKeys(t, s.ts); len(keys) != 1 || keys[0] != 1 {
		t.Errorf("after a refused delete the listing holds %v, want the definition still there", keys)
	}
}

// TestHandlersDeleteProcessRefusedWhenItsGuardsCannotBeRead: deleting the last version
// of a process first asks whether an order line or a catalogue release can still start
// it (ADR-0427). A store that cannot answer is not a "no": the delete is refused and
// the definition stays.
func TestHandlersDeleteProcessRefusedWhenItsGuardsCannotBeRead(t *testing.T) {
	for _, tc := range []struct {
		name, record, want string
	}{
		{"orders", filepath.Join("orders", handlersHexName("ord-1")), "check orders"},
		{"catalogue", filepath.Join("catalog", "catalogs", handlersHexName("cat-1")), "check instances"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			s := boot(t, dir)
			defer s.shutdown()
			handlersDeploy(t, s.ts, diagramBPMN)
			handlersWrite(t, dir, tc.record, handlersCutRecord)

			code, body := doReq(t, s.ts, http.MethodDelete, "/api/v1/processes/1", "", "")
			handlersRefusal(t, "delete", code, body, http.StatusInternalServerError, tc.want)
			if keys := handlersProcessKeys(t, s.ts); len(keys) != 1 {
				t.Errorf("after a refused delete the listing holds %v, want the definition still there", keys)
			}
			if _, err := os.Stat(filepath.Join(dir, "deployments", "1.json")); err != nil {
				t.Errorf("a refused delete removed the record: %v", err)
			}
		})
	}
}

// TestHandlersSetActiveThatCannotBeWrittenChangesNothing: deactivating persists the
// flag before the engine stops auto-starting the definition (ADR-0119). A flag that
// cannot be persisted is not applied, so the definition still reads as active.
func TestHandlersSetActiveThatCannotBeWrittenChangesNothing(t *testing.T) {
	dir := t.TempDir()
	s := boot(t, dir)
	defer s.shutdown()
	handlersDeploy(t, s.ts, diagramBPMN)
	handlersBlockWrite(t, dir, filepath.Join("deployments", "1.json"))

	code, body := doReq(t, s.ts, http.MethodPut, "/api/v1/processes/1/active", `{"active":false}`, "application/json")
	handlersRefusal(t, "deactivate", code, body, http.StatusInternalServerError, "persist deployment")

	code, body = doReq(t, s.ts, http.MethodGet, "/api/v1/processes", "", "")
	if code != http.StatusOK {
		t.Fatalf("list: status=%d body=%s", code, body)
	}
	if !strings.Contains(string(body), `"active":true`) {
		t.Errorf("a refused deactivation still took effect: %s", body)
	}
}

// TestHandlersDeployRefusedWhenItsDraftCannotBeRead: a deploy without ?projectId= files
// the definition under its matching draft's project, so it reads that draft — and its
// project — first. Either read failing refuses the deploy before anything is registered.
func TestHandlersDeployRefusedWhenItsDraftCannotBeRead(t *testing.T) {
	t.Run("draft", func(t *testing.T) {
		dir := t.TempDir()
		s := boot(t, dir)
		defer s.shutdown()
		handlersWrite(t, dir, filepath.Join("drafts", handlersHexName("drawn")), handlersCutRecord)

		code, body := doReq(t, s.ts, http.MethodPost, "/api/v1/deployments", diagramBPMN, "application/xml")
		handlersRefusal(t, "deploy", code, body, http.StatusInternalServerError, "read draft")
		if keys := handlersProcessKeys(t, s.ts); len(keys) != 0 {
			t.Errorf("a refused deploy registered %v", keys)
		}
	})
	t.Run("project", func(t *testing.T) {
		dir := t.TempDir()
		s := boot(t, dir)
		defer s.shutdown()
		code, body := doReq(t, s.ts, http.MethodPost, "/api/v1/projects", `{"name":"Zahlungen"}`, "application/json")
		if code != http.StatusOK {
			t.Fatalf("create project: status=%d body=%s", code, body)
		}
		var proj struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(body, &proj); err != nil || proj.ID == "" {
			t.Fatalf("decode project: %v (%s)", err, body)
		}
		if code, body := doReq(t, s.ts, http.MethodPost, "/api/v1/drafts?projectId="+proj.ID, diagramBPMN, "application/xml"); code != http.StatusOK {
			t.Fatalf("save draft: status=%d body=%s", code, body)
		}
		handlersWrite(t, dir, filepath.Join("projects", handlersHexName(proj.ID)), handlersCutRecord)

		code, body = doReq(t, s.ts, http.MethodPost, "/api/v1/deployments", diagramBPMN, "application/xml")
		handlersRefusal(t, "deploy", code, body, http.StatusInternalServerError, "read project")
		if keys := handlersProcessKeys(t, s.ts); len(keys) != 0 {
			t.Errorf("a refused deploy registered %v", keys)
		}
	})
}

// TestHandlersDeployRefusedWhenItsKeysCannotBeReserved: a definition key is spent
// durably before any record claims it (ADR-0339), so a key that cannot be reserved
// refuses the deploy outright — and spends nothing: once the store can be written again,
// the next deploy is handed the very key the refused one would have had.
func TestHandlersDeployRefusedWhenItsKeysCannotBeReserved(t *testing.T) {
	dir := t.TempDir()
	s := boot(t, dir)
	defer s.shutdown()
	blocked := filepath.Join("keyspace", handlersHexName("definition-keys"))
	handlersBlockWrite(t, dir, blocked)

	code, body := doReq(t, s.ts, http.MethodPost, "/api/v1/deployments", diagramBPMN, "application/xml")
	handlersRefusal(t, "deploy", code, body, http.StatusInternalServerError, "reserve")
	if keys := handlersProcessKeys(t, s.ts); len(keys) != 0 {
		t.Fatalf("a refused deploy registered %v", keys)
	}

	if err := os.Remove(filepath.Join(dir, blocked+".tmp")); err != nil {
		t.Fatalf("unblock: %v", err)
	}
	if key := handlersDeploy(t, s.ts, diagramBPMN); key != 1 {
		t.Errorf("the deploy after the refusal got key %d, want 1: the refused one spent a key", key)
	}
}

// handlersDraftProject returns the project a listed draft is filed under.
func handlersDraftProject(t *testing.T, ts *httptest.Server, id string) (string, bool) {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/drafts", "", "")
	if code != http.StatusOK {
		t.Fatalf("list drafts: status=%d body=%s", code, body)
	}
	var drafts []struct {
		ProcessID string `json:"processId"`
		ProjectID string `json:"projectId"`
	}
	if err := json.Unmarshal(body, &drafts); err != nil {
		t.Fatalf("decode drafts: %v (%s)", err, body)
	}
	for _, d := range drafts {
		if d.ProcessID == id {
			return d.ProjectID, true
		}
	}
	return "", false
}

// TestHandlersMoveDraftThatCannotBeWrittenStaysWhereItWas: moving a draft into a
// project rewrites its record; a record that cannot be rewritten leaves the draft in
// the place it was.
func TestHandlersMoveDraftThatCannotBeWrittenStaysWhereItWas(t *testing.T) {
	dir := t.TempDir()
	s := boot(t, dir)
	defer s.shutdown()
	if code, body := doReq(t, s.ts, http.MethodPost, "/api/v1/drafts", diagramBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("save draft: status=%d body=%s", code, body)
	}
	code, body := doReq(t, s.ts, http.MethodPost, "/api/v1/projects", `{"name":"Ziel"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create project: status=%d body=%s", code, body)
	}
	var proj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &proj); err != nil || proj.ID == "" {
		t.Fatalf("decode project: %v (%s)", err, body)
	}
	handlersBlockWrite(t, dir, filepath.Join("drafts", handlersHexName("drawn")))

	code, body = doReq(t, s.ts, http.MethodPatch, "/api/v1/drafts/drawn", `{"projectId":"`+proj.ID+`"}`, "application/json")
	handlersRefusal(t, "move", code, body, http.StatusInternalServerError, "move draft")
	if got, ok := handlersDraftProject(t, s.ts, "drawn"); !ok || got != "" {
		t.Errorf("after a refused move the draft is in %q (listed %v), want it still ungrouped", got, ok)
	}
}

// TestHandlersDeleteDraftFromAStoreThatIsGoneIsNotAcknowledged: deleting an absent
// draft succeeds, which is what makes the delete idempotent — but a store whose
// directory has gone cannot make even that durable, and a 204 would say it had.
func TestHandlersDeleteDraftFromAStoreThatIsGoneIsNotAcknowledged(t *testing.T) {
	dir := t.TempDir()
	s := boot(t, dir)
	defer s.shutdown()
	if code, body := doReq(t, s.ts, http.MethodPost, "/api/v1/drafts", diagramBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("save draft: status=%d body=%s", code, body)
	}
	if err := os.RemoveAll(filepath.Join(dir, "drafts")); err != nil {
		t.Fatalf("remove drafts dir: %v", err)
	}
	code, body := doReq(t, s.ts, http.MethodDelete, "/api/v1/drafts/drawn", "", "")
	handlersRefusal(t, "delete draft", code, body, http.StatusInternalServerError, "delete draft")
}

// TestHandlersRenameFromAnUnreadableDraftSavesNothing: a save that renames a draft
// carries the record it came from — its project, its creator — so that record is read
// first. When it cannot be read the rename is refused before anything is written under
// the new id.
func TestHandlersRenameFromAnUnreadableDraftSavesNothing(t *testing.T) {
	dir := t.TempDir()
	s := boot(t, dir)
	defer s.shutdown()
	handlersWrite(t, dir, filepath.Join("drafts", handlersHexName("old")), handlersCutRecord)

	code, body := doReq(t, s.ts, http.MethodPost, "/api/v1/drafts?from=old", diagramBPMN, "application/xml")
	handlersRefusal(t, "rename", code, body, http.StatusInternalServerError, "read draft")
	if code, body := doReq(t, s.ts, http.MethodGet, "/api/v1/drafts/drawn/xml", "", ""); code != http.StatusNotFound {
		t.Errorf("a refused rename still saved the draft under its new id: status=%d body=%s", code, body)
	}
}

// handlersMessageStartBPMN starts on message "go" and parks at a user task, so a
// started instance stays in the listing.
const handlersMessageStartBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <message id="m" name="go"/>
  <process id="on-message" isExecutable="true">
    <startEvent id="s"><messageEventDefinition messageRef="m"/></startEvent>
    <userTask id="t"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </process>
</definitions>`

// handlersInstanceCount counts the instances in the listing.
func handlersInstanceCount(t *testing.T, ts *httptest.Server) int {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	if code != http.StatusOK {
		t.Fatalf("list instances: status=%d body=%s", code, body)
	}
	var rows []instanceRow
	if err := json.Unmarshal(listRows(t, body), &rows); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	return len(rows)
}

// TestHandlersPublishMessageRefusals: a message whose variables are not an object is
// the caller's mistake, and a catalogue that cannot be read cannot say whether the
// name is a product's to deliver (ADR-0428). Neither may start anything; the same
// message sent correctly afterwards starts exactly one instance, which is what shows
// the refusals were not just a message nothing listens for.
func TestHandlersPublishMessageRefusals(t *testing.T) {
	t.Run("variables", func(t *testing.T) {
		ts := newTestServer(t)
		handlersDeploy(t, ts, handlersMessageStartBPMN)
		code, body := doReq(t, ts, http.MethodPost, "/api/v1/messages", `{"name":"go","variables":"not an object"}`, "application/json")
		handlersRefusal(t, "publish", code, body, http.StatusBadRequest, "invalid JSON body")
		if n := handlersInstanceCount(t, ts); n != 0 {
			t.Fatalf("a refused publish started %d instance(s)", n)
		}
		if code, body := doReq(t, ts, http.MethodPost, "/api/v1/messages", `{"name":"go","variables":{"x":1}}`, "application/json"); code != http.StatusOK {
			t.Fatalf("publish: status=%d body=%s", code, body)
		}
		if n := handlersInstanceCount(t, ts); n != 1 {
			t.Errorf("a correct publish started %d instance(s), want 1", n)
		}
	})
	t.Run("catalogue", func(t *testing.T) {
		dir := t.TempDir()
		s := boot(t, dir)
		defer s.shutdown()
		handlersDeploy(t, s.ts, handlersMessageStartBPMN)
		item := filepath.Join(dir, "catalog", "items", handlersHexName("item-1"))
		handlersWrite(t, dir, filepath.Join("catalog", "items", handlersHexName("item-1")), handlersCutRecord)

		code, body := doReq(t, s.ts, http.MethodPost, "/api/v1/messages", `{"name":"go"}`, "application/json")
		handlersRefusal(t, "publish", code, body, http.StatusInternalServerError, "read catalogue")
		if n := handlersInstanceCount(t, s.ts); n != 0 {
			t.Fatalf("a refused publish started %d instance(s)", n)
		}
		if err := os.Remove(item); err != nil {
			t.Fatalf("repair catalogue: %v", err)
		}
		if code, body := doReq(t, s.ts, http.MethodPost, "/api/v1/messages", `{"name":"go"}`, "application/json"); code != http.StatusOK {
			t.Fatalf("publish: status=%d body=%s", code, body)
		}
		if n := handlersInstanceCount(t, s.ts); n != 1 {
			t.Errorf("a correct publish started %d instance(s), want 1", n)
		}
	})
}

// handlersParkedJob deploys sampleBPMN, starts one instance and returns the key of the
// job it parks on.
func handlersParkedJob(t *testing.T, ts *httptest.Server) (instKey, jobKey uint64) {
	t.Helper()
	defKey := handlersDeploy(t, ts, sampleBPMN)
	code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", defKey), "{}", "application/json")
	if code != http.StatusOK {
		t.Fatalf("start: status=%d body=%s", code, body)
	}
	var started struct {
		InstanceKey uint64 `json:"instanceKey"`
	}
	if err := json.Unmarshal(body, &started); err != nil || started.InstanceKey == 0 {
		t.Fatalf("decode start: %v (%s)", err, body)
	}
	jobs := handlersInstanceJobs(t, ts, started.InstanceKey)
	if len(jobs) != 1 {
		t.Fatalf("instance parks on %d jobs, want 1", len(jobs))
	}
	return started.InstanceKey, jobs[0]
}

// handlersInstanceJobs lists the keys of the activatable jobs an instance parks on.
func handlersInstanceJobs(t *testing.T, ts *httptest.Server, instKey uint64) []uint64 {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/jobs", instKey), "", "")
	if code != http.StatusOK {
		t.Fatalf("list jobs: status=%d body=%s", code, body)
	}
	var jobs []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &jobs); err != nil {
		t.Fatalf("decode jobs: %v (%s)", err, body)
	}
	keys := make([]uint64, 0, len(jobs))
	for _, j := range jobs {
		keys = append(keys, j.Key)
	}
	return keys
}

// TestHandlersCompleteJobRefusesAMalformedBody: a completion is parsed three ways
// before the job is looked up — its variables, an agent's tool calls, and the rest of
// its fields — and a body any of them cannot read is a 400 that completes nothing.
func TestHandlersCompleteJobRefusesAMalformedBody(t *testing.T) {
	ts := newTestServer(t)
	instKey, jobKey := handlersParkedJob(t, ts)
	path := fmt.Sprintf("/api/v1/jobs/%d/complete", jobKey)
	for _, body := range []string{
		`{"reason":"by hand","toolCalls":"not a list"}`, // valid variables, unreadable tool calls
		`{"reason":42}`, // valid variables and tool calls, a reason that is not text
	} {
		code, got := doReq(t, ts, http.MethodPost, path, body, "application/json")
		handlersRefusal(t, "complete "+body, code, got, http.StatusBadRequest, "invalid JSON body")
	}
	if jobs := handlersInstanceJobs(t, ts, instKey); len(jobs) != 1 || jobs[0] != jobKey {
		t.Errorf("after refused completions the instance parks on %v, want job %d still open", jobs, jobKey)
	}
}

// TestHandlersActivateJobRefusesAKeyThatIsNotANumber: the single-job lease addresses a
// job by its numeric key.
func TestHandlersActivateJobRefusesAKeyThatIsNotANumber(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/jobs/not-a-key/activate", "", "")
	handlersRefusal(t, "activate", code, body, http.StatusBadRequest, "invalid job key")
}

// handlersFanOutBPMN parks one instance on 101 jobs of type "bulk" — one more than a
// single pull may take.
const handlersFanOutBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="fan-out" isExecutable="true">
    <startEvent id="s"/>
    <serviceTask id="t">
      <extensionElements><zeebe:taskDefinition type="bulk"/></extensionElements>
      <multiInstanceLoopCharacteristics isSequential="false"><loopCardinality>101</loopCardinality></multiInstanceLoopCharacteristics>
    </serviceTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </process>
</definitions>`

// handlersPull leases jobs of one type and returns how many came back.
func handlersPull(t *testing.T, h http.Handler, ctx context.Context, body string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/jobs/activate", strings.NewReader(body)).WithContext(ctx))
	if rec.Code != http.StatusOK {
		t.Fatalf("pull %s: status=%d body=%s", body, rec.Code, rec.Body.String())
	}
	var out struct {
		Jobs []json.RawMessage `json:"jobs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Jobs == nil {
		t.Fatalf("decode pull: %v (%s)", err, rec.Body.String())
	}
	return len(out.Jobs)
}

// TestHandlersAPullIsCappedNotRefused: a worker asking for more than a pull may take
// gets the most a pull hands out — not a 400, and not the whole backlog — and the
// remainder waits for its next pull.
func TestHandlersAPullIsCappedNotRefused(t *testing.T) {
	dir := t.TempDir()
	s := boot(t, dir)
	defer s.shutdown()
	defKey := handlersDeploy(t, s.ts, handlersFanOutBPMN)
	if code, body := doReq(t, s.ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", defKey), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("start: status=%d body=%s", code, body)
	}
	h := s.srv.Handler()
	if n := handlersPull(t, h, context.Background(), `{"type":"bulk","worker":"w1","maxJobs":100000}`); n != 100 {
		t.Errorf("first pull leased %d jobs, want the cap of 100", n)
	}
	if n := handlersPull(t, h, context.Background(), `{"type":"bulk","worker":"w1","maxJobs":100000}`); n != 1 {
		t.Errorf("second pull leased %d jobs, want the one left", n)
	}
}

// TestHandlersALongPollPastTheMaximumEndsWithItsCaller: a wait longer than the
// maximum is shortened rather than refused, and however long the wait, a caller that
// has gone away ends the poll — it answers empty at once instead of holding the
// request for the rest of the wait.
func TestHandlersALongPollPastTheMaximumEndsWithItsCaller(t *testing.T) {
	dir := t.TempDir()
	s := boot(t, dir)
	defer s.shutdown()
	handlersDeploy(t, s.ts, sampleBPMN) // registers the job type; no instance, so no job

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if n := handlersPull(t, s.srv.Handler(), ctx, `{"type":"payment","worker":"w1","waitMs":86400000}`); n != 0 {
		t.Errorf("an idle long poll leased %d jobs", n)
	}
}

// TestHandlersTerminateByFilterTouchesOnlyItsDefinition: a bulk terminate names one
// definition, and an instance of any other definition is not part of the batch — even
// with a limit far over the per-call maximum, which is shortened rather than refused.
func TestHandlersTerminateByFilterTouchesOnlyItsDefinition(t *testing.T) {
	ts := newTestServer(t)
	target := handlersDeploy(t, ts, sampleBPMN)
	other := handlersDeploy(t, ts, diagramBPMN)
	for _, def := range []uint64{target, other} {
		if code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", def), "{}", "application/json"); code != http.StatusOK {
			t.Fatalf("start: status=%d body=%s", code, body)
		}
	}
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/instances/terminate",
		fmt.Sprintf(`{"processDefKey":%d,"limit":1000000}`, target), "application/json")
	if code != http.StatusOK {
		t.Fatalf("terminate: status=%d body=%s", code, body)
	}
	var resp struct {
		Terminated int `json:"terminated"`
	}
	if err := json.Unmarshal(body, &resp); err != nil || resp.Terminated != 1 {
		t.Fatalf("terminate reported %s (err %v), want one terminated", body, err)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/instances?state=active", "", "")
	if code != http.StatusOK {
		t.Fatalf("list: status=%d body=%s", code, body)
	}
	var rows []instanceRow
	if err := json.Unmarshal(listRows(t, body), &rows); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(rows) != 1 || rows[0].ProcessDefKey != other {
		t.Errorf("active after terminate = %+v, want only the other definition's instance", rows)
	}
}

// TestHandlersModelDifferenceRefusedWhenTheModelCannotBeRead: the difference compares
// an application's processes with its information model, so a model store that cannot
// be read has no difference to report — a 500, not an empty difference that would read
// as "everything matches".
func TestHandlersModelDifferenceRefusedWhenTheModelCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	s := boot(t, dir)
	defer s.shutdown()
	handlersWrite(t, dir, filepath.Join("information-models", "10.json"), handlersCutRecord)
	code, body := doReq(t, s.ts, http.MethodGet, "/api/v1/infomodel/difference?applicationId=app-1", "", "")
	handlersRefusal(t, "difference", code, body, http.StatusInternalServerError, "information model")
}

// TestHandlersCollaborationRuntimeListsOnlyItsOwnPools: a collaboration's pools are
// found by the XML they share, so a definition deployed from another model — even one
// deployed first — is not drawn as one of its pools.
func TestHandlersCollaborationRuntimeListsOnlyItsOwnPools(t *testing.T) {
	ts := newTestServer(t)
	standalone := handlersDeploy(t, ts, sampleBPMN)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", collabBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy collaboration: status=%d body=%s", code, body)
	}
	var dep collabDeployResp
	if err := json.Unmarshal(body, &dep); err != nil || len(dep.Deployments) != 2 {
		t.Fatalf("decode collaboration: %v (%s)", err, body)
	}
	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/collaborations/%d/runtime", dep.Deployments[0].Key), "", "")
	if code != http.StatusOK {
		t.Fatalf("runtime: status=%d body=%s", code, body)
	}
	var rt struct {
		Pools []struct {
			Key uint64 `json:"key"`
		} `json:"pools"`
	}
	if err := json.Unmarshal(body, &rt); err != nil {
		t.Fatalf("decode runtime: %v (%s)", err, body)
	}
	if len(rt.Pools) != 2 {
		t.Fatalf("pools = %+v, want the collaboration's two", rt.Pools)
	}
	for _, p := range rt.Pools {
		if p.Key == standalone {
			t.Errorf("the standalone definition %d is drawn as a pool of the collaboration", standalone)
		}
	}
}

// TestHandlersReplayOfAMigrationFromADeletedVersion: once an instance has migrated off
// a version, that version may be deleted — nothing runs on it any more. Its replay
// still answers, and the migration step still names the definition it left by key; what
// it cannot name any more is that definition's version, and it says so with a zero
// rather than borrowing a version number from somewhere else.
func TestHandlersReplayOfAMigrationFromADeletedVersion(t *testing.T) {
	ts := newTestServer(t)
	v1 := deployXML(t, ts, migrateV1BPMN)
	key := startInstance(t, ts, v1)
	v2 := deployXML(t, ts, migrateV2BPMN)
	if code, _, raw := migrateCall(t, ts, fmt.Sprintf("/api/v1/instances/%d/migrate", key),
		fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"model fix"}`, v2)); code != http.StatusOK {
		t.Fatalf("migrate: status=%d body=%s", code, raw)
	}
	if code, body := doReq(t, ts, http.MethodDelete, fmt.Sprintf("/api/v1/processes/%d", v1), "", ""); code != http.StatusNoContent {
		t.Fatalf("delete the version the instance left: status=%d body=%s", code, body)
	}

	tl := readTimeline(t, ts, key)
	var blocks int
	for _, s := range tl.Steps {
		if s.Action != "migrate" {
			continue
		}
		blocks++
		m := s.Migration
		if m == nil {
			t.Fatal(`a step with action "migrate" carries no migration block`)
		}
		if m.FromProcessDefKey != v1 || m.FromVersion != 0 {
			t.Errorf("migration from %d v%d, want %d with no version: it has been deleted", m.FromProcessDefKey, m.FromVersion, v1)
		}
		if m.ToProcessDefKey != v2 || m.ToVersion != 2 {
			t.Errorf("migration to %d v%d, want %d v2", m.ToProcessDefKey, m.ToVersion, v2)
		}
	}
	if blocks != 1 {
		t.Errorf("timeline carries %d migration blocks, want 1", blocks)
	}
}

// handlersWriterV1BPMN writes an order at "write" and parks at "wait". In v2 a task is
// added in front of "write", so element index 1 — "write" in v1 — is "triage" in v2.
const handlersWriterV1BPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <itemDefinition id="ItemDefinition_Order" structureRef="Order"/>
  <process id="writer" isExecutable="true">
    <dataObject id="DO_order" name="order" itemSubjectRef="ItemDefinition_Order"/>
    <dataObjectReference id="Ref_order" name="order" dataObjectRef="DO_order"/>
    <startEvent id="start"/>
    <task id="write">
      <dataOutputAssociation id="d1"><targetRef>Ref_order</targetRef><assignment><from>= 42</from></assignment></dataOutputAssociation>
    </task>
    <userTask id="wait"/>
    <endEvent id="done"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="write"/>
    <sequenceFlow id="f2" sourceRef="write" targetRef="wait"/>
    <sequenceFlow id="f3" sourceRef="wait" targetRef="done"/>
  </process>
</definitions>`

const handlersWriterV2BPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <itemDefinition id="ItemDefinition_Order" structureRef="Order"/>
  <process id="writer" isExecutable="true">
    <dataObject id="DO_order" name="order" itemSubjectRef="ItemDefinition_Order"/>
    <dataObjectReference id="Ref_order" name="order" dataObjectRef="DO_order"/>
    <startEvent id="start"/>
    <userTask id="triage"/>
    <task id="write">
      <dataOutputAssociation id="d1"><targetRef>Ref_order</targetRef><assignment><from>= 42</from></assignment></dataOutputAssociation>
    </task>
    <userTask id="wait"/>
    <endEvent id="done"/>
    <sequenceFlow id="f0" sourceRef="start" targetRef="triage"/>
    <sequenceFlow id="f1" sourceRef="triage" targetRef="write"/>
    <sequenceFlow id="f2" sourceRef="write" targetRef="wait"/>
    <sequenceFlow id="f3" sourceRef="wait" targetRef="done"/>
  </process>
</definitions>`

// TestHandlersDataTabAttributesAWriteUnderTheVersionItRanOn: a write is attributed by
// the element instance that made it, and that element's index means whatever the
// definition in force at the write's log position says (ADR-0162). After a migration
// the instance is on a version where the same index is a different task; the Data tab
// must still name the element that actually wrote the value.
func TestHandlersDataTabAttributesAWriteUnderTheVersionItRanOn(t *testing.T) {
	ts := newTestServer(t)
	v1 := deployXML(t, ts, handlersWriterV1BPMN)
	key := startInstance(t, ts, v1)
	v2 := deployXML(t, ts, handlersWriterV2BPMN)
	if code, _, raw := migrateCall(t, ts, fmt.Sprintf("/api/v1/instances/%d/migrate", key),
		fmt.Sprintf(`{"targetProcessDefKey":%d,"reason":"triage first"}`, v2)); code != http.StatusOK {
		t.Fatalf("migrate: status=%d body=%s", code, raw)
	}

	code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/data-objects", key), "", "")
	if code != http.StatusOK {
		t.Fatalf("data objects: status=%d body=%s", code, body)
	}
	var objects []struct {
		Name       string `json:"name"`
		ProducedBy string `json:"producedBy"`
		History    []struct {
			ProducedBy string `json:"producedBy"`
		} `json:"history"`
	}
	if err := json.Unmarshal(body, &objects); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(objects) != 1 || objects[0].Name != "order" {
		t.Fatalf("objects = %s, want the order", body)
	}
	o := objects[0]
	if o.ProducedBy != "write" {
		t.Errorf("the order is attributed to %q, want %q — the element that wrote it on v1", o.ProducedBy, "write")
	}
	for _, h := range o.History {
		if h.ProducedBy == "triage" {
			t.Error(`a write is attributed to "triage", a v2 task this instance never ran: ` +
				"the write's element index is being read through the version the instance is on now")
		}
	}
}

// handlersDeployedVersion deploys a model and returns the version it was given.
func handlersDeployedVersion(t *testing.T, ts *httptest.Server, xml string) int32 {
	t.Helper()
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", xml, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var dep struct {
		Version int32 `json:"version"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	return dep.Version
}

// TestHandlersDeployThatCannotRecordItsJobTypeLeavesNothingBehind is the regression
// test for a deploy refused while interning a new job type. Interning is a durable write
// of its own (ADR-0157), and it used to happen after the deployment record was saved and
// its version counted: the caller got a 500, and the record it left on disk brought the
// "failed" definition back on the next restart, with its version already spent.
//
// The job-type directory going missing is the fault: the registry cannot write the
// reservation for a type it has not seen ("payment"), and nothing else is disturbed.
func TestHandlersDeployThatCannotRecordItsJobTypeLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	jobTypes := filepath.Join(dir, "jobtypes")

	first := boot(t, dir)
	if err := os.RemoveAll(jobTypes); err != nil {
		first.shutdown()
		t.Fatalf("remove job-type directory: %v", err)
	}
	code, body := doReq(t, first.ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml")
	if code != http.StatusInternalServerError || !strings.Contains(string(body), "job type") {
		first.shutdown()
		t.Fatalf("deploy with no job-type directory: status=%d body=%s, want 500 naming the job type", code, body)
	}
	first.shutdown()

	// A restart over the same data directory (which recreates the job-type directory)
	// must not find the definition the caller was told had failed.
	second := boot(t, dir)
	defer second.shutdown()
	if keys := handlersProcessKeys(t, second.ts); len(keys) != 0 {
		t.Fatalf("after a restart the refused deploy is listed as deployed: %v", keys)
	}

	// The same failure on a running server spends no version: once the directory is
	// back, the retry is the process's first version.
	if err := os.RemoveAll(jobTypes); err != nil {
		t.Fatalf("remove job-type directory: %v", err)
	}
	if code, body := doReq(t, second.ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml"); code != http.StatusInternalServerError {
		t.Fatalf("deploy with no job-type directory: status=%d body=%s, want 500", code, body)
	}
	if err := os.MkdirAll(jobTypes, 0o755); err != nil {
		t.Fatalf("restore job-type directory: %v", err)
	}
	if v := handlersDeployedVersion(t, second.ts, sampleBPMN); v != 1 {
		t.Errorf("the retry deployed as version %d, want 1: the refused deploy spent a version", v)
	}
}

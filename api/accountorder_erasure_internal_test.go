package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/vault"
	"github.com/pblumer/atlas/model"
)

// An erasure has to reach an order that has ended, not only one still running. The
// account order example (examples/account-bestellung) enciphers first and last name under
// the key of the personnel number (ADR-0314), and the ordinary request to be forgotten
// comes after the order is over — rejected, or the account long provisioned. A completed
// instance keeps its variables, and so do the WAL, every checkpoint and every backup made
// from them; destroying the key is what makes all of those copies unreadable at once.
//
// These tests file the example's own files the way its README does and run its rejection
// path, which ends the instance without the create-user task and so needs no tenant.

// fileAccountOrderExample uploads the decision model, registers the decision, saves both
// forms and deploys the process, and returns the definition key.
func fileAccountOrderExample(t *testing.T, srv *Server) uint64 {
	t.Helper()
	dir := filepath.Join("..", "examples", "account-bestellung")
	read := func(name string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(raw)
	}
	if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/dmn-models?handle=kontotyp", read("kontotyp.dmn"), "application/xml"); code != http.StatusOK {
		t.Fatalf("upload decision model: status=%d body=%s", code, body)
	}
	if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/dmnrefs", `{"name":"KontotypMapping","modelRef":"kontotyp"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("register decision: status=%d body=%s", code, body)
	}
	for id, file := range map[string]string{"account-order": "form-account-order.json", "account-freigabe": "form-account-freigabe.json"} {
		form := fmt.Sprintf(`{"id":%q,"name":%q,"schema":%s}`, id, id, read(file))
		if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/forms", form, "application/json"); code != http.StatusOK {
			t.Fatalf("save form %s: status=%d body=%s", id, code, body)
		}
	}
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", read("account-bestellung.bpmn"), "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	return deploy.Key
}

// orderAndReject places one order for personalnummer and has the approver reject it, and
// returns the instance, which has then ended.
func orderAndReject(t *testing.T, srv *Server, defKey uint64, personalnummer, vorname, nachname string) uint64 {
	t.Helper()
	start := fmt.Sprintf(`{"variables":{"personalnummer":%q,"vorname":%q,"nachname":%q,"kontotyp":"T","begruendung":"Testkonto"}}`,
		personalnummer, vorname, nachname)
	if code, body := serveInternal(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", defKey), start, "application/json"); code != http.StatusOK {
		t.Fatalf("order for %s: status=%d body=%s", personalnummer, code, body)
	}
	taskKey, instKey := openUserTask(t, srv)
	reject := `{"variables":{"entscheidung":"ablehnen","ablehnungsgrund":"kein Bedarf"}}`
	if code, body := serveInternal(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/complete", taskKey), reject, "application/json"); code != http.StatusOK {
		t.Fatalf("reject the order for %s: status=%d body=%s", personalnummer, code, body)
	}
	var live, completed bool
	srv.do(func() {
		_, live, _ = srv.store.ActiveProcessInstance(instKey)
		_ = srv.store.CompletedProcessInstances(func(k uint64, _ *model.ProcessInstanceValue) error {
			completed = completed || k == instKey
			return nil
		})
	})
	if live || !completed {
		t.Fatalf("the rejected order for %s: running=%v completed=%v, want it ended", personalnummer, live, completed)
	}
	return instKey
}

// instanceNames reads an instance's variables through the API, the door that opens
// personal values for whoever may read the instance, and returns the status, the body and
// the two names when they came back.
func instanceNames(t *testing.T, srv *Server, instKey uint64) (int, []byte, string, string) {
	t.Helper()
	code, body := serveInternal(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/variables", instKey), "", "")
	if code != http.StatusOK {
		return code, body, "", ""
	}
	var vars map[string]any
	if err := json.Unmarshal(body, &vars); err != nil {
		t.Fatalf("decode variables: %v (%s)", err, body)
	}
	vorname, _ := vars["vorname"].(string)
	nachname, _ := vars["nachname"].(string)
	return code, body, vorname, nachname
}

// TestAnErasureReachesACompletedAccountOrder: two orders, both rejected and so both over,
// for two people. Before the erasure both read back in the clear through the API and are
// stored only as envelopes. Erasing the first person makes their order unreadable — the
// API answers that the data was erased instead of handing out ciphertext or names — while
// the stored bytes stay exactly as they were, because the engine's history is facts and
// the erasure destroys a key, not a record (I6). The order's record without the names
// survives, and the other person's order is untouched.
func TestAnErasureReachesACompletedAccountOrder(t *testing.T) {
	srv := newServerForErrors(t)
	defKey := fileAccountOrderExample(t, srv)
	erased := orderAndReject(t, srv, defKey, "P-1001", "Anna", "Muster")
	kept := orderAndReject(t, srv, defKey, "P-1002", "Beat", "Beispiel")

	stored := map[string]model.VariableValue{}
	for _, name := range []string{"vorname", "nachname"} {
		v := rootVar(t, srv, erased, name)
		if !vault.IsEnciphered(v.Text) || strings.Contains(v.Text, "Anna") || strings.Contains(v.Text, "Muster") {
			t.Fatalf("the completed order stores %s as %q, want an envelope without the name", name, v.Text)
		}
		stored[name] = v
	}
	if code, body, vorname, nachname := instanceNames(t, srv, erased); code != http.StatusOK || vorname != "Anna" || nachname != "Muster" {
		t.Fatalf("before the erasure the completed order read back as status=%d %q %q (%s), want Anna Muster", code, vorname, nachname, body)
	}

	if code, body := serveInternal(t, srv, http.MethodDelete, "/api/v1/personal-data/P-1001", "", ""); code != http.StatusOK {
		t.Fatalf("erase P-1001: status=%d body=%s", code, body)
	}

	code, body, _, _ := instanceNames(t, srv, erased)
	if code != http.StatusConflict || !strings.Contains(string(body), "erased") {
		t.Errorf("reading the erased completed order: status=%d body=%s, want 409 saying erased", code, body)
	}
	if strings.Contains(string(body), "Anna") || strings.Contains(string(body), "Muster") {
		t.Errorf("the answer for the erased order carries the name: %s", body)
	}
	for name, before := range stored {
		if after := rootVar(t, srv, erased, name); after.Text != before.Text || after.Kind != before.Kind {
			t.Errorf("the erasure rewrote the stored %s: %q → %q", name, before.Text, after.Text)
		}
	}

	// The record of the order survives the erasure: the timeline, which labels a personal
	// value instead of opening it, still says who the order was for, by id, and that it
	// was rejected (ADR-0314, "the evidence survives it").
	code, body = serveInternal(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", erased), "", "")
	if code != http.StatusOK {
		t.Fatalf("the erased order's timeline: status=%d body=%s", code, body)
	}
	for _, want := range []string{`"P-1001"`, `"ablehnen"`, "enciphered for data subject P-1001"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the erased order's timeline lost %s: %s", want, body)
		}
	}
	if strings.Contains(string(body), "Anna") || strings.Contains(string(body), "Muster") {
		t.Errorf("the erased order's timeline carries the name: %s", body)
	}

	if code, body, vorname, nachname := instanceNames(t, srv, kept); code != http.StatusOK || vorname != "Beat" || nachname != "Beispiel" {
		t.Errorf("the other person's completed order after the erasure: status=%d %q %q (%s), want Beat Beispiel", code, vorname, nachname, body)
	}
}

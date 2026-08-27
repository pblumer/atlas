package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// Who owns a connector, and who may touch it (ADR-0205, measure M11).
//
// Before this, every authenticated account could list every connector with its
// endpoint and sender mailbox, edit one, delete somebody else's, and add an inbound
// subscription to it. These tests are the record of that being over, and each one
// is a line from the concept's acceptance list.
//
// The one that is easy to lose sight of is the last: a *deployed process* must keep
// resolving a connector regardless of who started it. Execution is not authoring
// (ADR-0071), and a sharing rule that reached the runtime would break every model
// the moment its author went on holiday.

// connectorPayload is the smallest valid create body: a clio connector needs a name
// and an endpoint, and nothing here talks to the network.
func connectorPayload(name string) string {
	return `{"name":"` + name + `","kind":"clio","endpoint":"https://clio.example.com","enabled":true}`
}

// createConnector posts a connector and returns its id.
func createConnector(t *testing.T, c *http.Client, base, name string) string {
	t.Helper()
	resp, err := c.Post(base+"/api/v1/connectors", "application/json", strings.NewReader(connectorPayload(name)))
	if err != nil {
		t.Fatalf("create connector: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("create connector = %d: %s", resp.StatusCode, data)
	}
	var out struct{ ID string }
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode connector: %v", err)
	}
	if out.ID == "" {
		t.Fatal("create returned no id")
	}
	return out.ID
}

// listConnectors returns what one caller sees, keyed by id.
func listConnectors(t *testing.T, c *http.Client, base string) map[string]map[string]any {
	t.Helper()
	resp, err := c.Get(base + "/api/v1/connectors")
	if err != nil {
		t.Fatalf("list connectors: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("list connectors = %d: %s", resp.StatusCode, data)
	}
	var out []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	byID := map[string]map[string]any{}
	for _, c := range out {
		id, _ := c["id"].(string)
		byID[id] = c
	}
	return byID
}

// statusOf performs a request and reports only its status, for the many "may they
// or may they not" assertions below.
func statusOf(t *testing.T, c *http.Client, method, url, body string) int {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, url, err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// shareConnector grants a principal a role on a connector.
func shareConnector(t *testing.T, c *http.Client, base, connID, principalID, refType, role string, want int) {
	t.Helper()
	body := `{"role":"` + role + `","type":"` + refType + `"}`
	if got := statusOf(t, c, http.MethodPut, base+"/api/v1/connectors/"+connID+"/members/"+principalID, body); got != want {
		t.Fatalf("share connector = %d, want %d", got, want)
	}
}

// TestAConnectorBelongsToWhoeverMadeIt is the whole measure in one test: the five
// things an ordinary account could do to a stranger's connector, and cannot now.
func TestAConnectorBelongsToWhoeverMadeIt(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "anna")
	createUser(t, admin, ts.URL, "bert")
	anna := signInAs(t, ts.URL, "anna", "a-password-that-is-long")
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")

	id := createConnector(t, anna, ts.URL, "annas-postfach")

	t.Run("its owner keeps it", func(t *testing.T) {
		seen := listConnectors(t, anna, ts.URL)
		if seen[id] == nil {
			t.Fatal("the owner cannot see her own connector")
		}
		if seen[id]["endpoint"] != "https://clio.example.com" {
			t.Errorf("the owner does not see the endpoint: %v", seen[id])
		}
		if got := statusOf(t, anna, http.MethodPatch, ts.URL+"/api/v1/connectors/"+id, `{"enabled":false}`); got != http.StatusOK {
			t.Errorf("owner PATCH = %d, want 200", got)
		}
	})

	t.Run("a stranger sees no configuration", func(t *testing.T) {
		seen := listConnectors(t, bert, ts.URL)
		// The name and kind stay visible — a modeller has to be able to author a
		// service task against a connector that exists (see the catalog test below).
		// What must not travel is the configuration.
		if c := seen[id]; c != nil {
			for _, field := range []string{"endpoint", "sender", "credentialsRef", "ownerId", "members"} {
				if _, ok := c[field]; ok {
					t.Errorf("a stranger sees %q on somebody else's connector: %v", field, c)
				}
			}
		}
	})

	t.Run("a stranger cannot change it", func(t *testing.T) {
		if got := statusOf(t, bert, http.MethodPatch, ts.URL+"/api/v1/connectors/"+id, `{"enabled":false}`); got == http.StatusOK {
			t.Error("a stranger edited somebody else's connector")
		}
	})

	t.Run("a stranger cannot delete it", func(t *testing.T) {
		if got := statusOf(t, bert, http.MethodDelete, ts.URL+"/api/v1/connectors/"+id+"?force=true", ""); got == http.StatusNoContent {
			t.Error("a stranger deleted somebody else's connector — the reported hole")
		}
		if listConnectors(t, anna, ts.URL)[id] == nil {
			t.Error("the connector is gone after a stranger's delete attempt")
		}
	})

	t.Run("a stranger cannot read its subscriptions", func(t *testing.T) {
		if got := statusOf(t, bert, http.MethodGet, ts.URL+"/api/v1/connectors/"+id+"/inbound-subscriptions", ""); got == http.StatusOK {
			t.Error("a stranger read the inbound subscriptions — that is every message name")
		}
	})

	t.Run("a stranger cannot subscribe on it", func(t *testing.T) {
		body := `{"watchedSubject":"mail/inbox","messageName":"stolen","enabled":true}`
		if got := statusOf(t, bert, http.MethodPost, ts.URL+"/api/v1/connectors/"+id+"/inbound-subscriptions", body); got == http.StatusOK {
			t.Error("a stranger pointed somebody else's connector at a message name of their choosing")
		}
	})

	t.Run("an administrator still sees everything", func(t *testing.T) {
		seen := listConnectors(t, admin, ts.URL)
		if seen[id] == nil || seen[id]["endpoint"] == nil {
			t.Error("an administrator cannot see a connector — that is what admin means here")
		}
	})
}

// TestEveryoneCanStillAuthorAgainstAConnector is the counterweight, and the reason
// the listing has two levels at all.
//
// The modeler fills its connector picker from GET /api/v1/connectors and needs a
// name, a kind and whether it is enabled. Scoping that away would leave every
// non-owner authoring against an empty dropdown — a sharing rule that stops people
// doing their work is not a sharing rule, it is an outage.
func TestEveryoneCanStillAuthorAgainstAConnector(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "anna")
	createUser(t, admin, ts.URL, "bert")
	anna := signInAs(t, ts.URL, "anna", "a-password-that-is-long")
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")

	id := createConnector(t, anna, ts.URL, "haus-clio")

	seen := listConnectors(t, bert, ts.URL)[id]
	if seen == nil {
		t.Fatal("a connector is invisible to a modeller, who then cannot author against it")
	}
	if seen["name"] != "haus-clio" || seen["kind"] != "clio" {
		t.Errorf("the catalog entry lacks what the picker needs: %v", seen)
	}
	if seen["enabled"] != true {
		t.Errorf("the catalog entry does not say whether it is enabled: %v", seen)
	}
}

// TestSharingAConnectorFollowsTheRoles: viewer reads, editor writes, and only the
// owner shares or deletes. The same three roles a project has (ADR-0071), because
// a second vocabulary would be a second thing to get wrong.
func TestSharingAConnectorFollowsTheRoles(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "anna")
	bertID := createUser(t, admin, ts.URL, "bert")
	anna := signInAs(t, ts.URL, "anna", "a-password-that-is-long")
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")

	id := createConnector(t, anna, ts.URL, "geteiltes-postfach")

	t.Run("viewer reads and does not write", func(t *testing.T) {
		shareConnector(t, anna, ts.URL, id, bertID, "user", "viewer", http.StatusOK)
		if seen := listConnectors(t, bert, ts.URL)[id]; seen == nil || seen["endpoint"] == nil {
			t.Errorf("a viewer cannot see the configuration: %v", seen)
		}
		if got := statusOf(t, bert, http.MethodGet, ts.URL+"/api/v1/connectors/"+id+"/inbound-subscriptions", ""); got != http.StatusOK {
			t.Errorf("a viewer cannot list subscriptions: %d", got)
		}
		if got := statusOf(t, bert, http.MethodPatch, ts.URL+"/api/v1/connectors/"+id, `{"enabled":false}`); got == http.StatusOK {
			t.Error("a viewer changed the connector")
		}
	})

	t.Run("editor writes and does not share or delete", func(t *testing.T) {
		shareConnector(t, anna, ts.URL, id, bertID, "user", "editor", http.StatusOK)
		if got := statusOf(t, bert, http.MethodPatch, ts.URL+"/api/v1/connectors/"+id, `{"enabled":true}`); got != http.StatusOK {
			t.Errorf("an editor cannot change the connector: %d", got)
		}
		body := `{"watchedSubject":"mail/inbox","messageName":"geteilt","enabled":true}`
		if got := statusOf(t, bert, http.MethodPost, ts.URL+"/api/v1/connectors/"+id+"/inbound-subscriptions", body); got != http.StatusOK {
			t.Errorf("an editor cannot subscribe: %d", got)
		}
		if got := statusOf(t, bert, http.MethodPut, ts.URL+"/api/v1/connectors/"+id+"/members/"+bertID, `{"role":"owner"}`); got == http.StatusOK {
			t.Error("an editor granted themselves a role — sharing is the owner's")
		}
		if got := statusOf(t, bert, http.MethodDelete, ts.URL+"/api/v1/connectors/"+id+"?force=true", ""); got == http.StatusNoContent {
			t.Error("an editor deleted the connector — deleting is the owner's")
		}
	})

	t.Run("withdrawing the share ends the access", func(t *testing.T) {
		if got := statusOf(t, anna, http.MethodDelete, ts.URL+"/api/v1/connectors/"+id+"/members/"+bertID, ""); got != http.StatusOK {
			t.Fatalf("unshare = %d, want 200", got)
		}
		if seen := listConnectors(t, bert, ts.URL)[id]; seen != nil && seen["endpoint"] != nil {
			t.Error("the configuration is still visible after the share was withdrawn")
		}
	})
}

// TestSharingAConnectorWithAGroup: the answer to "and maybe my wife too" is the one
// ADR-0180 already built. A group grant reaches every member, and it ends when the
// membership does.
func TestSharingAConnectorWithAGroup(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "anna")
	bertID := createUser(t, admin, ts.URL, "bert")
	anna := signInAs(t, ts.URL, "anna", "a-password-that-is-long")

	id := createConnector(t, anna, ts.URL, "familienpostfach")
	groupID := createGroup(t, admin, ts.URL, "haushalt")
	putJSON(t, admin, ts.URL+"/api/v1/groups/"+groupID+"/members/"+bertID, http.StatusOK)
	shareConnector(t, anna, ts.URL, id, groupID, "group", "viewer", http.StatusOK)

	// A group grant lands on the next sign-in, like every other group change.
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")
	if seen := listConnectors(t, bert, ts.URL)[id]; seen == nil || seen["endpoint"] == nil {
		t.Fatalf("a group member cannot see the connector: %v", seen)
	}

	deleteAt(t, admin, ts.URL+"/api/v1/groups/"+groupID+"/members/"+bertID, http.StatusOK)
	bert = signInAs(t, ts.URL, "bert", "a-password-that-is-long")
	if seen := listConnectors(t, bert, ts.URL)[id]; seen != nil && seen["endpoint"] != nil {
		t.Error("access outlived the group membership it came from")
	}
}

// TestAConnectorFromBeforeOwnershipIsAdminOnly pins the migration decision, which
// is the one that costs an existing installation something.
//
// A record written before M11 carries no owner. It becomes an administrator's to
// manage rather than everybody's — the opposite of ADR-0071's choice for legacy
// artifacts, and deliberately so: that record was adding a capability, this one is
// closing a hole, and a measure that exempts every existing installation has closed
// nothing. The catalog entry stays visible, so authoring against it keeps working.
func TestAConnectorFromBeforeOwnershipIsAdminOnly(t *testing.T) {
	dir := t.TempDir()
	first := newServerOn(t, dir)
	admin := signedInClient(t, first.URL)
	createUser(t, admin, first.URL, "anna")

	// An admin-created connector, stripped of its owner on disk: exactly the shape a
	// pre-M11 record has. Written before the second server reads the directory, so
	// what it loads is a genuine legacy record rather than one patched in memory.
	id := createConnector(t, admin, first.URL, "altlast")
	first.stop()
	stripConnectorOwner(t, dir, id)

	ts := newServerOn(t, dir)
	anna := signInAs(t, ts.URL, "anna", "a-password-that-is-long")
	if seen := listConnectors(t, anna, ts.URL)[id]; seen != nil && seen["endpoint"] != nil {
		t.Error("an ownerless connector still shows its configuration to everybody")
	}
	if seen := listConnectors(t, anna, ts.URL)[id]; seen == nil {
		t.Error("an ownerless connector vanished from the catalog, which breaks authoring against it")
	}
	if got := statusOf(t, anna, http.MethodPatch, ts.URL+"/api/v1/connectors/"+id, `{"enabled":false}`); got == http.StatusOK {
		t.Error("an ordinary account still edits an ownerless connector")
	}

	admin = signedInClient(t, ts.URL)
	if got := statusOf(t, admin, http.MethodPatch, ts.URL+"/api/v1/connectors/"+id, `{"enabled":false}`); got != http.StatusOK {
		t.Errorf("an administrator cannot manage an ownerless connector: %d", got)
	}
}

// TestAnOpenServerSharesEverything: with --auth=false there is nobody to be someone
// else, so none of this applies. The same stance ADR-0071 takes, and the one thing
// an open server must never do is start refusing.
func TestAnOpenServerSharesEverything(t *testing.T) {
	ts := newOpenConnectorServer(t)
	c := &http.Client{}

	id := createConnector(t, c, ts.URL, "offen")
	if seen := listConnectors(t, c, ts.URL)[id]; seen == nil || seen["endpoint"] == nil {
		t.Fatalf("an open server hid a connector: %v", seen)
	}
	if got := statusOf(t, c, http.MethodPatch, ts.URL+"/api/v1/connectors/"+id, `{"enabled":false}`); got != http.StatusOK {
		t.Errorf("an open server refused an edit: %d", got)
	}
	if got := statusOf(t, c, http.MethodDelete, ts.URL+"/api/v1/connectors/"+id+"?force=true", ""); got != http.StatusNoContent {
		t.Errorf("an open server refused a delete: %d", got)
	}
}

// TestBorrowingAnotherConnectorsCredentialIsRefused closes what the connector check
// left open, and it is adjacent enough to be part of this measure rather than after
// it: POST /api/v1/connectors/test resolves whatever credential reference the body
// names and sends real mail with it.
//
// Locking the connector record while anyone may still borrow its credential would
// be theatre. So the rule is exact: you may name a credential reference only if a
// connector you may edit already uses it.
func TestBorrowingAnotherConnectorsCredentialIsRefused(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "bert")
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")

	// The house mail connector, owned by the administrator. Deliberately not the
	// preview provider: that one dials nothing and its validator clears the
	// credential reference, so there would be nothing to borrow and the test would
	// prove nothing.
	body := `{"name":"haus-mail","kind":"mail","provider":"smtp","sender":"atlas@example.com",` +
		`"endpoint":"smtp.example.com:587","credentialsRef":"haus-mail-secret","enabled":true}`
	resp, err := admin.Post(ts.URL+"/api/v1/connectors", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("create mail connector: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create mail connector = %d", resp.StatusCode)
	}

	// The refusal has to come before anything is dialled, so this never reaches the
	// network however the assertion goes.
	probe := `{"name":"geborgt","kind":"mail","provider":"smtp","sender":"atlas@example.com",` +
		`"endpoint":"smtp.example.com:587","credentialsRef":"haus-mail-secret","to":"opfer@example.com"}`
	if got := statusOf(t, bert, http.MethodPost, ts.URL+"/api/v1/connectors/test", probe); got != http.StatusForbidden {
		t.Errorf("= %d, want 403: a stranger borrowed a credential they may not reach", got)
	}
	// Naming no credential at all is nobody's secret, so it stays allowed. The
	// preview provider is right here: it dials nothing, so the check answers from
	// the permission alone.
	own := `{"name":"eigen","kind":"mail","provider":"preview","sender":"bert@example.com"}`
	if got := statusOf(t, bert, http.MethodPost, ts.URL+"/api/v1/connectors/test", own); got != http.StatusOK {
		t.Errorf("= %d, want 200: a check that resolves no stored secret is nobody's to refuse", got)
	}
}

// stripConnectorOwner rewrites a stored connector into the shape it had before
// ownership existed: no ownerId, no visibility, no members. The server must be
// stopped first — the store belongs to its run loop while it is running.
func stripConnectorOwner(t *testing.T, dir, id string) {
	t.Helper()
	// The store hex-encodes a key into its filename, so the record is found by
	// reading the directory rather than by rebuilding that encoding here — a test
	// that mirrors a storage detail breaks when the detail changes, for no reason.
	entries, err := os.ReadDir(filepath.Join(dir, "connectors"))
	if err != nil {
		t.Fatalf("read connector dir: %v", err)
	}
	var (
		path string
		rec  map[string]any
	)
	for _, e := range entries {
		p := filepath.Join(dir, "connectors", e.Name())
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		var cur map[string]any
		if json.Unmarshal(raw, &cur) != nil {
			continue
		}
		if cur["id"] == id {
			path, rec = p, cur
			break
		}
	}
	if rec == nil {
		t.Fatalf("no stored connector with id %s", id)
	}
	if _, ok := rec["ownerId"]; !ok {
		t.Fatalf("%s carries no ownerId, so this test would prove nothing", path)
	}
	delete(rec, "ownerId")
	delete(rec, "visibility")
	delete(rec, "members")
	out, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// newOpenConnectorServer starts a server without --auth: the deliberate exception
// (ADR-0195), where every rule in this file is a no-op.
func newOpenConnectorServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	wl, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, wl, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := api.New(proc, store, dir)
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
		_ = store.Close()
		_ = wl.Close()
	})
	return ts
}

// TestSealingAConnectorAndOpeningItAgain: an owner can shut a shared connector
// without losing the list of who it was shared with.
//
// Keeping the members through a seal is the point. If sealing cleared them, an
// owner who wanted to close a connector for an afternoon would have to rebuild the
// list afterwards — which is a good reason never to seal it, and a rule people
// route around is not a rule.
func TestSealingAConnectorAndOpeningItAgain(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "anna")
	bertID := createUser(t, admin, ts.URL, "bert")
	anna := signInAs(t, ts.URL, "anna", "a-password-that-is-long")
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")

	id := createConnector(t, anna, ts.URL, "zeitweise-offen")
	shareConnector(t, anna, ts.URL, id, bertID, "user", "editor", http.StatusOK)
	if listConnectors(t, bert, ts.URL)[id]["endpoint"] == nil {
		t.Fatal("the member cannot see the connector they were just shared")
	}

	visibility := ts.URL + "/api/v1/connectors/" + id + "/visibility"
	if got := statusOf(t, anna, http.MethodPut, visibility, `{"visibility":"private"}`); got != http.StatusOK {
		t.Fatalf("seal = %d, want 200", got)
	}
	if listConnectors(t, bert, ts.URL)[id]["endpoint"] != nil {
		t.Error("sealing the connector left its member with access")
	}

	if got := statusOf(t, anna, http.MethodPut, visibility, `{"visibility":"shared"}`); got != http.StatusOK {
		t.Fatalf("reopen = %d, want 200", got)
	}
	if listConnectors(t, bert, ts.URL)[id]["endpoint"] == nil {
		t.Error("reopening it did not restore the member — the list was lost in the seal")
	}

	t.Run("only the owner decides", func(t *testing.T) {
		// Bert is an editor, which is as far as anybody gets without being the owner.
		if got := statusOf(t, bert, http.MethodPut, visibility, `{"visibility":"private"}`); got != http.StatusForbidden {
			t.Errorf("= %d, want 403: an editor sealed somebody else's connector", got)
		}
	})

	t.Run("refuses what it cannot mean", func(t *testing.T) {
		for _, tc := range []struct{ name, body string }{
			{"not JSON", "{"},
			{"an unknown visibility", `{"visibility":"public"}`},
			{"no visibility at all", `{}`},
		} {
			if got := statusOf(t, anna, http.MethodPut, visibility, tc.body); got != http.StatusBadRequest {
				t.Errorf("%s = %d, want 400", tc.name, got)
			}
		}
	})
}

// TestHandingAConnectorOn: a connector outlives the person who made it.
//
// It exists because an ownerless connector is administrators-only. Without a
// transfer, one person leaving would make an administrator the owner of every
// connector they ever configured — and the fix would be editing JSON on disk.
func TestHandingAConnectorOn(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "anna")
	bertID := createUser(t, admin, ts.URL, "bert")
	anna := signInAs(t, ts.URL, "anna", "a-password-that-is-long")
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")

	id := createConnector(t, anna, ts.URL, "uebergabe")
	// Bert is an editor first, so the transfer has a stale member grant to clean up.
	shareConnector(t, anna, ts.URL, id, bertID, "user", "editor", http.StatusOK)

	owner := ts.URL + "/api/v1/connectors/" + id + "/owner/"
	t.Run("a stranger cannot hand on what is not theirs", func(t *testing.T) {
		if got := statusOf(t, bert, http.MethodPut, owner+bertID, ""); got != http.StatusForbidden {
			t.Errorf("= %d, want 403: an editor handed a connector to themselves", got)
		}
	})
	t.Run("and not to somebody who does not exist", func(t *testing.T) {
		if got := statusOf(t, anna, http.MethodPut, owner+"usr_nobody", ""); got != http.StatusBadRequest {
			t.Errorf("= %d, want 400", got)
		}
	})

	if got := statusOf(t, anna, http.MethodPut, owner+bertID, ""); got != http.StatusOK {
		t.Fatalf("transfer = %d, want 200", got)
	}

	// Bert owns it: he can do the one thing only an owner can.
	bert = signInAs(t, ts.URL, "bert", "a-password-that-is-long")
	if seen := listConnectors(t, bert, ts.URL)[id]; seen == nil || seen["role"] != "owner" {
		t.Errorf("the new owner does not hold the connector: %v", seen)
	}
	if got := statusOf(t, bert, http.MethodPut, ts.URL+"/api/v1/connectors/"+id+"/visibility",
		`{"visibility":"private"}`); got != http.StatusOK {
		t.Errorf("the new owner cannot seal it: %d", got)
	}
	// And the stale member grant went with the transfer: an owner listed as their own
	// editor reads as a restriction that is not there.
	seen := listConnectors(t, bert, ts.URL)[id]
	if members, _ := seen["members"].([]any); len(members) != 0 {
		t.Errorf("the new owner is still listed as a member of their own connector: %v", members)
	}
	// Anna handed it over, so she has it no longer.
	if seen := listConnectors(t, anna, ts.URL)[id]; seen != nil && seen["endpoint"] != nil {
		t.Error("the previous owner kept access after handing the connector on")
	}
}

// TestSharingRefusesWhatItCannotMean covers the ways a share request is wrong, and
// one that is merely pointless.
func TestSharingRefusesWhatItCannotMean(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	annaID := createUser(t, admin, ts.URL, "anna")
	bertID := createUser(t, admin, ts.URL, "bert")
	anna := signInAs(t, ts.URL, "anna", "a-password-that-is-long")

	id := createConnector(t, anna, ts.URL, "grenzfaelle")
	at := func(principal string) string {
		return ts.URL + "/api/v1/connectors/" + id + "/members/" + principal
	}

	for _, tc := range []struct {
		name, principal, body string
		want                  int
	}{
		{"not JSON", bertID, "{", http.StatusBadRequest},
		{"no role", bertID, `{}`, http.StatusBadRequest},
		{"owner is not a grantable role", bertID, `{"role":"owner"}`, http.StatusBadRequest},
		{"an unknown member type", bertID, `{"role":"viewer","type":"robot"}`, http.StatusBadRequest},
		{"a user who does not exist", "usr_nobody", `{"role":"viewer"}`, http.StatusBadRequest},
		{"a group that does not exist", "grp_nobody", `{"role":"viewer","type":"group"}`, http.StatusBadRequest},
		// Not an error in the world, but an instruction with no meaning: the owner
		// already has everything a member role could grant, and storing it would make
		// their own connector list them as its editor.
		{"the owner as their own member", annaID, `{"role":"editor"}`, http.StatusBadRequest},
		{"a connector that is not theirs", bertID, `{"role":"viewer"}`, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusOf(t, anna, http.MethodPut, at(tc.principal), tc.body); got != tc.want {
				t.Errorf("= %d, want %d", got, tc.want)
			}
		})
	}

	t.Run("withdrawing is idempotent", func(t *testing.T) {
		// Removing somebody who is not a member succeeds: an owner tidying up should
		// not have to know the current list to be allowed to shorten it.
		if got := statusOf(t, anna, http.MethodDelete, at("usr_never-was"), ""); got != http.StatusOK {
			t.Errorf("= %d, want 200", got)
		}
	})

	t.Run("a connector that does not exist is not there", func(t *testing.T) {
		gone := ts.URL + "/api/v1/connectors/nope/members/" + bertID
		if got := statusOf(t, anna, http.MethodPut, gone, `{"role":"viewer"}`); got != http.StatusNotFound {
			t.Errorf("= %d, want 404", got)
		}
		if got := statusOf(t, anna, http.MethodDelete, gone, ""); got != http.StatusNotFound {
			t.Errorf("delete = %d, want 404", got)
		}
	})
}

// TestAnAdministratorMayUseAnyCredential: the borrow rule has to leave the operator
// who owns the vault able to check a connector. Otherwise an administrator
// diagnosing somebody else's mail connector would be refused by a measure meant to
// stop a stranger.
func TestAnAdministratorMayUseAnyCredential(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "anna")
	anna := signInAs(t, ts.URL, "anna", "a-password-that-is-long")

	body := `{"name":"annas-mail","kind":"mail","provider":"smtp","sender":"anna@example.com",` +
		`"endpoint":"smtp.example.com:587","credentialsRef":"annas-secret","enabled":true}`
	resp, err := anna.Post(ts.URL+"/api/v1/connectors", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create = %d", resp.StatusCode)
	}

	// The preview provider clears the reference before anything is resolved, so this
	// asks only the question this test is about and dials nothing.
	probe := `{"name":"pruefung","kind":"mail","provider":"preview","sender":"anna@example.com"}`
	if got := statusOf(t, admin, http.MethodPost, ts.URL+"/api/v1/connectors/test", probe); got != http.StatusOK {
		t.Errorf("= %d, want 200: an administrator was refused a connector check", got)
	}
}

// TestConnectorSharingSurvivesABrokenDataDirectory: what the ownership endpoints
// answer when the disk beneath them is not what it was.
//
// 500 and no change, rather than an answer that reads as a decision. A sharing
// endpoint that reported "no connector with that id" for an unreadable store would
// look exactly like a connector somebody had deleted.
func TestConnectorSharingSurvivesABrokenDataDirectory(t *testing.T) {
	dir := t.TempDir()
	ts := newServerOn(t, dir)
	admin := signedInClient(t, ts.URL)
	bertID := createUser(t, admin, ts.URL, "bert")
	createUser(t, admin, ts.URL, "carla")
	carla := signInAs(t, ts.URL, "carla", "a-password-that-is-long")
	id := createConnector(t, admin, ts.URL, "kaputt")

	// Broken under a running server, because a server cannot start on one: New
	// creates the store's directory and fails if something else is in its place. A
	// plain file where the directory belongs then fails every read and every write
	// the store makes, and fails for root too — which chmod would not achieve here.
	p := filepath.Join(dir, "connectors")
	if err := os.RemoveAll(p); err != nil {
		t.Fatalf("remove %s: %v", p, err)
	}
	if err := os.WriteFile(p, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}

	base := ts.URL + "/api/v1/connectors/" + id
	for _, tc := range []struct{ name, method, url, body string }{
		{"share", http.MethodPut, base + "/members/" + bertID, `{"role":"viewer"}`},
		{"unshare", http.MethodDelete, base + "/members/" + bertID, ""},
		{"visibility", http.MethodPut, base + "/visibility", `{"visibility":"private"}`},
		{"transfer", http.MethodPut, base + "/owner/" + bertID, ""},
		{"list subscriptions", http.MethodGet, base + "/inbound-subscriptions", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusOf(t, admin, tc.method, tc.url, tc.body); got != http.StatusInternalServerError {
				t.Errorf("= %d, want 500 — an unreadable store must not read as a decision", got)
			}
		})
	}
	// The connector check reads the same store to decide whether a credential may be
	// borrowed, and an unreadable store is not a permission answer either.
	probe := `{"name":"x","kind":"mail","provider":"smtp","sender":"a@example.com",` +
		`"endpoint":"smtp.example.com:587","credentialsRef":"irgendein-verweis"}`
	if got := statusOf(t, carla, http.MethodPost, ts.URL+"/api/v1/connectors/test", probe); got != http.StatusInternalServerError {
		t.Errorf("connector check = %d, want 500", got)
	}
}

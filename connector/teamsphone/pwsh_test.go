package teamsphone_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/teamsphone"
)

// The answer is the last line of stdout that decodes as one, so a module that writes
// to the output stream despite the script's care costs nothing.
func TestTheAnswerIsTheLastEnvelope(t *testing.T) {
	out := []byte("Account  Environment\r\n-------  -----------\r\n{\"ok\":true,\"result\":{\"id\":\"first\"}}\r\nnoise\r\n{\"ok\":true,\"result\":{\"id\":\"last\"}}\r\n")
	env, ok := teamsphone.LastEnvelope(out)
	if !ok || env.Result["id"] != "last" {
		t.Errorf("envelope = %+v, %v; want the last one", env, ok)
	}
	if _, ok := teamsphone.LastEnvelope([]byte("WARNING: something\n")); ok {
		t.Error("a line that is no envelope was read as an answer")
	}
}

// The request travels as ASCII, so a display name with an umlaut arrives intact
// whatever code page the reading side assumes.
func TestTheRequestIsASCII(t *testing.T) {
	raw, err := teamsphone.ASCIIJSON(map[string]string{"displayName": "Zürich Empfang 📞"})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range raw {
		if b >= 0x80 {
			t.Fatalf("encoded request has a non-ASCII byte: %s", raw)
		}
	}
	var back map[string]string
	if err := json.Unmarshal(raw, &back); err != nil || back["displayName"] != "Zürich Empfang 📞" {
		t.Errorf("round trip = %v, %v", back, err)
	}
}

// pwshBinary finds a PowerShell 7 to run the shipped script with: ATLAS_TEST_PWSH, or
// pwsh on the PATH. Without one the end-to-end test is skipped, not failed — the script
// is still covered by the mock's rules, but not by a run.
func pwshBinary(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("ATLAS_TEST_PWSH"); p != "" {
		return p
	}
	p, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("pwsh not found; set ATLAS_TEST_PWSH to run the shipped PowerShell end to end")
	}
	return p
}

// stubTenant is the test's Microsoft: a token endpoint that checks the client assertion
// the script signs, and the Graph user endpoint, both reading the tenant the stub
// MicrosoftTeams module keeps in a JSON file.
type stubTenant struct {
	t         *testing.T
	statePath string
	cert      *x509.Certificate
	mu        sync.Mutex
	tokens    int
}

type stubState struct {
	Connects  int                       `json:"connects"`
	Sets      int                       `json:"sets"`
	Users     map[string]string         `json:"users"`
	Accounts  map[string]map[string]any `json:"accounts"`
	Apps      map[string]map[string]any `json:"apps"`
	Assoc     map[string]map[string]any `json:"assoc"`
	Schedules []map[string]any          `json:"schedules"`
	Numbers   map[string]map[string]any `json:"numbers"`
	Entra     map[string]map[string]any `json:"entra"`
}

func (s *stubTenant) load() stubState {
	raw, err := os.ReadFile(s.statePath)
	if err != nil {
		s.t.Fatalf("read stub state: %v", err)
	}
	var st stubState
	if err := json.Unmarshal(raw, &st); err != nil {
		s.t.Fatalf("decode stub state: %v", err)
	}
	return st
}

func (s *stubTenant) save(st stubState) {
	raw, err := json.Marshal(st)
	if err != nil {
		s.t.Fatalf("encode stub state: %v", err)
	}
	if err := os.WriteFile(s.statePath, raw, 0o600); err != nil {
		s.t.Fatalf("write stub state: %v", err)
	}
}

func (s *stubTenant) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token"):
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if msg := s.checkAssertion(r.PostForm.Get("client_assertion"), "http://"+r.Host+r.URL.Path); msg != "" {
			s.t.Errorf("token request: %s", msg)
			http.Error(w, msg, http.StatusUnauthorized)
			return
		}
		s.tokens++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"graph-token","expires_in":3599}`))
	case strings.HasPrefix(r.URL.Path, "/v1.0/users/"):
		if r.Header.Get("Authorization") != "Bearer graph-token" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		key := strings.TrimPrefix(r.URL.Path, "/v1.0/users/")
		st := s.load()
		for id, a := range st.Accounts {
			if id != key && !strings.EqualFold(a["UserPrincipalName"].(string), key) {
				continue
			}
			if r.Method == http.MethodDelete {
				delete(st.Accounts, id)
				s.save(st)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "displayName": a["DisplayName"], "accountEnabled": false})
			return
		}
		if u, ok := st.Entra[strings.ToLower(key)]; ok && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(u)
			return
		}
		http.Error(w, `{"error":{"code":"Request_ResourceNotFound"}}`, http.StatusNotFound)
	default:
		http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
	}
}

// checkAssertion verifies what the script signed: RS256 under the certificate's key,
// the certificate named by its SHA-256 thumbprint, and the claims RFC 7523 requires.
func (s *stubTenant) checkAssertion(assertion, aud string) string {
	parts := strings.Split(assertion, ".")
	if len(parts) != 3 {
		return "the client assertion is no JWT"
	}
	var header map[string]string
	var claims map[string]any
	for i, into := range []any{&header, &claims} {
		raw, err := base64.RawURLEncoding.DecodeString(parts[i])
		if err != nil || json.Unmarshal(raw, into) != nil {
			return "the client assertion does not decode"
		}
	}
	thumb := sha256.Sum256(s.cert.Raw)
	if header["alg"] != "RS256" || header["x5t#S256"] != base64.RawURLEncoding.EncodeToString(thumb[:]) {
		return "the assertion header does not name the certificate"
	}
	if claims["aud"] != aud || claims["iss"] != "app-1" || claims["sub"] != "app-1" {
		return "the assertion claims are not the app's"
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(s.cert.PublicKey.(*rsa.PublicKey), crypto.SHA256, digest[:], sig); err != nil {
		return "the certificate does not verify the assertion"
	}
	return ""
}

func testCertificate(t *testing.T) (string, *x509.Certificate) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "atlas-teamsphone-test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})) +
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})), cert
}

// TestTheShippedScriptProvisionsAndUnwinds runs operations.ps1 in a real pwsh against
// a stand-in MicrosoftTeams module and a stand-in Microsoft: the sign-in, the Graph
// token the script signs itself, the Teams/Entra lag, the refusals and the leaver path.
func TestTheShippedScriptProvisionsAndUnwinds(t *testing.T) {
	bin := pwshBinary(t)
	tmp := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, tmp)
	}
	modules, err := filepath.Abs(filepath.Join("testdata", "modules"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PSModulePath", modules)
	certPEM, cert := testCertificate(t)
	stub := &stubTenant{t: t, statePath: filepath.Join(tmp, "teamsphone-stub.json"), cert: cert}
	stub.save(stubState{
		Users:    map[string]string{"anna@contoso.example": "user-anna"},
		Accounts: map[string]map[string]any{}, Apps: map[string]map[string]any{}, Assoc: map[string]map[string]any{},
		Schedules: []map[string]any{},
		// The tenant's inventory, held by nobody yet: a Calling Plan number given out below,
		// and beside it the ones list-free-phone-numbers must tell apart — one only a person
		// can hold, one blocked, one not activated yet, and one of another type.
		Numbers: map[string]map[string]any{
			"+41441110000": {"TelephoneNumber": "+41441110000", "NumberType": "CallingPlan", "AssignedPstnTargetId": "", "AssignmentCategory": "Primary"},
			"+41441110001": {"TelephoneNumber": "+41441110001", "NumberType": "CallingPlan", "AssignedPstnTargetId": "", "Capability": []string{"UserAssignment"}, "City": "Zürich", "IsoCountryCode": "CH"},
			"+41441110002": {"TelephoneNumber": "+41441110002", "NumberType": "CallingPlan", "AssignedPstnTargetId": "", "AssignmentBlockedState": "BlockedForever"},
			"+41441110003": {"TelephoneNumber": "+41441110003", "NumberType": "CallingPlan", "AssignedPstnTargetId": "", "ActivationState": "AssignmentPending"},
			"+41441110004": {"TelephoneNumber": "+41441110004", "NumberType": "CallingPlan", "AssignedPstnTargetId": ""},
			"+41449990000": {"TelephoneNumber": "+41449990000", "NumberType": "OperatorConnect", "AssignedPstnTargetId": ""},
		},
		Entra: map[string]map[string]any{
			"anna@contoso.example": {"id": "user-anna", "displayName": "Anna Muster", "accountEnabled": true},
		},
	})
	srv := httptest.NewServer(stub)
	defer srv.Close()

	client := (&teamsphone.Pwsh{
		Credential: teamsphone.Credential{TenantID: "tenant-1", ClientID: "app-1", Certificate: certPEM},
		Bin:        bin,
		Timeout:    2 * time.Minute,
		// Not waiting for Teams to show a new account lets the test see the refusal
		// the wait ends in, instead of sitting through it.
		AccountWait: time.Millisecond,
	}).WithEndpoints(srv.URL, srv.URL+"/v1.0")
	reg := teamsphone.NewRegistry()
	reg.Register("contoso", client)
	ctx := context.Background()
	run := func(op string, args map[string]any) (map[string]any, error) {
		out, err := teamsphone.Run(ctx, teamsphone.Job{Connector: "contoso", Operation: op, Args: args, ResultVariable: "r"}, reg)
		if err != nil {
			return nil, err
		}
		return out["r"].(map[string]any), nil
	}
	must := func(op string, args map[string]any) map[string]any {
		t.Helper()
		out, err := run(op, args)
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		return out
	}
	coded := func(err error, code string) bool {
		var refusal *teamsphone.Refusal
		return errors.As(err, &refusal) && refusal.Code == code
	}
	// changed reports whether an ensure-call-queue answered that it changed exactly the
	// named values, in the sorted order the answer lists them in.
	changed := func(out map[string]any, names ...string) bool {
		got, _ := out["changed"].([]any)
		if len(got) != len(names) {
			return false
		}
		for i, n := range names {
			if got[i] != n {
				return false
			}
		}
		return true
	}

	users := must("resolve-users", map[string]any{"users": "anna@contoso.example, ghost@contoso.example"})
	if ids, _ := users["ids"].([]any); len(ids) != 1 || ids[0] != "user-anna" {
		t.Errorf("resolve-users ids = %v", users)
	}
	if nf, _ := users["notFound"].([]any); len(nf) != 1 || nf[0] != "ghost@contoso.example" {
		t.Errorf("resolve-users notFound = %v", users)
	}

	cqArgs := map[string]any{"userPrincipalName": "cq_zuerich@contoso.example", "displayName": "Zürich Empfang", "accountType": "call-queue"}
	first := must("ensure-resource-account", cqArgs)
	if first["created"] != true || first["source"] != "created" {
		t.Fatalf("first ensure-resource-account = %v", first)
	}
	cqID := first["objectId"].(string)
	if st := stub.load(); st.Accounts[cqID]["DisplayName"] != "Zürich Empfang" {
		t.Errorf("display name arrived as %q", st.Accounts[cqID]["DisplayName"])
	}
	// Teams does not show the new account yet: a queue for it is refused before anything
	// is created, and a repeated ensure adopts the account from Entra.
	if _, err := run("ensure-call-queue", map[string]any{"name": "CQ Zürich", "resourceAccountId": cqID}); err == nil ||
		!strings.Contains(err.Error(), "not visible to Teams yet") || !coded(err, teamsphone.CodeAccountNotVisible) {
		t.Errorf("a queue for an account Teams does not show yet: %v", err)
	}
	if again := must("ensure-resource-account", cqArgs); again["created"] != false || again["source"] != "entra" || again["objectId"] != cqID {
		t.Errorf("repeated ensure-resource-account = %v, want the account adopted from Entra", again)
	}
	if third := must("ensure-resource-account", cqArgs); third["source"] != "teams" || third["objectId"] != cqID {
		t.Errorf("third ensure-resource-account = %v, want it found in Teams", third)
	}
	if _, err := run("ensure-resource-account", map[string]any{"userPrincipalName": "anna@contoso.example", "displayName": "Zürich Empfang", "accountType": "call-queue"}); err == nil ||
		!strings.Contains(err.Error(), "enabled user account") || !coded(err, teamsphone.CodeUPNTaken) {
		t.Errorf("an enabled user's address as a resource account: %v", err)
	}
	aaID := must("ensure-resource-account", map[string]any{"userPrincipalName": "aa_zuerich@contoso.example", "displayName": "Zürich Empfang", "accountType": "auto-attendant"})["objectId"].(string)
	must("ensure-resource-account", map[string]any{"userPrincipalName": "aa_zuerich@contoso.example", "displayName": "Zürich Empfang", "accountType": "auto-attendant"})

	// A queue made without naming its lists gets none — not one blank entry each — and
	// the default settings, which a repeat leaves without a single change.
	queueArgs := map[string]any{"name": "CQ Zürich", "resourceAccountId": cqID, "routingMethod": "serial", "languageId": "de-CH"}
	queue := must("ensure-call-queue", queueArgs)
	if again := must("ensure-call-queue", queueArgs); queue["created"] != true || again["id"] != queue["id"] || !changed(again) {
		t.Errorf("ensure-call-queue did not converge: %v, then %v", queue, again)
	}
	queueID := queue["id"].(string)
	app := func(id string) map[string]any { return stub.load().Apps[id] }
	listOf := func(id, name string) []any { l, _ := app(id)[name].([]any); return l }
	if params := app(queueID)["Params"].(map[string]any); params["RoutingMethod"] != "Serial" || params["ConferenceMode"] != true || params["AllowOptOut"] != true {
		t.Errorf("queue created with %v", params)
	}
	if users, owners := listOf(queueID, "Users"), listOf(queueID, "AuthorizedUsers"); len(users) != 0 || len(owners) != 0 {
		t.Errorf("a queue made without lists has the agents %v and the authorized users %v", users, owners)
	}
	if n := stub.load().Sets; n != 0 {
		t.Errorf("a repeat with nothing to change called Set-CsCallQueue %d times", n)
	}
	// A change of staff and of routing reaches the existing queue in one Set-CsCallQueue,
	// the agents in the order serial routing rings them in, and a repeat changes nothing.
	staffed := map[string]any{"name": "CQ Zürich", "resourceAccountId": cqID, "agents": []any{"user-beat", "USER-ANNA"},
		"authorizedUsers": "owner-1", "routingMethod": "serial", "presenceBasedRouting": "false"}
	if got := must("ensure-call-queue", staffed); got["created"] != false || !changed(got, "agents", "authorizedUsers", "presenceBasedRouting") || got["agentsChanged"] != true {
		t.Errorf("ensure-call-queue with new agents and routing = %v, want them set on the existing queue", got)
	}
	st := stub.load()
	if users := listOf(queueID, "Users"); len(users) != 2 || users[0] != "user-beat" || users[1] != "USER-ANNA" {
		t.Errorf("the queue's agents are %v, want [user-beat USER-ANNA]", users)
	}
	if owners := listOf(queueID, "AuthorizedUsers"); len(owners) != 1 || owners[0] != "owner-1" {
		t.Errorf("the queue's authorized users are %v, want [owner-1]", owners)
	}
	if q := st.Apps[queueID]; q["PresenceBasedRouting"] != false || q["RoutingMethod"] != "Serial" || q["ConferenceMode"] != true || st.Sets != 1 {
		t.Errorf("the queue reads %v after %d Set-CsCallQueue calls; want presence-based routing off in one call, the rest as it was", q, st.Sets)
	}
	if got := must("ensure-call-queue", staffed); !changed(got) || stub.load().Sets != 1 {
		t.Errorf("a repeated change = %v, want nothing left to change and no further call", got)
	}
	// Authorized users named in another case are no change, but on a serial queue another
	// order of agents is, even with the routing left out; the settings the model leaves
	// out stay as the queue has them.
	same := map[string]any{"name": "CQ Zürich", "resourceAccountId": cqID, "agents": "user-anna, user-beat", "authorizedUsers": "OWNER-1"}
	if got := must("ensure-call-queue", same); !changed(got, "agents") {
		t.Errorf("the same agents in another order on a serial queue = %v, want the order set", got)
	}
	if got := must("ensure-call-queue", map[string]any{"name": "CQ Zürich", "resourceAccountId": cqID, "routingMethod": "round-robin", "agents": "USER-ANNA, user-beat"}); !changed(got, "routingMethod") {
		t.Errorf("round-robin with the agents in another order = %v, want only the routing changed", got)
	}
	if q := app(queueID); q["RoutingMethod"] != "RoundRobin" || q["PresenceBasedRouting"] != false {
		t.Errorf("the queue reads %v, want round-robin with presence-based routing still off", q)
	}
	for _, list := range []string{"agents", "authorizedUsers"} {
		before := stub.load().Sets
		_, err := run("ensure-call-queue", map[string]any{"name": "CQ Zürich", "resourceAccountId": cqID, list: []any{}, "conferenceMode": "false"})
		if err == nil || !strings.Contains(err.Error(), "would remove every") || !coded(err, teamsphone.CodeWouldEmptyList) {
			t.Errorf("an empty %s for a queue that has some: %v; want it refused", list, err)
		}
		if stub.load().Sets != before || app(queueID)["ConferenceMode"] != true {
			t.Errorf("a refused reconciliation of %s still changed the queue", list)
		}
	}
	// A queue made with its lists carries them from the start.
	must("remove-call-queue", map[string]any{"resourceAccountId": cqID})
	remade := must("ensure-call-queue", staffed)
	if users := listOf(remade["id"].(string), "Users"); remade["created"] != true || !changed(remade) || len(users) != 2 || users[0] != "user-beat" {
		t.Errorf("ensure-call-queue with agents = %v carrying %v, want a new queue with [user-beat USER-ANNA]", remade, users)
	}
	if owners := listOf(remade["id"].(string), "AuthorizedUsers"); len(owners) != 1 {
		t.Errorf("the new queue's authorized users are %v, want [owner-1]", owners)
	}
	attendantArgs := map[string]any{
		"name": "AA Zürich", "resourceAccountId": aaID, "transferTarget": cqID, "languageId": "de-CH",
		"timeZone": "W. Europe Standard Time", "holidays": `[{"name":"Weihnachten","start":"2026-12-25"}]`,
	}
	attendant := must("ensure-auto-attendant", attendantArgs)
	if attendant["created"] != true || must("ensure-auto-attendant", attendantArgs)["created"] != false {
		t.Errorf("ensure-auto-attendant did not converge: %v", attendant)
	}
	st = stub.load()
	if params := st.Apps[attendant["id"].(string)]["Params"].(map[string]any); params["TransferTarget"] != cqID || params["Holidays"] != float64(1) {
		t.Errorf("auto attendant created with %v", params)
	}
	if len(st.Schedules) != 1 || st.Schedules[0]["Start"] != "25/12/2026" || st.Schedules[0]["End"] != "26/12/2026" {
		t.Errorf("holiday schedules = %v, want Christmas Day in Teams' date form", st.Schedules)
	}

	// Phone numbers: a Direct Routing number for the queue's account, by id and then by
	// address; a Calling Plan number from the inventory for a person, replaced by another
	// — the stand-in refuses a second primary number, so the old one must go first — and
	// the refusals a process can catch.
	number := func(identity, phone, kind string) (map[string]any, error) {
		return run("ensure-phone-number", map[string]any{"identity": identity, "phoneNumber": phone, "numberType": kind})
	}
	if got, err := number(cqID, "+41 44 555 00 00", "direct-routing"); err != nil || got["assigned"] != true || got["phoneNumber"] != "+41445550000" {
		t.Errorf("a Direct Routing number for the queue's account = %v, %v", got, err)
	}
	if got, err := number("cq_zuerich@contoso.example", "+41445550000", "direct-routing"); err != nil || got["assigned"] != false {
		t.Errorf("the same number for the account by its address = %v, %v; want nothing to do", got, err)
	}
	if got, err := number("anna@contoso.example", "+41441110000", "calling-plan"); err != nil || got["assigned"] != true {
		t.Errorf("a Calling Plan number for a person = %v, %v", got, err)
	}
	if got, err := number("anna@contoso.example", "+41445550001", "direct-routing"); err != nil || got["assigned"] != true || got["previousNumber"] != "+41441110000" {
		t.Errorf("a new number for the person = %v, %v; want the old one replaced and reported", got, err)
	}
	if n := stub.load().Numbers["+41441110000"]; n["AssignedPstnTargetId"] != "" {
		t.Errorf("the replaced Calling Plan number is still held: %v", n)
	}
	if _, err := number("anna@contoso.example", "+41445550000", "direct-routing"); !coded(err, teamsphone.CodeNumberInUse) {
		t.Errorf("the queue's number for a person: %v; want it refused as %s", err, teamsphone.CodeNumberInUse)
	}
	if _, err := number("anna@contoso.example", "+41449999999", "calling-plan"); !coded(err, teamsphone.CodeNumberNotFound) {
		t.Errorf("a Calling Plan number the tenant lacks: %v; want it refused as %s", err, teamsphone.CodeNumberNotFound)
	}
	removeNumbers := func(args map[string]any) map[string]any { return must("remove-phone-number", args) }
	if got := removeNumbers(map[string]any{"identity": "anna@contoso.example"}); got["removed"] != true || len(got["phoneNumbers"].([]any)) != 1 {
		t.Errorf("remove-phone-number for the person = %v", got)
	}
	if got := removeNumbers(map[string]any{"identity": "anna@contoso.example"}); got["removed"] != false {
		t.Errorf("remove-phone-number repeated = %v, want removed=false", got)
	}
	if got := removeNumbers(map[string]any{"identity": cqID, "phoneNumber": "+41445550000"}); got["removed"] != true {
		t.Errorf("remove-phone-number for the account's number = %v", got)
	}
	// The queue's account gets its number back, and the leaver path below takes it away
	// with the account.
	if got, err := number(cqID, "+41445550000", "direct-routing"); err != nil || got["assigned"] != true {
		t.Errorf("the queue's number again = %v, %v", got, err)
	}

	// The free numbers: Teams filters by status, activation, type, capability and prefix,
	// the script drops a blocked one and sorts; one more than max is asked for, so a
	// page that stopped short says so. The number Anna gave back is free again.
	free := func(assignable string, extra map[string]any) map[string]any {
		args := map[string]any{"numberType": "calling-plan", "assignableTo": assignable, "numberPrefix": "+41 44 111"}
		for k, v := range extra {
			args[k] = v
		}
		return must("list-free-phone-numbers", args)
	}
	listed := func(got map[string]any) []string {
		out := []string{}
		for _, n := range got["numbers"].([]any) {
			out = append(out, n.(string))
		}
		return out
	}
	if got := free("user", nil); !slices.Equal(listed(got), []string{"+41441110000", "+41441110001", "+41441110004"}) || fmt.Sprint(got["count"]) != "3" || got["truncated"] != false {
		t.Errorf("free Calling Plan numbers for a person = %v; want the free, activated, unblocked ones", got)
	} else if entry := got["entries"].([]any)[1].(map[string]any); entry["phoneNumber"] != "+41441110001" || entry["city"] != "Zürich" || entry["isoCountryCode"] != "CH" || entry["numberType"] != "calling-plan" {
		t.Errorf("entry = %v; want the number with its city, country and type", entry)
	}
	if got := free("resource-account", nil); !slices.Equal(listed(got), []string{"+41441110000", "+41441110004"}) {
		t.Errorf("free numbers for a resource account = %v; want none that only a person can hold", got)
	}
	if got := free("user", map[string]any{"max": json.Number("1")}); !slices.Equal(listed(got), []string{"+41441110000"}) || got["truncated"] != true {
		t.Errorf("one free number = %v; want the first and truncated", got)
	}
	if got := must("list-free-phone-numbers", map[string]any{"numberType": "operator-connect", "assignableTo": "user"}); !slices.Equal(listed(got), []string{"+41449990000"}) {
		t.Errorf("free Operator Connect numbers = %v", got)
	}
	if got := free("user", map[string]any{"numberPrefix": "+41 44 777"}); len(listed(got)) != 0 || fmt.Sprint(got["count"]) != "0" {
		t.Errorf("free numbers under a prefix nobody has = %v; want an empty list", got)
	}

	// An attendant's account carries no queue, and a queue's remove does not take the
	// attendant away: both are refusals a process can catch by their code.
	if _, err := run("ensure-call-queue", map[string]any{"name": "CQ Falsch", "resourceAccountId": aaID}); !coded(err, teamsphone.CodeAccountTypeMismatch) {
		t.Errorf("a queue on an attendant's account: %v; want it refused as %s", err, teamsphone.CodeAccountTypeMismatch)
	}
	if _, err := run("remove-call-queue", map[string]any{"resourceAccountId": aaID}); !coded(err, teamsphone.CodeAccountInUse) {
		t.Errorf("remove-call-queue on an attendant's account: %v; want it refused as %s", err, teamsphone.CodeAccountInUse)
	}

	// The leaver path: the attendant and its account one operation at a time, the queue
	// with its account and number as one hotline.
	for _, step := range []struct{ op, account string }{
		{"remove-auto-attendant", aaID}, {"remove-resource-account", aaID},
	} {
		if got := must(step.op, map[string]any{"resourceAccountId": step.account}); got["removed"] != true {
			t.Errorf("%s = %v, want removed", step.op, got)
		}
		if got := must(step.op, map[string]any{"resourceAccountId": step.account}); got["removed"] != false {
			t.Errorf("%s repeated = %v, want removed=false", step.op, got)
		}
	}
	// A queue that also answers for an account the hotline does not name is refused
	// before anything changes.
	st = stub.load()
	st.Assoc["someone-else"] = map[string]any{"ConfigurationId": remade["id"], "ConfigurationType": "CallQueue"}
	stub.save(st)
	if _, err := run("remove-hotline", map[string]any{"resourceAccountIds": []any{cqID}}); !coded(err, teamsphone.CodeAccountInUse) ||
		!strings.Contains(err.Error(), "someone-else") {
		t.Errorf("a hotline whose queue also answers for another account: %v; want it refused as %s", err, teamsphone.CodeAccountInUse)
	}
	st = stub.load()
	if len(st.Apps) != 1 || len(st.Accounts) != 1 {
		t.Errorf("a refused remove-hotline changed the tenant: %d voice apps, %d accounts", len(st.Apps), len(st.Accounts))
	}
	delete(st.Assoc, "someone-else")
	stub.save(st)
	hotline := must("remove-hotline", map[string]any{"resourceAccountIds": []any{cqID}})
	if queues, _ := hotline["queues"].([]any); hotline["removed"] != true || len(queues) != 1 || queues[0] != remade["id"] {
		t.Errorf("remove-hotline = %v, want the queue %v removed", hotline, remade["id"])
	}
	if numbers, _ := hotline["phoneNumbers"].([]any); len(numbers) != 1 || numbers[0] != "+41445550000" {
		t.Errorf("remove-hotline = %v, want the queue's number released with its account", hotline)
	}
	if again := must("remove-hotline", map[string]any{"resourceAccountIds": []any{cqID}}); again["removed"] != false {
		t.Errorf("remove-hotline repeated = %v, want removed=false", again)
	}
	if st := stub.load(); len(st.Apps) != 0 || len(st.Accounts) != 0 || len(st.Assoc) != 0 || st.Numbers["+41445550000"] != nil {
		t.Errorf("the leaver path left %d voice apps, %d accounts, %d associations and the number %v",
			len(st.Apps), len(st.Accounts), len(st.Assoc), st.Numbers["+41445550000"])
	}
	if stub.tokens == 0 {
		t.Error("the script never asked for a Graph token")
	}
}

// A failure inside the script is the job's error, with the script's own words.
func TestAScriptFailureIsTheJobsError(t *testing.T) {
	bin := pwshBinary(t)
	tmp := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, tmp)
	}
	modules, _ := filepath.Abs(filepath.Join("testdata", "modules"))
	t.Setenv("PSModulePath", modules)
	client := &teamsphone.Pwsh{
		Credential: teamsphone.Credential{TenantID: "t", ClientID: "c", CertificateThumbprint: "00 11 22"},
		Bin:        bin,
	}
	_, err := client.Do(context.Background(), teamsphone.Request{Operation: "resolve-users", Args: map[string]any{"users": []string{"a"}}})
	if err == nil || !strings.Contains(err.Error(), "no certificate with the thumbprint 001122") {
		t.Errorf("want the script's refusal of the missing certificate, got %v", err)
	}
}

// A worker host without PowerShell says what to install.
func TestAMissingPwshSaysWhatToInstall(t *testing.T) {
	client := &teamsphone.Pwsh{Bin: filepath.Join(t.TempDir(), "no-pwsh-here")}
	_, err := client.Do(context.Background(), teamsphone.Request{Operation: "resolve-users"})
	if err == nil || !strings.Contains(err.Error(), "install PowerShell 7") {
		t.Errorf("want an error naming what to install, got %v", err)
	}
}

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
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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
	Users     map[string]string         `json:"users"`
	Accounts  map[string]map[string]any `json:"accounts"`
	Apps      map[string]map[string]any `json:"apps"`
	Assoc     map[string]map[string]any `json:"assoc"`
	Schedules []map[string]any          `json:"schedules"`
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
		!strings.Contains(err.Error(), "not visible to Teams yet") {
		t.Errorf("a queue for an account Teams does not show yet: %v", err)
	}
	if again := must("ensure-resource-account", cqArgs); again["created"] != false || again["source"] != "entra" || again["objectId"] != cqID {
		t.Errorf("repeated ensure-resource-account = %v, want the account adopted from Entra", again)
	}
	if third := must("ensure-resource-account", cqArgs); third["source"] != "teams" || third["objectId"] != cqID {
		t.Errorf("third ensure-resource-account = %v, want it found in Teams", third)
	}
	if _, err := run("ensure-resource-account", map[string]any{"userPrincipalName": "anna@contoso.example", "displayName": "Zürich Empfang", "accountType": "call-queue"}); err == nil ||
		!strings.Contains(err.Error(), "enabled user account") {
		t.Errorf("an enabled user's address as a resource account: %v", err)
	}
	aaID := must("ensure-resource-account", map[string]any{"userPrincipalName": "aa_zuerich@contoso.example", "displayName": "Zürich Empfang", "accountType": "auto-attendant"})["objectId"].(string)
	must("ensure-resource-account", map[string]any{"userPrincipalName": "aa_zuerich@contoso.example", "displayName": "Zürich Empfang", "accountType": "auto-attendant"})

	queueArgs := map[string]any{"name": "CQ Zürich", "resourceAccountId": cqID, "agents": []any{"user-anna"}, "routingMethod": "serial", "languageId": "de-CH"}
	queue := must("ensure-call-queue", queueArgs)
	if queue["created"] != true || must("ensure-call-queue", queueArgs)["id"] != queue["id"] {
		t.Errorf("ensure-call-queue did not converge: %v", queue)
	}
	if params := stub.load().Apps[queue["id"].(string)]["Params"].(map[string]any); params["RoutingMethod"] != "Serial" || params["ConferenceMode"] != true {
		t.Errorf("queue created with %v", params)
	}
	attendantArgs := map[string]any{
		"name": "AA Zürich", "resourceAccountId": aaID, "transferTarget": cqID, "languageId": "de-CH",
		"timeZone": "W. Europe Standard Time", "holidays": `[{"name":"Weihnachten","start":"2026-12-25"}]`,
	}
	attendant := must("ensure-auto-attendant", attendantArgs)
	if attendant["created"] != true || must("ensure-auto-attendant", attendantArgs)["created"] != false {
		t.Errorf("ensure-auto-attendant did not converge: %v", attendant)
	}
	st := stub.load()
	if params := st.Apps[attendant["id"].(string)]["Params"].(map[string]any); params["TransferTarget"] != cqID || params["Holidays"] != float64(1) {
		t.Errorf("auto attendant created with %v", params)
	}
	if len(st.Schedules) != 1 || st.Schedules[0]["Start"] != "25/12/2026" || st.Schedules[0]["End"] != "26/12/2026" {
		t.Errorf("holiday schedules = %v, want Christmas Day in Teams' date form", st.Schedules)
	}

	for _, step := range []struct{ op, account string }{
		{"remove-auto-attendant", aaID}, {"remove-call-queue", cqID},
		{"remove-resource-account", aaID}, {"remove-resource-account", cqID},
	} {
		if got := must(step.op, map[string]any{"resourceAccountId": step.account}); got["removed"] != true {
			t.Errorf("%s = %v, want removed", step.op, got)
		}
		if got := must(step.op, map[string]any{"resourceAccountId": step.account}); got["removed"] != false {
			t.Errorf("%s repeated = %v, want removed=false", step.op, got)
		}
	}
	if st := stub.load(); len(st.Apps) != 0 || len(st.Accounts) != 0 || len(st.Assoc) != 0 {
		t.Errorf("the leaver path left %d voice apps, %d accounts and %d associations", len(st.Apps), len(st.Accounts), len(st.Assoc))
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

package contentsource

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/atlas/logging"
)

// pastedToken is what a read token of the first source looks like: the prefix
// and 64 hex digits. It is typed where the name of its vault secret belongs.
const pastedToken = "wwbb_fe9204b0450ab0eab30cd7dda81a2bbbb344a10cb50a3b0274906a14b088ebd8"

// TestLooksLikeCredential tells a pasted credential from the name of one: the
// source's own token prefix in any case, a long run of hex digits and a signed
// token are credentials; the names a vault holds are not.
func TestLooksLikeCredential(t *testing.T) {
	for _, ref := range []string{
		pastedToken,
		"  " + pastedToken + "  ",
		"WWBB_" + strings.Repeat("a", 10),
		"token-" + strings.Repeat("0f", 16),
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.c2lnbmF0dXJl",
	} {
		if !LooksLikeCredential(ref) {
			t.Errorf("%q was taken for a secret name", ref)
		}
	}
	for _, ref := range []string{
		"wiki", "wiki_read_token", "WIKI_READ_TOKEN", "atlas_connector_wikiwikibangbang_read_token",
		"wwbb_", "release-2026-10", "eyJ-not-a-jwt",
	} {
		if LooksLikeCredential(ref) {
			t.Errorf("the secret name %q was taken for a credential", ref)
		}
	}
	if got := SafeRef(pastedToken); strings.Contains(got, "fe9204") {
		t.Errorf("SafeRef kept the credential: %q", got)
	}
	if got := SafeRef("wiki"); got != "wiki" {
		t.Errorf("SafeRef changed a secret name: %q", got)
	}
}

// TestAPastedTokenIsRefusedAndNeverShown: saving a source whose credentialsRef is
// a token is refused without quoting it; one stored before the refusal existed is
// listed and read without it, flagged for replacement, and opening it names the
// fault without the value — the message a product manager reads on the catalogue
// screen. Nothing of the token reaches the audit trail.
func TestAPastedTokenIsRefusedAndNeverShown(t *testing.T) {
	f := wiki(t)
	reg := registry(t, f)
	svc := NewService(reg, func() int64 { return 5 })
	sink := &lockedSink{}
	if err := logging.Setup(sink, logging.FormatJSON, logging.DefaultLevel); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logging.Setup(os.Stderr, logging.DefaultFormat, logging.DefaultLevel) })
	do := func(h http.HandlerFunc, method, body, id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/x", strings.NewReader(body))
		if id != "" {
			req.SetPathValue("id", id)
		}
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec
	}
	noToken := func(what, body string) {
		t.Helper()
		if strings.Contains(body, "fe9204b0450ab0eab30cd7dda81a2bbbb344a10cb50a3b0274906a14b088ebd8") {
			t.Errorf("%s carries the token: %s", what, body)
		}
	}

	pasted := `{"id":"pasted","name":"P","kind":"wikiwikibangbang","baseURL":"https://p.example.org","credentialsRef":"` + pastedToken + `"}`
	rec := do(svc.HandleCreate, "POST", pasted, "")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "looks like a token") {
		t.Errorf("create with a pasted token = %d %s", rec.Code, rec.Body)
	}
	noToken("the refused create", rec.Body.String())
	pastedUpdate := `{"name":"W","kind":"wikiwikibangbang","baseURL":"https://p.example.org","credentialsRef":"` + pastedToken + `"}`
	rec = do(svc.HandleUpdate, "PUT", pastedUpdate, "wiki")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "looks like a token") {
		t.Errorf("update to a pasted token = %d %s", rec.Code, rec.Body)
	}
	noToken("the refused update", rec.Body.String())

	// A source stored before the refusal, with the token in its reference.
	legacy := testSource(f)
	legacy.ID, legacy.CredentialsRef = "legacy", pastedToken
	var err error
	reg.loop.Do(func() { err = reg.store.Save(legacy) })
	if err != nil {
		t.Fatal(err)
	}
	rec = do(svc.HandleList, "GET", "", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"credentialsRefRedacted":true`) {
		t.Errorf("list with a legacy source = %d %s", rec.Code, rec.Body)
	}
	noToken("the list", rec.Body.String())
	rec = do(svc.HandleGet, "GET", "", "legacy")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"credentialsRefRedacted":true`) {
		t.Errorf("get of a legacy source = %d %s", rec.Code, rec.Body)
	}
	noToken("the read", rec.Body.String())
	if strings.Contains(do(svc.HandleGet, "GET", "", "wiki").Body.String(), "credentialsRefRedacted") {
		t.Error("a source naming its secret was flagged")
	}
	rec = do(svc.HandleCheck, "POST", "", "legacy")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "looks like a token") {
		t.Errorf("check of a legacy source = %d %s", rec.Code, rec.Body)
	}
	noToken("the check", rec.Body.String())

	// Refused even when a secret happens to carry the token as its name.
	reg.secret = func(string) string { return "anything" }
	if _, _, err := reg.Open("legacy"); err == nil || !strings.Contains(err.Error(), "looks like a token") {
		t.Errorf("open of a legacy source = %v", err)
	} else {
		noToken("the open error", err.Error())
	}

	// Replacing the reference with a name is accepted and the flag goes.
	fixed := `{"name":"L","kind":"wikiwikibangbang","baseURL":"` + legacy.BaseURL + `","credentialsRef":"wiki"}`
	if rec := do(svc.HandleUpdate, "PUT", fixed, "legacy"); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "credentialsRefRedacted") {
		t.Errorf("update to a secret name = %d %s", rec.Code, rec.Body)
	}
	noToken("the audit trail", sink.String())
	if !strings.Contains(sink.String(), `"credentials_ref":"wiki"`) {
		t.Errorf("the audit trail did not name the secret: %s", sink.String())
	}
}

type lockedSink struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *lockedSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *lockedSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

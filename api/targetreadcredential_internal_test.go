package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/panorama"
)

// A deployment target's credential does two jobs, and they are disjoint by design: a promotion
// needs the import route, and a read of the peer needs its descriptor and — at the estate
// altitude (ADR-0402) — its derived landscape. A token carries one
// scope, so one reference cannot do both: measured against two installations, a deploy token
// answers 401 at the two read routes and a landscape credential answers 403 at the import
// route. So a target may name a second reference for reads.
//
// What these tests hold is the fallback and the split: a target with one reference behaves
// exactly as it did before, and a target with two presents the right one to each side.

// TestOneReferenceStillServesBothJobs is the compatibility half. Every target configured before
// this change names one credential, and nothing about it may move.
func TestOneReferenceStillServesBothJobs(t *testing.T) {
	only := deploymentTarget{ID: "t1", Name: "Geneva", CredentialRef: "vault://peer/geneva"}
	if got := only.readRef(); got != "vault://peer/geneva" {
		t.Errorf("readRef = %q, want the promotion credential where no read one is configured", got)
	}
	none := deploymentTarget{ID: "t2", Name: "Bern"}
	if got := none.readRef(); got != "" {
		t.Errorf("readRef = %q on a target that names no credential at all, want none", got)
	}
	// Whitespace is not a configured reference: an operator who cleared the field left it
	// empty, and a target that presented " " would fail every read with a puzzle.
	blank := deploymentTarget{ID: "t3", CredentialRef: "vault://peer/x", ReadCredentialRef: "   "}
	if got := blank.readRef(); got != "vault://peer/x" {
		t.Errorf("readRef = %q for a blanked read reference, want the fallback", got)
	}
}

// TestTheReadReferenceIsWhatAPeerReadPresents is the split. The estate read is the widest of
// the three reads, so it is the one held here; the landscape's target rows and the observation
// projection resolve the same reference through the same helper.
func TestTheReadReferenceIsWhatAPeerReadPresents(t *testing.T) {
	var presented string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/api/v1/node":
			_, _ = w.Write([]byte(servingDescriptor))
		default:
			_ = json.NewEncoder(w).Encode(peerGraph("rt-geneva", 7, 0))
		}
	}))
	defer peer.Close()

	s, done := bootAPIWithModels(t, t.TempDir(), false)
	defer done()
	// Two references, two values: what a promotion would present, and what a read does.
	for name, value := range map[string]string{
		"peer-deploy": "deploy-secret", "peer-read": "read-secret",
	} {
		if _, err := s.vault.Set(name, value); err != nil {
			t.Fatalf("seed the vault: %v", err)
		}
	}
	if err := s.targets.Save(deploymentTarget{
		ID: "t-geneva", Name: "Geneva", BaseURL: peer.URL,
		CredentialRef: "peer-deploy", ReadCredentialRef: "peer-read",
	}); err != nil {
		t.Fatalf("save target: %v", err)
	}
	s.forgetLandscape()

	g := estateOf(t, s, &httpapi.Principal{UserID: "usr_a", Username: "a"})
	if got := estateDomainNode(t, g, "t-geneva"); got.State != panorama.StateHealthy || got.Holds != 7 {
		t.Fatalf("the peer domain = %+v, want it read with the read credential", got)
	}
	if presented != "Bearer read-secret" {
		t.Errorf("the peer was presented %q, want the read credential — the promotion one "+
			"is refused both read routes", presented)
	}
}

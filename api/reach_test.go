package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The minting half of ADR-0410, over the real HTTP path.
//
// A reach that only the code knows about would be a mechanism nobody can use. Three rules
// belong at the door rather than at the read: the landscape credential must state a reach,
// a minter cannot grant a reach they do not hold, and an operator can see afterwards what a
// credential reaches.

// TestALandscapeCredentialMustStateItsReach is the fail-closed rule, placed where it costs
// nothing to obey: a credential that would otherwise see every project is refused at minting
// rather than narrowed at every read. ADR-0410 scopes the rule here so that every credential
// already in the field — none of which states a reach — keeps working.
func TestALandscapeCredentialMustStateItsReach(t *testing.T) {
	ts, admin := apiTokenServer(t)

	code, body := cReq(t, admin, ts, "POST", "/api/v1/api-tokens",
		`{"name":"peer-without-a-reach","scope":"landscape"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("minting a landscape token with no reach: got %d (%s), want 400", code, body)
	}
	if !strings.Contains(string(body), "reach") {
		t.Errorf("the refusal does not say what is missing: %s", body)
	}

	// An ordinary scope is unaffected: the rule is about the landscape read's width.
	if code, body := cReq(t, admin, ts, "POST", "/api/v1/api-tokens",
		`{"name":"worker","scope":"worker"}`); code != http.StatusOK {
		t.Fatalf("minting a worker token: got %d (%s), want 200", code, body)
	}
}

// TestAMinterCannotGrantAReachItDoesNotHold is the rule ADR-0410 leaves implicit and the code
// must not: a credential is never more privileged than whoever created it, which is already
// true of its roles (they are snapshotted at mint time) and has to be true of its reach for
// the same reason. Without it, minting a token would be the escalation, one step removed.
func TestAMinterCannotGrantAReachItDoesNotHold(t *testing.T) {
	ts, admin := apiTokenServer(t)

	code, body := cReq(t, admin, ts, "POST", "/api/v1/api-tokens",
		`{"name":"peer","scope":"landscape","reach":["no-such-project"]}`)
	if code != http.StatusBadRequest {
		t.Fatalf("minting a reach naming an unknown project: got %d (%s), want 400", code, body)
	}
	if !strings.Contains(string(body), "no-such-project") {
		t.Errorf("the refusal does not name the project it refused: %s", body)
	}
}

// TestACredentialSaysWhatItReaches closes the loop for an operator. A reach that is invisible
// after minting is a reach nobody can audit, and the listing is where the scope is already
// shown for exactly that reason.
func TestACredentialSaysWhatItReaches(t *testing.T) {
	ts, admin := apiTokenServer(t)
	project := createProjectAs(t, admin, ts.URL, "Finance")

	code, body := cReq(t, admin, ts, "POST", "/api/v1/api-tokens",
		`{"name":"finance-peer","scope":"landscape","reach":["`+project+`"]}`)
	if code != http.StatusOK {
		t.Fatalf("minting a landscape token with a held reach: got %d (%s)", code, body)
	}

	code, list := cReq(t, admin, ts, "GET", "/api/v1/api-tokens", "")
	if code != http.StatusOK {
		t.Fatalf("listing tokens: got %d (%s)", code, list)
	}
	var tokens []struct {
		Name  string   `json:"name"`
		Scope string   `json:"scope"`
		Reach []string `json:"reach,omitempty"`
	}
	if err := json.Unmarshal(list, &tokens); err != nil {
		t.Fatalf("decode token list %s: %v", list, err)
	}
	for _, tok := range tokens {
		if tok.Name != "finance-peer" {
			continue
		}
		if len(tok.Reach) != 1 || tok.Reach[0] != project {
			t.Errorf("the listing says the credential reaches %v, want [%s]", tok.Reach, project)
		}
		return
	}
	t.Errorf("the minted credential is not in the listing: %s", list)
}

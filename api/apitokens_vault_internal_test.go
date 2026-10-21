package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Minting an API token straight into the vault (ADR-0194, amendment 2026-10-21).
//
// The shop's system processes reach Atlas with authSecret="atlas" (ADR-0411), so the
// token they need has to end up as the vault secret of that name. The route that mints
// one used to hand the secret to the caller, who then pasted it into PUT
// /api/v1/secrets/atlas — the secret crossed the wire twice and sat in a clipboard in
// between, for a value no person ever needs to read. With vaultSecret the server seals
// it itself, and the caller never holds it.

// vaultValue reads a vault secret on the loop, which owns the vault.
func vaultValue(t *testing.T, srv *Server, name string) (string, bool) {
	t.Helper()
	var (
		v   string
		ok  bool
		err error
	)
	srv.do(func() { v, ok, err = srv.vault.Get(name) })
	if err != nil {
		t.Fatalf("read vault secret %q: %v", name, err)
	}
	return v, ok
}

// indexedTokens counts what authenticates right now.
func indexedTokens(srv *Server) int {
	srv.apiTokens.mu.RLock()
	defer srv.apiTokens.mu.RUnlock()
	return len(srv.apiTokens.byHash)
}

// TestAPITokenSealedInTheVaultNeverLeavesTheServer is the point of the field: the
// response carries no secret, the vault holds one that authenticates as the token
// just minted, and a REST task naming the secret resolves exactly that value.
func TestAPITokenSealedInTheVaultNeverLeavesTheServer(t *testing.T) {
	srv := newServerForErrors(t)

	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/api-tokens",
		`{"name":"shop system processes","scope":"full","expiresInDays":365,"vaultSecret":"atlas"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("mint into the vault: %d (%s), want 200", code, body)
	}
	if strings.Contains(string(body), apiTokenPrefix) || strings.Contains(string(body), `"token"`) {
		t.Fatalf("the response carries the secret although it was sealed in the vault: %s", body)
	}
	var minted struct {
		ID          string `json:"id"`
		VaultSecret string `json:"vaultSecret"`
	}
	if err := json.Unmarshal(body, &minted); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if minted.ID == "" || minted.VaultSecret != "atlas" {
		t.Fatalf("response = %s, want the token's id and the vault secret it was sealed as", body)
	}

	secret, ok := vaultValue(t, srv, "atlas")
	if !ok || !strings.HasPrefix(secret, apiTokenPrefix) {
		t.Fatalf("vault secret atlas = %q (present %v), want an API token", secret, ok)
	}
	rec, ok := srv.apiTokens.match(secret, time.Now().Unix())
	if !ok || rec.ID != minted.ID {
		t.Fatalf("the sealed secret authenticates as %q (%v), want the minted token %q", rec.ID, ok, minted.ID)
	}
	// What the system processes' REST tasks resolve authSecret="atlas" to (ADR-0411).
	var resolved string
	srv.do(func() { resolved = srv.resolveConnectorSecret("atlas") })
	if resolved != secret {
		t.Error("a REST task naming the secret atlas does not resolve the token just sealed")
	}

	// The listing says where the token went, and still not what it is.
	code, list := serveInternal(t, srv, http.MethodGet, "/api/v1/api-tokens", "", "")
	if code != http.StatusOK {
		t.Fatalf("list: %d (%s)", code, list)
	}
	if strings.Contains(string(list), secret) {
		t.Error("the listing contains the sealed secret")
	}
	if !strings.Contains(string(list), `"vaultSecret":"atlas"`) {
		t.Errorf("the listing does not say which vault secret the token was sealed as: %s", list)
	}
}

// TestAPITokenSealedInTheVaultReplacesTheSecret: rotation is the ordinary case — mint
// a new token under the same name, then revoke the old one — so an existing secret is
// overwritten rather than refused, and revoking the old token leaves the new value.
func TestAPITokenSealedInTheVaultReplacesTheSecret(t *testing.T) {
	srv := newServerForErrors(t)
	mintInto := func() string {
		t.Helper()
		code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/api-tokens",
			`{"name":"shop","scope":"full","expiresInDays":30,"vaultSecret":"atlas"}`, "application/json")
		if code != http.StatusOK {
			t.Fatalf("mint: %d (%s)", code, body)
		}
		var minted struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &minted)
		return minted.ID
	}
	oldID := mintInto()
	oldSecret, _ := vaultValue(t, srv, "atlas")
	newID := mintInto()
	newSecret, _ := vaultValue(t, srv, "atlas")
	if newSecret == oldSecret {
		t.Fatal("the second mint did not replace the vault secret")
	}
	if code, body := serveInternal(t, srv, http.MethodDelete, "/api/v1/api-tokens/"+oldID, "", ""); code != http.StatusNoContent {
		t.Fatalf("revoke the old token: %d (%s)", code, body)
	}
	if got, ok := vaultValue(t, srv, "atlas"); !ok || got != newSecret {
		t.Error("revoking the old token changed the vault secret the new one was sealed as")
	}
	if rec, ok := srv.apiTokens.match(newSecret, time.Now().Unix()); !ok || rec.ID != newID {
		t.Error("the replacement token does not authenticate after the old one was revoked")
	}
}

// TestAPITokenIsNotMintedWhenTheVaultRefuses is the all-or-nothing rule: a token the
// vault would not take is a token nobody holds, so none may exist — not on disk, not
// in the index, and not as a secret in the response.
func TestAPITokenIsNotMintedWhenTheVaultRefuses(t *testing.T) {
	t.Run("no vault configured", func(t *testing.T) {
		srv := newServerWithOptions(t, WithoutVault())
		code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/api-tokens",
			`{"name":"shop","scope":"full","vaultSecret":"atlas"}`, "application/json")
		if code != http.StatusServiceUnavailable || !strings.Contains(string(body), "vault not configured") {
			t.Fatalf("mint into a missing vault: %d (%s), want 503 naming the vault", code, body)
		}
		assertNothingMinted(t, srv, body)
	})

	t.Run("the vault cannot be written", func(t *testing.T) {
		srv := newServerForErrors(t)
		approvalsPathsDirAsFile(t, filepath.Join(srv.dataDir, "vault"))
		code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/api-tokens",
			`{"name":"shop","scope":"full","vaultSecret":"atlas"}`, "application/json")
		if code != http.StatusInternalServerError || !strings.Contains(string(body), "seal api token") {
			t.Fatalf("mint into an unwritable vault: %d (%s), want 500", code, body)
		}
		assertNothingMinted(t, srv, body)
	})

	t.Run("the token store cannot be written", func(t *testing.T) {
		srv := newServerForErrors(t)
		approvalsPathsDirAsFile(t, srv.apiTokenStore.Dir())
		code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/api-tokens",
			`{"name":"shop","scope":"full","vaultSecret":"atlas"}`, "application/json")
		if code != http.StatusInternalServerError {
			t.Fatalf("mint with an unwritable token store: %d (%s), want 500", code, body)
		}
		if indexedTokens(srv) != 0 || strings.Contains(string(body), apiTokenPrefix) {
			t.Errorf("a token exists although its record was never stored: %s", body)
		}
		if _, ok := vaultValue(t, srv, "atlas"); ok {
			t.Error("the vault holds a secret for a token that was never stored")
		}
	})
}

// TestAPITokenSealedInTheVaultSaysWhenTheWorkersCouldNotReload: the registries are
// rebuilt after the token and its secret are both durable, so a rebuild that fails
// cannot undo the mint — and the answer says the token exists rather than reading as
// a refusal somebody retries into a second token.
func TestAPITokenSealedInTheVaultSaysWhenTheWorkersCouldNotReload(t *testing.T) {
	srv := newServerForErrors(t)
	approvalsPathsDirAsFile(t, srv.connectors.Dir())
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/api-tokens",
		`{"name":"shop","scope":"full","vaultSecret":"atlas"}`, "application/json")
	if code != http.StatusInternalServerError || !strings.Contains(string(body), "was minted and sealed as atlas") {
		t.Fatalf("mint with unloadable workers: %d (%s), want 500 saying the token exists", code, body)
	}
	if strings.Contains(string(body), apiTokenPrefix) {
		t.Errorf("the answer carries the sealed secret: %s", body)
	}
	if recs := apiTokensPathsStored(t, srv); len(recs) != 1 || recs[0].VaultSecret != "atlas" {
		t.Errorf("stored tokens = %v, want the one sealed as atlas", recs)
	}
	if _, ok := vaultValue(t, srv, "atlas"); !ok {
		t.Error("the vault lost the secret of a token that exists")
	}
}

// assertNothingMinted checks the three places a refused mint must not have left a
// credential.
func assertNothingMinted(t *testing.T, srv *Server, body []byte) {
	t.Helper()
	if strings.Contains(string(body), apiTokenPrefix) {
		t.Errorf("a refused mint returned a secret: %s", body)
	}
	if recs := apiTokensPathsStored(t, srv); len(recs) != 0 {
		t.Errorf("tokens stored after a refused mint = %v, want none", recs)
	}
	if n := indexedTokens(srv); n != 0 {
		t.Errorf("%d token(s) authenticate after a refused mint", n)
	}
}

// TestAPITokenVaultSecretNameIsChecked: the name is a vault key, and two kinds of
// value are not one an operator chose. A blank one is a field somebody filled in and
// emptied, and a personal-data key name would overwrite a data subject's key —
// silently and irreversibly making their data unreadable (ADR-0314). Both are refused
// before anything is minted.
func TestAPITokenVaultSecretNameIsChecked(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"blank", `{"name":"x","scope":"full","vaultSecret":"   "}`, "vaultSecret"},
		{"a data subject's key", `{"name":"x","scope":"full","vaultSecret":"atlas:personal-data-key:usr_1"}`, "personal-data key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServerForErrors(t)
			code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/api-tokens", tc.body, "application/json")
			if code != http.StatusBadRequest || !strings.Contains(string(body), tc.want) {
				t.Fatalf("mint %s: %d (%s), want 400 naming %q", tc.body, code, body, tc.want)
			}
			assertNothingMinted(t, srv, body)
		})
	}
}

// TestAPITokenWithoutAVaultSecretIsUnchanged: the field is optional, and a request
// without it is answered exactly as before — the secret in the response, nothing in
// the vault.
func TestAPITokenWithoutAVaultSecretIsUnchanged(t *testing.T) {
	srv := newServerForErrors(t)
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/api-tokens",
		`{"name":"ci","scope":"full","expiresInDays":30}`, "application/json")
	if code != http.StatusOK || !strings.Contains(string(body), `"token":"`+apiTokenPrefix) {
		t.Fatalf("plain mint: %d (%s), want 200 with the secret", code, body)
	}
	if strings.Contains(string(body), "vaultSecret") {
		t.Errorf("a token minted without a vault secret names one: %s", body)
	}
	var metas int
	srv.do(func() {
		list, _ := srv.vault.List()
		metas = len(list)
	})
	if metas != 0 {
		t.Errorf("a plain mint wrote %d vault secret(s)", metas)
	}
}

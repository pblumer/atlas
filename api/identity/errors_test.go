package identity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/vault"
	"github.com/pblumer/atlas/limits"
)

// faultySecrets is the vault failing in the ways memSecrets does not: on a read and on
// a delete.
type faultySecrets struct {
	*memSecrets
	getErr, deleteErr error
}

func (f *faultySecrets) Get(name string) (string, bool, error) {
	if f.getErr != nil {
		return "", false, f.getErr
	}
	return f.memSecrets.Get(name)
}

func (f *faultySecrets) Delete(name string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	return f.memSecrets.Delete(name)
}

// corrupt puts bytes that are not a record where a store keeps one.
func corrupt(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// blockWrite makes the next save of the record at path fail while reading it still
// works: a store writes through path+".tmp", and a directory there cannot be opened as
// a file.
func blockWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
}

// noRefresh fails the test if the provider is asked for anything.
func (r *rig) noRefresh(t *testing.T) {
	r.refresh = func(context.Context, Connection, string) (Tokens, error) {
		t.Error("the provider was asked for a token")
		return Tokens{}, errors.New("unexpected refresh")
	}
}

func (r *rig) credential(id string) (credential, bool) {
	raw, ok := r.secrets.data[vault.ConnectionCredentialName(id)]
	var c credential
	_ = json.Unmarshal([]byte(raw), &c)
	return c, ok
}

func TestOpenStoresRefusesADirectoryItCannotCreate(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(dir string) string // returns the directory to open
		want  string
	}{
		"a directory under a regular file": {
			setup: func(dir string) string {
				file := filepath.Join(dir, "file")
				_ = os.WriteFile(file, nil, 0o600)
				return filepath.Join(file, "identity")
			},
			want: "identity: ",
		},
		"an identity store that is a regular file": {
			setup: func(dir string) string {
				_ = os.WriteFile(filepath.Join(dir, "external-identities"), nil, 0o600)
				return dir
			},
			want: "external identity: create dir",
		},
		"a connection store that is a regular file": {
			setup: func(dir string) string {
				_ = os.WriteFile(filepath.Join(dir, "connections"), nil, 0o600)
				return dir
			},
			want: "connection: create dir",
		},
	} {
		got, err := OpenStores(tc.setup(t.TempDir()))
		if err == nil || got != nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: OpenStores = %v, %v; want an error containing %q", name, got, err, tc.want)
		}
	}
}

// A store that cannot be read links nothing: deciding "not linked yet" from a store
// that could not be read could bind one account to two people.
func TestLinkingWithAStoreItCannotReadLinksNothing(t *testing.T) {
	for name, broken := range map[string]func(s *Stores) string{
		"identities":  func(s *Stores) string { return s.identities.FileFor("xid_broken") },
		"connections": func(s *Stores) string { return s.connections.FileFor("con_broken") },
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			corrupt(t, broken(r.svc.stores))
			if _, err := r.svc.Link(aliceLink()); err == nil || !strings.Contains(err.Error(), "decode") {
				t.Fatalf("Link over an unreadable store: %v", err)
			}
			if len(r.secrets.data) != 0 {
				t.Fatalf("a credential was stored for a link that failed: %v", r.secrets.data)
			}
		})
	}
}

// The credential is stored before the connection says it is active, so a vault that
// refuses it leaves no connection at all.
func TestAVaultThatRefusesTheCredentialLeavesNoConnection(t *testing.T) {
	r := newRig(t)
	r.secrets.fail = errors.New("vault sealed")
	if _, err := r.svc.Link(aliceLink()); err == nil || !strings.Contains(err.Error(), "vault sealed") {
		t.Fatalf("err = %v", err)
	}
	if _, conns := r.svc.ForOwner("usr_alice"); len(conns) != 0 {
		t.Fatalf("a connection without a credential: %+v", conns)
	}
}

func TestARelinkThatCannotBeSavedLeavesTheConnectionAsItWas(t *testing.T) {
	t.Run("the identity", func(t *testing.T) {
		r := newRig(t)
		c, _ := r.svc.Link(aliceLink())
		ids, _ := r.svc.ForOwner("usr_alice")
		blockWrite(t, r.svc.stores.identities.FileFor(ids[0].ID))
		r.now = r.now.Add(time.Hour)
		again := aliceLink()
		again.RefreshToken = "rt-2"
		if _, err := r.svc.Link(again); err == nil {
			t.Fatal("relinked although the identity could not be saved")
		}
		got, _, _ := r.svc.Get(c.ID)
		if got.LastInteractiveAuthAt != c.LastInteractiveAuthAt {
			t.Fatalf("the connection moved: %+v", got)
		}
		if cred, _ := r.credential(c.ID); cred.RefreshToken != "rt-1" {
			t.Fatalf("the credential was replaced: %+v", cred)
		}
	})
	t.Run("the connection", func(t *testing.T) {
		r := newRig(t)
		c, _ := r.svc.Link(aliceLink())
		blockWrite(t, r.svc.stores.connections.FileFor(c.ID))
		r.now = r.now.Add(time.Hour)
		again := aliceLink()
		again.RefreshToken = "rt-2"
		if _, err := r.svc.Link(again); err == nil {
			t.Fatal("relinked although the connection could not be saved")
		}
		got, _, _ := r.svc.Get(c.ID)
		if got.LastInteractiveAuthAt != c.LastInteractiveAuthAt || got.Status != StatusActive {
			t.Fatalf("the connection moved: %+v", got)
		}
		// The credential goes first, so the active connection holds the newer one.
		if cred, _ := r.credential(c.ID); cred.RefreshToken != "rt-2" || cred.Generation != 1 {
			t.Fatalf("credential = %+v, want the rotated one", cred)
		}
	})
}

// A user's identities and connections come newest first, and a user with none gets
// empty lists — never null — on the routes.
func TestForOwnerListsNewestFirstAndNeverNull(t *testing.T) {
	r := newRig(t)
	first, _ := r.svc.Link(aliceLink())
	r.now = r.now.Add(time.Hour)
	second := aliceLink()
	second.Subject = "oid-alice-admin"
	newer, _ := r.svc.Link(second)
	ids, conns := r.svc.ForOwner("usr_alice")
	if len(ids) != 2 || ids[0].Subject != "oid-alice-admin" || ids[1].Subject != "oid-alice" {
		t.Fatalf("identities = %+v, want newest first", ids)
	}
	if len(conns) != 2 || conns[0].ID != newer.ID || conns[1].ID != first.ID {
		t.Fatalf("connections = %+v, want newest first", conns)
	}

	ids, conns = r.svc.ForOwner("usr_nobody")
	if ids == nil || conns == nil || len(ids)+len(conns) != 0 {
		t.Fatalf("nobody: %#v %#v", ids, conns)
	}
	admin := &httpapi.Principal{UserID: "usr_admin", Roles: []string{"admin"}, Session: true}
	rec := call(t, r.svc.HandleOfUser, "GET", "/", "", admin, map[string]string{"id": "usr_nobody"})
	if body := rec.Body.String(); rec.Code != 200 || !strings.Contains(body, `"identities":[]`) || !strings.Contains(body, `"connections":[]`) {
		t.Fatalf("nobody's view: %d %s", rec.Code, body)
	}
}

// A connection record that cannot be read is a server error, never "no such
// connection" and never "no connection for this Worker": either would let a broken
// store read as an answer.
func TestAConnectionThatCannotBeReadIsAnErrorNotAMissingOne(t *testing.T) {
	r := newRig(t)
	const id = "con_broken"
	corrupt(t, r.svc.stores.connections.FileFor(id))
	if _, _, err := r.svc.Get(id); err == nil {
		t.Error("Get read an unreadable record")
	}
	if _, err := r.svc.SetAllowedWorkers(id, "usr_alice", []string{"sp-prod"}); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("SetAllowedWorkers: err = %v, want a store error", err)
	}
	if _, err := r.svc.Revoke(id, "usr_alice", true, "x"); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("Revoke: err = %v, want a store error", err)
	}
	if _, ok, err := r.svc.ConnectionFor("usr_alice", ProviderMicrosoft, "sp-prod"); err == nil || ok {
		t.Errorf("ConnectionFor: ok=%v err=%v, want a store error", ok, err)
	}
	alice := &httpapi.Principal{UserID: "usr_alice", Roles: []string{"user"}, Session: true}
	notAdmin := func(*httpapi.Principal) bool { return false }
	if rec := call(t, r.svc.HandleSetWorkers, "PUT", "/", `{"workers":["sp-prod"]}`, alice, map[string]string{"id": id}); rec.Code != http.StatusInternalServerError {
		t.Errorf("set workers: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, r.svc.HandleRevoke(notAdmin), "POST", "/", `{}`, alice, map[string]string{"id": id}); rec.Code != http.StatusInternalServerError {
		t.Errorf("revoke: %d %s", rec.Code, rec.Body)
	}
	// An id that names nothing is a 404 on both routes.
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"set workers": call(t, r.svc.HandleSetWorkers, "PUT", "/", `{"workers":[]}`, alice, map[string]string{"id": "con_ghost"}),
		"revoke":      call(t, r.svc.HandleRevoke(notAdmin), "POST", "/", `{}`, alice, map[string]string{"id": "con_ghost"}),
	} {
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s ghost: %d %s", name, rec.Code, rec.Body)
		}
	}
	if _, err := r.svc.Revoke("con_ghost", "usr_admin", true, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("an administrator revoked a ghost: %v", err)
	}
}

// A vault that will not delete the credential stops the revocation before the record
// says revoked: the connection does not claim a withdrawal that did not happen.
func TestARevocationTheVaultRefusesLeavesTheConnectionAsItWas(t *testing.T) {
	r := newRig(t)
	c, _ := r.svc.Link(aliceLink())
	r.svc.secrets = &faultySecrets{memSecrets: r.secrets, deleteErr: errors.New("vault sealed")}
	if _, err := r.svc.Revoke(c.ID, "usr_alice", false, "done"); err == nil {
		t.Fatal("revoked although the credential could not be deleted")
	}
	if got, _, _ := r.svc.Get(c.ID); got.Status != StatusActive || got.RevokedAt != 0 {
		t.Fatalf("the record changed: %+v", got)
	}
	if _, ok := r.credential(c.ID); !ok {
		t.Fatal("the credential is gone")
	}
}

// A revocation whose record cannot be saved has still deleted the credential, so it
// fails closed: the connection reads as active, but nothing can be brokered through it.
func TestARevocationThatCannotBeRecordedStillLeavesNoCredential(t *testing.T) {
	r := newRig(t)
	c, _ := r.svc.Link(aliceLink())
	blockWrite(t, r.svc.stores.connections.FileFor(c.ID))
	if _, err := r.svc.Revoke(c.ID, "usr_alice", false, "done"); err == nil {
		t.Fatal("revoked although the record could not be saved")
	}
	if _, ok := r.credential(c.ID); ok {
		t.Fatal("the credential outlived a revocation")
	}
	r.noRefresh(t)
	if tok, _, err := r.svc.AccessToken(context.Background(), c.ID); !errors.Is(err, ErrInactive) || tok != "" {
		t.Fatalf("token %q, err %v; want ErrInactive", tok, err)
	}
}

// The broker asks the provider for nothing it cannot use: a missing connection, an
// unreadable one, and a credential the vault does not have or cannot make sense of.
func TestTheBrokerRefusesAConnectionItCannotUse(t *testing.T) {
	for name, tc := range map[string]struct {
		breakIt func(r *rig, id string) string // returns the id to ask for
		is      error
		want    string
	}{
		"an unknown connection": {
			breakIt: func(*rig, string) string { return "con_ghost" },
			is:      ErrNotFound,
		},
		"an unreadable record": {
			breakIt: func(r *rig, id string) string {
				_ = os.WriteFile(r.svc.stores.connections.FileFor(id), []byte("{"), 0o600)
				return id
			},
			want: "decode",
		},
		"no credential in the vault": {
			breakIt: func(r *rig, id string) string {
				delete(r.secrets.data, vault.ConnectionCredentialName(id))
				return id
			},
			is: ErrInactive,
		},
		"a vault that cannot be read": {
			breakIt: func(r *rig, id string) string {
				r.svc.secrets = &faultySecrets{memSecrets: r.secrets, getErr: errors.New("vault sealed")}
				return id
			},
			is: ErrInactive,
		},
		"a credential that is not JSON": {
			breakIt: func(r *rig, id string) string {
				r.secrets.data[vault.ConnectionCredentialName(id)] = "rt-1"
				return id
			},
			want: "unreadable credential",
		},
		"a credential without a refresh token": {
			breakIt: func(r *rig, id string) string {
				r.secrets.data[vault.ConnectionCredentialName(id)] = `{"refreshToken":"","generation":3}`
				return id
			},
			want: "unreadable credential",
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			c, _ := r.svc.Link(aliceLink())
			r.noRefresh(t)
			tok, exp, err := r.svc.AccessToken(context.Background(), tc.breakIt(r, c.ID))
			if err == nil || tok != "" || !exp.IsZero() {
				t.Fatalf("token %q until %v, err %v", tok, exp, err)
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Fatalf("err = %v, want %v", err, tc.is)
			}
			if tc.want != "" && (!strings.Contains(err.Error(), tc.want) || errors.Is(err, ErrInactive) || errors.Is(err, ErrNotFound)) {
				t.Fatalf("err = %v, want one about %q", err, tc.want)
			}
		})
	}
}

// A provider answer without a usable access token releases nothing, records no
// refresh, and caches nothing: the next call asks the provider again.
func TestTheBrokerReleasesNoTokenTheProviderDidNotIssue(t *testing.T) {
	for name, answer := range map[string]Tokens{
		"no access token": {RefreshToken: "rotated", ExpiresIn: 3600},
		"no lifetime":     {AccessToken: "at", RefreshToken: "rotated"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			c, _ := r.svc.Link(aliceLink())
			calls := 0
			r.refresh = func(context.Context, Connection, string) (Tokens, error) {
				calls++
				return answer, nil
			}
			for i := 0; i < 2; i++ {
				if tok, _, err := r.svc.AccessToken(context.Background(), c.ID); err == nil || tok != "" {
					t.Fatalf("released %q (%v)", tok, err)
				}
			}
			if calls != 2 {
				t.Fatalf("the provider was asked %d times, want every call to ask again", calls)
			}
			if got, _, _ := r.svc.Get(c.ID); got.LastRefreshAt != 0 {
				t.Fatalf("a refresh that yielded nothing was recorded: %+v", got)
			}
		})
	}
}

// A record that becomes unreadable while the provider is being asked stops the
// release: the token is never handed out without its refresh being recorded.
func TestTheBrokerReleasesNothingItCouldNotRecord(t *testing.T) {
	r := newRig(t)
	c, _ := r.svc.Link(aliceLink())
	r.refresh = func(context.Context, Connection, string) (Tokens, error) {
		corrupt(t, r.svc.stores.connections.FileFor(c.ID))
		return Tokens{AccessToken: "at", RefreshToken: "rotated", ExpiresIn: 3600}, nil
	}
	tok, _, err := r.svc.AccessToken(context.Background(), c.ID)
	if err == nil || tok != "" || errors.Is(err, ErrInactive) {
		t.Fatalf("token %q, err %v; want a store error", tok, err)
	}
	if cred, _ := r.credential(c.ID); cred.RefreshToken != "rt-1" {
		t.Fatalf("the rotated token was written for a connection that could not be read: %+v", cred)
	}
}

// A provider refusal that arrives after the connection was revoked leaves it revoked:
// marking it for re-authentication would invite the owner to undo a withdrawal.
func TestARefusedRefreshDoesNotUndoARevocation(t *testing.T) {
	r := newRig(t)
	c, _ := r.svc.Link(aliceLink())
	r.refresh = func(context.Context, Connection, string) (Tokens, error) {
		if _, err := r.svc.Revoke(c.ID, "usr_alice", false, "now"); err != nil {
			t.Error(err)
		}
		return Tokens{}, ErrInvalidGrant
	}
	if _, _, err := r.svc.AccessToken(context.Background(), c.ID); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("err = %v", err)
	}
	if got, _, _ := r.svc.Get(c.ID); got.Status != StatusRevoked {
		t.Fatalf("status = %s, want it still revoked", got.Status)
	}
}

func TestTheCallerIsOfferedTheProvidersThisServerLinks(t *testing.T) {
	r := newRig(t)
	opt := LinkOption{Provider: ProviderMicrosoft, Start: "/api/v1/me/identities/microsoft/start"}
	r.svc.SetLinkable(opt)
	alice := &httpapi.Principal{UserID: "usr_alice", Roles: []string{"user"}, Session: true}
	var v view
	rec := call(t, r.svc.HandleMine, "GET", "/", "", alice, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || len(v.Linkable) != 1 || v.Linkable[0] != opt {
		t.Fatalf("mine: %d %s", rec.Code, rec.Body)
	}
	// What the server can link is the caller's own business, not an administrator's
	// view of somebody else.
	rec = call(t, r.svc.HandleOfUser, "GET", "/", "", alice, map[string]string{"id": "usr_alice"})
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || len(v.Linkable) != 0 {
		t.Fatalf("of user: %d %s", rec.Code, rec.Body)
	}
}

// A body is read within the Request budget — an unset budget meaning the defaults, not
// nothing — and an empty body is no body at all, not invalid JSON.
func TestARequestBodyIsReadWithinTheRequestBudget(t *testing.T) {
	r := newRig(t)
	c, _ := r.svc.Link(aliceLink())
	alice := &httpapi.Principal{UserID: "usr_alice", Roles: []string{"user"}, Session: true}
	id := map[string]string{"id": c.ID}

	r.svc.Limits = limits.Limits{}
	if rec := call(t, r.svc.HandleSetWorkers, "PUT", "/", `{"workers":["sp-prod"]}`, alice, id); rec.Code != 200 {
		t.Fatalf("unset limits: %d %s", rec.Code, rec.Body)
	}
	r.svc.Limits = limits.Default()
	r.svc.Limits.Request = 8
	if rec := call(t, r.svc.HandleSetWorkers, "PUT", "/", `{"workers":["sp-test"]}`, alice, id); rec.Code != http.StatusBadRequest {
		t.Fatalf("a body over the budget: %d %s", rec.Code, rec.Body)
	}
	if got, _, _ := r.svc.Get(c.ID); len(got.AllowedWorkers) != 1 || got.AllowedWorkers[0] != "sp-prod" {
		t.Fatalf("a body over the budget changed the connection: %v", got.AllowedWorkers)
	}
	r.svc.Limits = limits.Default()

	req := httptest.NewRequest("PUT", "/", iotest.ErrReader(errors.New("connection reset")))
	req = req.WithContext(httpapi.WithPrincipal(req.Context(), alice))
	req.SetPathValue("id", c.ID)
	rec := httptest.NewRecorder()
	r.svc.HandleSetWorkers(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "connection reset") {
		t.Fatalf("an unreadable body: %d %s", rec.Code, rec.Body)
	}

	notAdmin := func(*httpapi.Principal) bool { return false }
	if rec := call(t, r.svc.HandleRevoke(notAdmin), "POST", "/", "  ", alice, id); rec.Code != 200 {
		t.Fatalf("revoke without a body: %d %s", rec.Code, rec.Body)
	}
	if got, _, _ := r.svc.Get(c.ID); got.Status != StatusRevoked || got.RevokeReason != "" {
		t.Fatalf("revoked = %+v", got)
	}
}

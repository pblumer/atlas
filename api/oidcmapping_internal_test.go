package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// What a provider's claims are allowed to decide here
// (ADR-draft-federated-authentication, step two).
//
// The unit half: reading a claim out of a token, and turning what it says into
// roles and groups. The half that matters most is what happens when the claim says
// nothing — a mapping that grants on absence would be a mapping that grants to
// anybody the provider lets in.

// TestAClaimIsReadWhereverItIs. Providers put the same information in different
// places: a flat "groups", Keycloak's "realm_access.roles", a single string when
// there is one value. All three are the same question.
func TestAClaimIsReadWhereverItIs(t *testing.T) {
	raw := map[string]any{}
	if err := json.Unmarshal([]byte(`{
		"groups": ["atlas-modeller", "atlas-betrieb"],
		"role": "atlas-admin",
		"realm_access": {"roles": ["offline_access", "atlas-modeller"]},
		"nested": {"deep": {"claim": "one"}},
		"numbers": [1, 2],
		"object": {"a": "b"},
		"blank": "",
		"blanks": ["", "atlas-admin", ""]
	}`), &raw); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	for _, tc := range []struct {
		name, path string
		want       string
	}{
		{"a list", "groups", "atlas-modeller atlas-betrieb"},
		{"a single string", "role", "atlas-admin"},
		{"a nested list", "realm_access.roles", "offline_access atlas-modeller"},
		{"a deeply nested string", "nested.deep.claim", "one"},
		{"a claim that is not there", "missing", ""},
		{"a path through something that is not an object", "role.nope", ""},
		{"values that are not strings", "numbers", ""},
		{"a claim that is an object", "object", ""},
		{"a claim that is the empty string", "blank", ""},
		{"blanks in a list are not values", "blanks", "atlas-admin"},
		{"no path at all", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := strings.Join(claimValues(raw, tc.path), " "); got != tc.want {
				t.Errorf("claimValues(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
	if got := claimValues(nil, "groups"); len(got) != 0 {
		t.Errorf("claimValues of nothing = %v, want none", got)
	}
}

// TestAMappingGrantsOnlyWhatItMatches. The rules are exact-value matches, and a
// person the provider says nothing about gets nothing from them.
func TestAMappingGrantsOnlyWhatItMatches(t *testing.T) {
	m := oidcMapping{
		Enabled: true,
		Claim:   "groups",
		Rules: []oidcMapRule{
			{Value: "atlas-modeller", Roles: []string{RoleModeler}, Groups: []string{"grp_model"}},
			{Value: "atlas-betrieb", Roles: []string{RoleOperator}},
			{Value: "atlas-admin", Roles: []string{RoleAdmin}, Groups: []string{"grp_model"}},
		},
	}
	for _, tc := range []struct {
		name          string
		values        []string
		roles, groups string
	}{
		{"one rule", []string{"atlas-betrieb"}, "operator", ""},
		{"two rules union", []string{"atlas-modeller", "atlas-betrieb"}, "modeler operator", "grp_model"},
		{"the same group twice is one", []string{"atlas-modeller", "atlas-admin"}, "modeler admin", "grp_model"},
		{"a value nobody mapped", []string{"finance-all"}, "", ""},
		{"nothing at all", nil, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			roles, groups := m.apply(tc.values)
			if got := strings.Join(roles, " "); got != tc.roles {
				t.Errorf("roles = %q, want %q", got, tc.roles)
			}
			if got := strings.Join(groups, " "); got != tc.groups {
				t.Errorf("groups = %q, want %q", got, tc.groups)
			}
		})
	}
}

// TestAMappingIsRefusedWhenItCannotMeanAnything. A rule naming a role Atlas does
// not enforce, or a group that does not exist, is a rule that grants nothing —
// silently, and forever. It is refused where it is written instead.
func TestAMappingIsRefusedWhenItCannotMeanAnything(t *testing.T) {
	exists := func(id string) bool { return id == "grp_real" }
	for _, tc := range []struct {
		name string
		m    oidcMapping
		says string
	}{
		{"a role nobody enforces",
			oidcMapping{Enabled: true, Claim: "groups", Rules: []oidcMapRule{{Value: "x", Roles: []string{"wizard"}}}},
			"wizard"},
		{"a group that does not exist",
			oidcMapping{Enabled: true, Claim: "groups", Rules: []oidcMapRule{{Value: "x", Groups: []string{"grp_gone"}}}},
			"grp_gone"},
		{"enabled with no claim to read",
			oidcMapping{Enabled: true, Rules: []oidcMapRule{{Value: "x", Roles: []string{RoleUser}}}},
			"claim"},
		{"a rule matching nothing",
			oidcMapping{Enabled: true, Claim: "groups", Rules: []oidcMapRule{{Roles: []string{RoleUser}}}},
			"value"},
		{"a rule granting nothing",
			oidcMapping{Enabled: true, Claim: "groups", Rules: []oidcMapRule{{Value: "x"}}},
			"grants nothing"},
		{"switched on with no rules at all",
			oidcMapping{Enabled: true, Claim: "groups"},
			"at least one rule"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.m.validate(exists)
			if err == nil {
				t.Fatal("accepted a mapping that cannot mean anything")
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("error = %q, want it to name %q", err, tc.says)
			}
		})
	}

	ok := oidcMapping{Enabled: true, Claim: "realm_access.roles", Rules: []oidcMapRule{
		{Value: "atlas-modeller", Roles: []string{RoleModeler}, Groups: []string{"grp_real"}},
	}}
	if err := ok.validate(exists); err != nil {
		t.Errorf("a usable mapping was refused: %v", err)
	}
	// Switched off, nothing is asked of it: an operator may keep a draft mapping
	// that names a group they are about to create.
	off := oidcMapping{Rules: []oidcMapRule{{Value: "x", Groups: []string{"grp_gone"}}}}
	if err := off.validate(exists); err != nil {
		t.Errorf("a disabled mapping was refused: %v", err)
	}
}

// TestTheUserRoleIsAFloorAndNotAGrant. What the mapping decides is `admin`,
// `modeler` and `operator` — the roles that change what somebody may do to the
// instance. `user` is what anybody who can sign in at all has, so losing a group
// at the provider does not leave a person unable to see their own task list.
func TestTheUserRoleIsAFloorAndNotAGrant(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mapped []string
		want   string
	}{
		{"nothing mapped", nil, "user"},
		{"a role mapped", []string{RoleModeler}, "modeler user"},
		{"user mapped explicitly is not doubled", []string{RoleUser, RoleOperator}, "user operator"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := strings.Join(withUserFloor(tc.mapped), " "); got != tc.want {
				t.Errorf("withUserFloor(%v) = %q, want %q", tc.mapped, got, tc.want)
			}
		})
	}
}

// TestAMappingOwnsOnlyTheGroupsItNames. Roles are a closed set Atlas defines, so
// the mapping deciding them is a complete statement; groups are an open set people
// create for their own reasons, and a mapping that never mentions one has said
// nothing about it.
func TestAMappingOwnsOnlyTheGroupsItNames(t *testing.T) {
	m := oidcMapping{Rules: []oidcMapRule{
		{Value: "a", Groups: []string{"grp_1", "grp_2"}},
		{Value: "b", Roles: []string{RoleOperator}},
		{Value: "c", Groups: []string{"grp_2", "grp_3"}},
	}}
	if got := strings.Join(m.namedGroups(), " "); got != "grp_1 grp_2 grp_3" {
		t.Errorf("namedGroups = %q, want the three it names once each", got)
	}
	if got := (oidcMapping{}).namedGroups(); len(got) != 0 {
		t.Errorf("a mapping with no rules names %v, want nothing", got)
	}
}

// The end-to-end half: a real login against a provider that puts groups in a
// claim, and what the account looks like afterwards.

// mapClaims makes the provider issue tokens carrying a "groups" claim.
func mapClaims(idp *fakeIdP, groups ...string) {
	idp.mu.Lock()
	defer idp.mu.Unlock()
	if groups == nil {
		idp.mutate = nil
		return
	}
	list := make([]any, 0, len(groups))
	for _, g := range groups {
		list = append(list, g)
	}
	idp.mutate = func(_, claims map[string]any) { claims["groups"] = list }
}

// makeGroup creates a group directly, the way an administrator would have before
// any of this ran.
func makeGroup(t *testing.T, srv *Server, name string, members ...string) string {
	t.Helper()
	id, err := newGroupID()
	if err != nil {
		t.Fatalf("group id: %v", err)
	}
	srv.do(func() {
		err = srv.groups.Save(group{ID: id, Name: name, Members: members, CreatedAt: 1, UpdatedAt: 1})
	})
	if err != nil {
		t.Fatalf("save group %s: %v", name, err)
	}
	return id
}

// setMapping stores a mapping the way the admin endpoint would.
func setMapping(t *testing.T, srv *Server, m oidcMapping) {
	t.Helper()
	var err error
	srv.do(func() { err = srv.settings.saveOIDCMapping(m) })
	if err != nil {
		t.Fatalf("save mapping: %v", err)
	}
}

// groupNames is the names of the groups a user is in, sorted, so an assertion can
// name what it expects instead of an opaque id.
func groupNames(t *testing.T, srv *Server, userID string) string {
	t.Helper()
	var (
		all []group
		err error
	)
	srv.do(func() { all, err = srv.groups.LoadAll() })
	if err != nil {
		t.Fatalf("load groups: %v", err)
	}
	var names []string
	for _, g := range all {
		if g.hasMember(userID) {
			names = append(names, g.Name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, " ")
}

// TestAClaimDecidesRolesAndGroups is the measure: a person's groups at the
// provider decide what they may do here, so onboarding is a membership somebody
// already maintains.
func TestAClaimDecidesRolesAndGroups(t *testing.T) {
	idp := newFakeIdP(t)
	ts, srv := oidcServer(t, idp)
	modelling := makeGroup(t, srv, "Modelling")
	operations := makeGroup(t, srv, "Operations")
	setMapping(t, srv, oidcMapping{
		Enabled: true, Claim: "groups",
		Rules: []oidcMapRule{
			{Value: "atlas-modeller", Roles: []string{RoleModeler}, Groups: []string{modelling}},
			{Value: "atlas-betrieb", Roles: []string{RoleOperator}, Groups: []string{operations}},
		},
	})
	mapClaims(idp, "atlas-modeller", "finance-all")

	resp := federate(t, ts, idp, noRedirects(t))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/" {
		t.Fatalf("callback = %d %q, want 302 /", resp.StatusCode, resp.Header.Get("Location"))
	}
	u, ok := accountBySubject(t, srv, "subject-1")
	if !ok {
		t.Fatal("no account for the subject that just signed in")
	}
	if got := strings.Join(u.Roles, " "); got != "modeler user" {
		t.Errorf("roles = %q, want %q", got, "modeler user")
	}
	if got := groupNames(t, srv, u.ID); got != "Modelling" {
		t.Errorf("groups = %q, want %q — the rule that did not match granted nothing", got, "Modelling")
	}
}

// TestAClaimThatGoesAwayTakesItsGrantsWithIt. Offboarding is the whole reason to
// federate: the membership going away at the provider is the revocation here.
func TestAClaimThatGoesAwayTakesItsGrantsWithIt(t *testing.T) {
	idp := newFakeIdP(t)
	ts, srv := oidcServer(t, idp)
	ops := makeGroup(t, srv, "Operations")
	byHand := makeGroup(t, srv, "Weihnachtsfeier")
	setMapping(t, srv, oidcMapping{
		Enabled: true, Claim: "groups",
		Rules: []oidcMapRule{{Value: "atlas-betrieb", Roles: []string{RoleOperator}, Groups: []string{ops}}},
	})
	mapClaims(idp, "atlas-betrieb")

	c := noRedirects(t)
	federate(t, ts, idp, c).Body.Close()
	u, ok := accountBySubject(t, srv, "subject-1")
	if !ok {
		t.Fatal("no account after the first login")
	}
	// An administrator adds them to a group the mapping says nothing about.
	srv.do(func() {
		g, found, err := srv.groups.Get(byHand)
		if err != nil || !found {
			t.Errorf("get group: %v (found %v)", err, found)
			return
		}
		g.Members = append(g.Members, u.ID)
		if err := srv.groups.Save(g); err != nil {
			t.Errorf("save: %v", err)
		}
	})

	// The next day they are out of the operations group at the provider.
	mapClaims(idp, "finance-all")
	federate(t, ts, idp, noRedirects(t)).Body.Close()

	u, _ = accountBySubject(t, srv, "subject-1")
	if got := strings.Join(u.Roles, " "); got != "user" {
		t.Errorf("roles = %q, want %q — the claim is gone", got, "user")
	}
	if got := groupNames(t, srv, u.ID); got != "Weihnachtsfeier" {
		t.Errorf("groups = %q, want only the group no rule names", got)
	}
}

// TestAMappingThatIsOffDecidesNothing. It is off until an operator turns it on,
// and an upgrade into this version does not quietly hand the provider the roles.
func TestAMappingThatIsOffDecidesNothing(t *testing.T) {
	idp := newFakeIdP(t)
	ts, srv := oidcServer(t, idp)
	admins := makeGroup(t, srv, "Admins")
	setMapping(t, srv, oidcMapping{
		Claim: "groups",
		Rules: []oidcMapRule{{Value: "atlas-admin", Roles: []string{RoleAdmin}, Groups: []string{admins}}},
	})
	mapClaims(idp, "atlas-admin")

	federate(t, ts, idp, noRedirects(t)).Body.Close()
	u, ok := accountBySubject(t, srv, "subject-1")
	if !ok {
		t.Fatal("no account after the login")
	}
	if got := strings.Join(u.Roles, " "); got != "user" {
		t.Errorf("roles = %q, want %q — the mapping is switched off", got, "user")
	}
	if got := groupNames(t, srv, u.ID); got != "" {
		t.Errorf("groups = %q, want none", got)
	}
}

// TestAMappedRoleReachesTheRouteItNames. The point of the roles is what they open,
// so the assertion is a request that would be refused without them and is not.
func TestAMappedRoleReachesTheRouteItNames(t *testing.T) {
	idp := newFakeIdP(t)
	ts, srv := oidcServer(t, idp)
	setMapping(t, srv, oidcMapping{
		Enabled: true, Claim: "realm_access.roles",
		Rules: []oidcMapRule{{Value: "atlas-modeller", Roles: []string{RoleModeler}}},
	})
	idp.mu.Lock()
	idp.mutate = func(_, claims map[string]any) {
		claims["realm_access"] = map[string]any{"roles": []any{"offline_access", "atlas-modeller"}}
	}
	idp.mu.Unlock()

	c := noRedirects(t)
	federate(t, ts, idp, c).Body.Close()

	// Exactly `modeler`: the route that names it opens, and the routes naming the
	// two roles the claim did not grant stay shut.
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/api/v1/repository/installed", http.StatusOK}, // modeler
		{"/api/v1/workers", http.StatusForbidden},       // operator
		{"/api/v1/users", http.StatusForbidden},         // admin
	} {
		resp, err := c.Get(ts.URL + tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("GET %s = %d, want %d for an account the claim made a modeler",
				tc.path, resp.StatusCode, tc.want)
		}
	}
}

// TestTheMappingEndpointIsAdminOnlyAndChecked. The validation is at the write, so
// an operator finds out here rather than from a colleague who cannot deploy — and
// the rules, which name the provider's group identifiers, are not readable by
// anybody else.
func TestTheMappingEndpointIsAdminOnlyAndChecked(t *testing.T) {
	idp := newFakeIdP(t)
	ts, srv := oidcServer(t, idp)
	real := makeGroup(t, srv, "Modelling")

	admin := adminClient(t, ts)
	put := func(body string) (int, string) { return mappingPut(t, admin, ts, body) }

	// Before anybody has written one: the zero value, with an empty rule list rather
	// than a null the Console would have to special-case.
	if code, body := mappingGet(t, admin, ts); code != http.StatusOK {
		t.Fatalf("GET on a fresh instance = %d %s, want 200", code, body)
	} else if body := strings.ReplaceAll(body, " ", ""); !strings.Contains(body, `"rules":[]`) {
		t.Errorf("an unwritten mapping reads as %s, want an empty rule list", body)
	}

	// A body that cannot be read at all is a refusal, not a mapping of nothing.
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, withAdminSession(t, srv,
		httptest.NewRequest(http.MethodPut, "/api/v1/settings/oidc-mapping", errReader{})))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("PUT with an unreadable body = %d, want 400", rec.Code)
	}

	if code, body := put(`{"enabled":true,"claim":"groups","rules":[{"value":"x","groups":["grp_nope"]}]}`); code != http.StatusBadRequest {
		t.Errorf("a mapping naming a group that does not exist = %d %s, want 400", code, body)
	} else if !strings.Contains(body, "grp_nope") {
		t.Errorf("refusal does not name the group: %s", body)
	}
	if code, body := put(`{"enabled":true,"claim":"groups","rules":[{"value":"x","roles":["wizard"]}]}`); code != http.StatusBadRequest {
		t.Errorf("a mapping naming a role nobody enforces = %d %s, want 400", code, body)
	}
	if code, body := put(`not json`); code != http.StatusBadRequest {
		t.Errorf("a body that is not JSON = %d %s, want 400", code, body)
	}
	good := `{"enabled":true,"claim":"groups","rules":[{"value":"atlas-modeller","roles":["modeler"],"groups":["` + real + `"]}]}`
	if code, body := put(good); code != http.StatusOK {
		t.Fatalf("a usable mapping = %d %s, want 200", code, body)
	}

	// It reads back as it was written, and nobody else may read it: the rules name
	// the provider's group identifiers.
	code, body := mappingGet(t, admin, ts)
	if code != http.StatusOK {
		t.Fatalf("GET = %d %s, want 200", code, body)
	}
	var got oidcMapping
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if !got.Enabled || got.Claim != "groups" || len(got.Rules) != 1 || got.Rules[0].Value != "atlas-modeller" {
		t.Errorf("read back %+v, want what was written", got)
	}

	anon := noRedirects(t)
	out, err := anon.Get(ts.URL + "/api/v1/settings/oidc-mapping")
	if err != nil {
		t.Fatalf("anonymous get: %v", err)
	}
	defer out.Body.Close()
	if out.StatusCode != http.StatusUnauthorized {
		t.Errorf("anonymous read = %d, want 401", out.StatusCode)
	}
}

// TestARolesListIsCompared. What decides whether a login rewrites the account
// record, so it has to notice both shapes of difference.
func TestARolesListIsCompared(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b []string
		want bool
	}{
		{"the same list", []string{RoleModeler, RoleUser}, []string{RoleModeler, RoleUser}, true},
		{"a different length", []string{RoleUser}, []string{RoleModeler, RoleUser}, false},
		{"a different member", []string{RoleModeler, RoleUser}, []string{RoleOperator, RoleUser}, false},
		{"a different order", []string{RoleUser, RoleModeler}, []string{RoleModeler, RoleUser}, false},
		{"nothing at all", nil, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameRoles(tc.a, tc.b); got != tc.want {
				t.Errorf("sameRoles(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestAMappingThatCannotBeReadIsNotAMappingThatIsOff. The two states have to stay
// distinguishable: a mapping nobody wrote grants nothing on purpose, and a mapping
// that cannot be read is a broken instance. Reading the second as the first would
// mean a disk fault silently un-mapping everybody at their next login.
func TestAMappingThatCannotBeReadIsNotAMappingThatIsOff(t *testing.T) {
	idp := newFakeIdP(t)
	ts, srv := oidcServer(t, idp)
	if err := os.WriteFile(srv.settings.oidcFile, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write mapping file: %v", err)
	}

	resp := federate(t, ts, idp, noRedirects(t))
	defer resp.Body.Close()
	if got := resp.Header.Get("Location"); got != "/"+oidcFailedQuery {
		t.Errorf("Location = %q, want the login screen", got)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == sessionCookie && ck.Value != "" {
			t.Error("a login that could not read the mapping produced a session")
		}
	}
	if _, ok := accountBySubject(t, srv, "subject-1"); ok {
		t.Error("an account was created against a mapping that could not be read")
	}

	admin := adminClient(t, ts)
	code, body := mappingGet(t, admin, ts)
	if code != http.StatusInternalServerError {
		t.Errorf("GET = %d %s, want 500", code, body)
	}
}

// TestAMappingThatCannotBeStoredIsARefusal, and says which half failed: a
// mapping that was accepted and not written is the worst of the three outcomes.
func TestAMappingThatCannotBeStoredIsARefusal(t *testing.T) {
	idp := newFakeIdP(t)
	ts, srv := oidcServer(t, idp)
	admin := adminClient(t, ts)
	body := `{"enabled":true,"claim":"groups","rules":[{"value":"x","roles":["operator"]}]}`

	// A directory where the record belongs fails the write for root too, which is
	// what a permission bit would not achieve here.
	if err := os.MkdirAll(srv.settings.oidcFile, 0o700); err != nil {
		t.Fatalf("mkdir over the mapping file: %v", err)
	}
	if code, out := mappingPut(t, admin, ts, body); code != http.StatusInternalServerError {
		t.Errorf("PUT over an unwritable record = %d %s, want 500", code, out)
	} else if !strings.Contains(out, "store oidc mapping") {
		t.Errorf("refusal does not say storing failed: %s", out)
	}
	if err := os.RemoveAll(srv.settings.oidcFile); err != nil {
		t.Fatalf("clean up: %v", err)
	}

	// The groups have to be readable to check a rule against them, so a broken
	// group store is a refusal rather than a mapping validated against nothing.
	p := filepath.Join(srv.dataDir, "groups")
	if err := os.RemoveAll(p); err != nil {
		t.Fatalf("remove %s: %v", p, err)
	}
	if err := os.WriteFile(p, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	if code, out := mappingPut(t, admin, ts, body); code != http.StatusInternalServerError {
		t.Errorf("PUT with an unreadable group store = %d %s, want 500", code, out)
	} else if !strings.Contains(out, "read groups") {
		t.Errorf("refusal does not say which half failed: %s", out)
	}
}

// withAdminSession attaches a session cookie for the bootstrap administrator, for
// the one case a real client cannot produce: a request body that fails to read.
func withAdminSession(t *testing.T, srv *Server, req *http.Request) *http.Request {
	t.Helper()
	var (
		all []User
		err error
	)
	srv.do(func() { all, err = srv.users.LoadAll() })
	if err != nil {
		t.Fatalf("load users: %v", err)
	}
	for _, u := range all {
		if !u.hasRole(RoleAdmin) {
			continue
		}
		tok, err := srv.sessions.create(u, nil)
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
		return req
	}
	t.Fatal("no administrator to sign in as")
	return req
}

// adminClient is a client holding a session for the bootstrap administrator.
func adminClient(t *testing.T, ts *httptest.Server) *http.Client {
	t.Helper()
	c := noRedirects(t)
	resp, err := c.Post(ts.URL+"/api/v1/auth/login", "application/json",
		strings.NewReader(`{"username":"root","password":"rootpassword12"}`))
	if err != nil {
		t.Fatalf("admin login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin login = %d, want 200", resp.StatusCode)
	}
	return c
}

func mappingGet(t *testing.T, c *http.Client, ts *httptest.Server) (int, string) {
	t.Helper()
	resp, err := c.Get(ts.URL + "/api/v1/settings/oidc-mapping")
	if err != nil {
		t.Fatalf("get mapping: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func mappingPut(t *testing.T, c *http.Client, ts *httptest.Server, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/settings/oidc-mapping", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("put mapping: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// TestAGroupStoreThatCannotBeReadFailsTheLogin, on both paths through it: the
// mapping reads the groups to decide membership, and the session snapshot reads
// them again to record it. A login that produced a session with the group half
// missing would hand somebody a browser that quietly cannot see their team's work.
func TestAGroupStoreThatCannotBeReadFailsTheLogin(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mapping oidcMapping
	}{
		{"deciding membership", oidcMapping{Enabled: true, Claim: "groups",
			Rules: []oidcMapRule{{Value: "atlas-betrieb", Roles: []string{RoleOperator}}}}},
		{"recording it in the session", oidcMapping{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			idp := newFakeIdP(t)
			ts, srv := oidcServer(t, idp)
			setMapping(t, srv, tc.mapping)
			mapClaims(idp, "atlas-betrieb")

			// A plain file where the directory belongs fails every read, for root too.
			p := filepath.Join(srv.dataDir, "groups")
			if err := os.RemoveAll(p); err != nil {
				t.Fatalf("remove %s: %v", p, err)
			}
			if err := os.WriteFile(p, []byte("not a directory"), 0o600); err != nil {
				t.Fatalf("write %s: %v", p, err)
			}

			resp := federate(t, ts, idp, noRedirects(t))
			defer resp.Body.Close()
			if got := resp.Header.Get("Location"); got != "/"+oidcFailedQuery {
				t.Errorf("Location = %q, want the login screen", got)
			}
			for _, ck := range resp.Cookies() {
				if ck.Name == sessionCookie && ck.Value != "" {
					t.Error("a login that could not read the groups produced a session")
				}
			}
		})
	}
}

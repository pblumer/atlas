package teamsphone

import (
	"context"
	"crypto/sha1"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// MockTenant is a Teams tenant in this process's memory: resource accounts, call
// queues, auto attendants and the associations between them, gone when the process
// stops. A worker in mock mode registers one per configured tenant name, so a model runs
// end to end — and its retries and its leaver path with it — before anybody has a tenant
// to point it at.
//
// It keeps the rules the shipped PowerShell keeps, because a mock that is more forgiving
// than the real thing proves nothing: an existing voice app is found before one is
// created, an existing call queue is brought to the lists and settings a model names
// but never has a list emptied, an account Entra holds but Teams does not show yet is adopted, an enabled
// user's address is refused as a resource account, and a remove of something already
// gone answers removed=false. Teams' synchronization lag is modelled too: an account the
// mock creates is invisible to Teams until the next operation that looks for it, which
// is what a retried "ensure" meets in a real tenant.
type MockTenant struct {
	mu sync.Mutex
	// users are the people resolve-users knows, by lower-case name; nil knows everyone.
	users      map[string]bool
	accounts   map[string]*mockAccount // by object id
	byUPN      map[string]string       // lower-case UPN -> object id
	apps       map[string]*mockApp     // voice apps by id
	associated map[string]string       // resource account object id -> voice app id
}

type mockAccount struct {
	objectID, upn, displayName, accountType string
	inTeams                                 bool
}

type mockApp struct {
	id, name, kind string
	// A call queue's lists and settings; an auto attendant has none.
	agents, authorized           []string
	routing                      string
	presence, conference, optOut bool
}

// NewMockTenant creates an empty tenant. users names the people resolve-users finds,
// by user principal name or id; with none, every name resolves, which is what a model
// tried out with real colleagues' addresses expects.
func NewMockTenant(users ...string) *MockTenant {
	m := &MockTenant{
		accounts:   map[string]*mockAccount{},
		byUPN:      map[string]string{},
		apps:       map[string]*mockApp{},
		associated: map[string]string{},
	}
	if len(users) > 0 {
		m.users = map[string]bool{}
		for _, u := range users {
			if u = strings.ToLower(strings.TrimSpace(u)); u != "" {
				m.users[u] = true
			}
		}
	}
	return m
}

// mockID derives a stable GUID-shaped id from a seed, so a mock run's ids are the same
// on every run and a test can name them.
func mockID(seed string) string {
	h := sha1.Sum([]byte(seed))
	return fmt.Sprintf("%x-%x-%x-%x-%x", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

// Do performs one prepared operation.
func (m *MockTenant) Do(_ context.Context, req Request) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := req.Args
	str := func(name string) string { s, _ := a[name].(string); return s }
	switch req.Operation {
	case "resolve-users":
		users, _ := a["users"].([]string)
		ids, notFound := []string{}, []string{}
		for _, u := range users {
			if m.users != nil && !m.users[strings.ToLower(u)] {
				notFound = append(notFound, u)
				continue
			}
			ids = append(ids, mockID("user:"+strings.ToLower(u)))
		}
		return map[string]any{"ids": ids, "notFound": notFound}, nil
	case "ensure-resource-account":
		return m.ensureAccount(str("userPrincipalName"), str("displayName"), str("accountType"))
	case "ensure-call-queue":
		return m.ensureApp(req.Operation, "call queue", AccountCallQueue, str("name"), str("resourceAccountId"), "", &req)
	case "ensure-auto-attendant":
		return m.ensureApp(req.Operation, "auto attendant", AccountAutoAttendant, str("name"), str("resourceAccountId"), str("transferTarget"), nil)
	case "remove-call-queue":
		return m.removeApp(req.Operation, AccountCallQueue, str("resourceAccountId"), str("name"))
	case "remove-auto-attendant":
		return m.removeApp(req.Operation, AccountAutoAttendant, str("resourceAccountId"), str("name"))
	case "remove-resource-account":
		id := str("resourceAccountId")
		acc, ok := m.accounts[id]
		if !ok {
			return map[string]any{"removed": false, "objectId": id}, nil
		}
		delete(m.associated, id)
		delete(m.byUPN, strings.ToLower(acc.upn))
		delete(m.accounts, id)
		return map[string]any{"removed": true, "objectId": id}, nil
	}
	return nil, fmt.Errorf("teamsphone: unknown operation %q (want %s)", req.Operation, strings.Join(OpNames(), ", "))
}

func (m *MockTenant) ensureAccount(upn, displayName, accountType string) (map[string]any, error) {
	const op = "ensure-resource-account"
	result := func(acc *mockAccount, created bool, source string) map[string]any {
		return map[string]any{"objectId": acc.objectID, "userPrincipalName": acc.upn, "created": created, "source": source}
	}
	if id, ok := m.byUPN[strings.ToLower(upn)]; ok {
		acc := m.accounts[id]
		if !acc.inTeams {
			// The synchronization has happened by the time anybody asks again.
			acc.inTeams = true
			return result(acc, false, "entra"), nil
		}
		if acc.accountType != accountType {
			return nil, refused(CodeAccountTypeMismatch, op, upn+" is a resource account for another kind of voice app")
		}
		return result(acc, false, "teams"), nil
	}
	if m.users != nil && m.users[strings.ToLower(upn)] {
		return nil, refused(CodeUPNTaken, op, upn+" belongs to an enabled user account, not to a resource account; choose another user principal name")
	}
	acc := &mockAccount{objectID: mockID("account:" + strings.ToLower(upn)), upn: upn, displayName: displayName, accountType: accountType}
	m.accounts[acc.objectID] = acc
	m.byUPN[strings.ToLower(upn)] = acc.objectID
	return result(acc, true, "created"), nil
}

// ensureApp finds or creates a voice app and associates it with its account. queue is a
// call queue's request — the lists and settings it names, with defaults for the rest —
// and nil for an auto attendant; a queue that already exists is brought to what the
// request names before anything else changes, as the shipped PowerShell does.
func (m *MockTenant) ensureApp(op, label, kind, name, accountID, transferTarget string, queue *Request) (map[string]any, error) {
	acc, ok := m.accounts[accountID]
	if !ok {
		return nil, refused(CodeAccountNotVisible, op, "the resource account "+accountID+" is not visible to Teams; Teams synchronizes new accounts after a delay, so retry the task later")
	}
	if acc.accountType != kind {
		return nil, refused(CodeAccountTypeMismatch, op, "the resource account "+acc.upn+" was created for another kind of voice app and cannot carry a "+label)
	}
	if transferTarget != "" {
		if _, ok := m.accounts[transferTarget]; !ok {
			return nil, fmt.Errorf("teamsphone: the transfer target %s is no resource account in this tenant", transferTarget)
		}
	}
	result := func(app *mockApp, created bool, changed []string) map[string]any {
		out := map[string]any{"id": app.id, "name": app.name, "created": created}
		if queue != nil {
			// agentsChanged is what 0.16.0 answered; models written against it keep it.
			out["changed"], out["agentsChanged"] = changed, contains(changed, "agents")
		}
		return out
	}
	sync := func(app *mockApp) ([]string, error) {
		if queue == nil {
			return nil, nil
		}
		return syncQueue(app, *queue)
	}
	if id, ok := m.associated[accountID]; ok {
		app := m.apps[id]
		if app.kind != kind {
			return nil, refused(CodeAccountInUse, op, "the resource account "+accountID+" is already associated with "+article(app.kind))
		}
		changed, err := sync(app)
		if err != nil {
			return nil, err
		}
		return result(app, false, changed), nil
	}
	for _, app := range m.apps {
		if app.kind == kind && app.name == name {
			// Before the association: a refused request leaves the queue as unattached
			// as it was.
			changed, err := sync(app)
			if err != nil {
				return nil, err
			}
			m.associated[accountID] = app.id
			return result(app, false, changed), nil
		}
	}
	app := &mockApp{id: mockID(kind + ":" + name), name: name, kind: kind}
	if queue != nil {
		a := queue.Args
		agents, _ := a["agents"].([]string)
		authorized, _ := a["authorizedUsers"].([]string)
		app.agents, app.authorized = append([]string(nil), agents...), append([]string(nil), authorized...)
		app.routing, _ = a["routingMethod"].(string)
		app.presence, _ = a["presenceBasedRouting"].(bool)
		app.conference, _ = a["conferenceMode"].(bool)
		app.optOut, _ = a["allowOptOut"].(bool)
	}
	m.apps[app.id] = app
	m.associated[accountID] = app.id
	return result(app, true, []string{}), nil
}

// article names a kind of voice app the way a refusal does.
func article(kind string) string {
	if kind == AccountCallQueue {
		return "a call queue"
	}
	return "an auto attendant"
}

// syncQueue brings an existing call queue to what a request names, by the rules the
// shipped PowerShell keeps (Sync-CallQueue in operations.ps1): what the request does not
// name stays as it is, ids compare without regard to case, the agents' order counts only
// where the queue rings serially once this run is done, and a list that would be emptied
// is refused before anything changes. It answers the names it changed, sorted.
func syncQueue(app *mockApp, req Request) ([]string, error) {
	a := req.Args
	named := func(name string) bool { return contains(req.Reconcile, name) }
	routing := app.routing
	if named("routingMethod") {
		routing, _ = a["routingMethod"].(string)
	}
	lists := []struct {
		name, label string
		have        *[]string
		ordered     bool
	}{
		{"agents", "agents", &app.agents, routing == "serial"},
		{"authorizedUsers", "authorized users", &app.authorized, false},
	}
	want := map[string][]string{}
	for _, l := range lists {
		w, _ := a[l.name].([]string)
		if !named(l.name) || listKey(w, l.ordered) == listKey(*l.have, l.ordered) {
			continue
		}
		if len(w) == 0 {
			return nil, refused(CodeWouldEmptyList, req.Operation, emptyListRefusal(l.name, l.label, app.name, len(*l.have)))
		}
		want[l.name] = w
	}
	changed := []string{}
	for _, l := range lists {
		if w, ok := want[l.name]; ok {
			*l.have = append([]string(nil), w...)
			changed = append(changed, l.name)
		}
	}
	if named("routingMethod") && routing != app.routing {
		app.routing = routing
		changed = append(changed, "routingMethod")
	}
	for _, s := range []struct {
		name string
		have *bool
	}{{"presenceBasedRouting", &app.presence}, {"conferenceMode", &app.conference}, {"allowOptOut", &app.optOut}} {
		if v, ok := a[s.name].(bool); ok && named(s.name) && v != *s.have {
			*s.have = v
			changed = append(changed, s.name)
		}
	}
	sort.Strings(changed)
	return changed, nil
}

// emptyListRefusal is the words both the mock and the shipped PowerShell refuse an
// emptied list with.
func emptyListRefusal(name, label, queue string, have int) string {
	return fmt.Sprintf("the empty list of %s would remove every one of the %d %s of the call queue %s; an ensure never empties a list, because a Resolve users that found nobody reads the same: name at least one, or leave %s out to keep the ones it has", name, have, label, queue, name)
}

// listKey is what two lists of ids are compared by: the ids in lower case, sorted unless
// the order is the routing.
func listKey(ids []string, ordered bool) string {
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = strings.ToLower(id)
	}
	if !ordered {
		sort.Strings(keys)
	}
	return strings.Join(keys, ",")
}

func (m *MockTenant) removeApp(op, kind, accountID, name string) (map[string]any, error) {
	if id, ok := m.associated[accountID]; ok {
		if m.apps[id].kind != kind {
			return nil, refused(CodeAccountInUse, op, "the resource account "+accountID+" is associated with another kind of voice app than "+article(kind))
		}
		delete(m.associated, accountID)
		delete(m.apps, id)
		return map[string]any{"removed": true, "id": id}, nil
	}
	if name != "" {
		for id, app := range m.apps {
			if app.kind == kind && app.name == name && !m.isAssociated(id) {
				delete(m.apps, id)
				return map[string]any{"removed": true, "id": id}, nil
			}
		}
	}
	return map[string]any{"removed": false, "id": ""}, nil
}

func (m *MockTenant) isAssociated(appID string) bool {
	for _, id := range m.associated {
		if id == appID {
			return true
		}
	}
	return false
}

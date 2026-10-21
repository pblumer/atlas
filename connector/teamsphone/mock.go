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
	// numbers are the phone numbers held, by number, naming their holder's key (see
	// target). inventory are the Calling Plan and Operator Connect numbers the tenant
	// has; nil has every one.
	numbers   map[string]mockNumber
	inventory map[string]bool
}

type mockNumber struct {
	holder, kind string
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
		numbers:    map[string]mockNumber{},
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

// WithNumbers names the Calling Plan and Operator Connect numbers the tenant has; with
// none named, it has every number, which is what a model tried out with real numbers
// expects. A Direct Routing number needs no inventory to be assigned. The numbers named
// are also the inventory list-free-phone-numbers offers from, as if Direct Routing
// numbers had been uploaded to it.
func (m *MockTenant) WithNumbers(numbers ...string) *MockTenant {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, n := range numbers {
		if p, err := phoneNumber("number", n); err == nil && p != "" {
			if m.inventory == nil {
				m.inventory = map[string]bool{}
			}
			m.inventory[numberKey(p)] = true
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
	case "remove-hotline":
		ids, _ := a["resourceAccountIds"].([]string)
		names, _ := a["names"].([]string)
		return m.removeHotline(ids, names)
	case "ensure-phone-number":
		return m.ensureNumber(str("identity"), str("phoneNumber"), str("numberType"))
	case "remove-phone-number":
		return m.removeNumbers(str("identity"), str("phoneNumber"))
	case "list-free-phone-numbers":
		most, _ := a["max"].(int)
		return m.freeNumbers(str("numberPrefix"), str("numberType"), most), nil
	case "remove-resource-account":
		// The leaver path in one task, in the order the shipped PowerShell keeps: the
		// account's numbers are released, the association removed, the account deleted.
		id := str("resourceAccountId")
		acc, ok := m.accounts[id]
		if !ok {
			return map[string]any{"removed": false, "objectId": id, "phoneNumbers": []string{}}, nil
		}
		released, err := m.removeNumbers(id, "")
		if err != nil {
			return nil, err
		}
		delete(m.associated, id)
		delete(m.byUPN, strings.ToLower(acc.upn))
		delete(m.accounts, id)
		return map[string]any{"removed": true, "objectId": id, "phoneNumbers": released["phoneNumbers"]}, nil
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

// removeHotline takes a hotline away by the rules the shipped PowerShell keeps
// (Remove-Hotline in operations.ps1): nothing changes when a voice app also answers for
// an account the hotline does not name; otherwise the auto attendants go first, then the
// queues — each found through its account, or by name when an earlier run left it without
// one — then each account's numbers and the account.
func (m *MockTenant) removeHotline(ids, names []string) (map[string]any, error) {
	const op = "remove-hotline"
	named := map[string]bool{}
	for _, id := range ids {
		named[id] = true
	}
	for _, id := range ids {
		appID, ok := m.associated[id]
		if !ok {
			continue
		}
		for acc, other := range m.associated {
			if other == appID && !named[acc] {
				return nil, refused(CodeAccountInUse, op, fmt.Sprintf("the voice app %s also answers for the resource account %s, which this hotline does not name; nothing was removed", m.apps[appID].name, acc))
			}
		}
	}
	removedApps := map[string][]string{AccountAutoAttendant: {}, AccountCallQueue: {}}
	for _, kind := range []string{AccountAutoAttendant, AccountCallQueue} {
		for _, id := range ids {
			if appID, ok := m.associated[id]; ok && m.apps[appID].kind == kind {
				out, err := m.removeApp(op, kind, id, "")
				if err != nil {
					return nil, err
				}
				removedApps[kind] = append(removedApps[kind], out["id"].(string))
			}
		}
		for _, name := range names {
			if out, err := m.removeApp(op, kind, "", name); err == nil && out["removed"] == true {
				removedApps[kind] = append(removedApps[kind], out["id"].(string))
			}
		}
	}
	accounts, numbers := []string{}, []string{}
	for _, id := range ids {
		acc, ok := m.accounts[id]
		if !ok {
			continue
		}
		released, err := m.removeNumbers(id, "")
		if err != nil {
			return nil, err
		}
		numbers = append(numbers, released["phoneNumbers"].([]string)...)
		delete(m.associated, id)
		delete(m.byUPN, strings.ToLower(acc.upn))
		delete(m.accounts, id)
		accounts = append(accounts, id)
	}
	for _, l := range [][]string{removedApps[AccountAutoAttendant], removedApps[AccountCallQueue], accounts, numbers} {
		sort.Strings(l)
	}
	removed := len(accounts)+len(numbers)+len(removedApps[AccountAutoAttendant])+len(removedApps[AccountCallQueue]) > 0
	return map[string]any{"removed": removed, "attendants": removedApps[AccountAutoAttendant], "queues": removedApps[AccountCallQueue],
		"accounts": accounts, "phoneNumbers": numbers}, nil
}

// target is the key a phone number's holder is known by: a resource account's object id,
// whether it is named by id or by address, and a person's lower-case name. Somebody the
// tenant does not know cannot hold a number.
func (m *MockTenant) target(identity string) (string, error) {
	if _, ok := m.accounts[identity]; ok {
		return identity, nil
	}
	if id, ok := m.byUPN[strings.ToLower(identity)]; ok {
		return id, nil
	}
	if m.users == nil || m.users[strings.ToLower(identity)] {
		return "user:" + strings.ToLower(identity), nil
	}
	return "", fmt.Errorf("teamsphone: no user or resource account %s in this tenant", identity)
}

// numberKey is what two spellings of one phone number are compared by: without the
// leading + and in lower case, as Teams answers with one whichever was written.
func numberKey(n string) string { return strings.ToLower(strings.TrimPrefix(n, "+")) }

// ensureNumber gives a user or resource account the number named, by the rules the
// shipped PowerShell keeps (Set-PhoneNumber in operations.ps1): nothing to do when the
// holder has it already; refused when somebody else holds it, or when the tenant lacks a
// Calling Plan or Operator Connect number; otherwise the holder's number, if any, is
// replaced and answered as previousNumber.
func (m *MockTenant) ensureNumber(identity, number, kind string) (map[string]any, error) {
	const op = "ensure-phone-number"
	holder, err := m.target(identity)
	if err != nil {
		return nil, err
	}
	result := func(assigned bool, previous string) map[string]any {
		return map[string]any{"phoneNumber": number, "identity": identity, "assigned": assigned, "previousNumber": previous}
	}
	key := numberKey(number)
	if held, ok := m.numbers[key]; ok {
		if held.holder == holder {
			return result(false, ""), nil
		}
		return nil, refused(CodeNumberInUse, op, "the phone number "+number+" is assigned to another user or resource account; remove it there first")
	}
	if kind != NumberDirectRouting && m.inventory != nil && !m.inventory[key] {
		return nil, refused(CodeNumberNotFound, op, "the phone number "+number+" is not one of the tenant's "+kind+" numbers; acquire or port it first")
	}
	previous := ""
	for n, held := range m.numbers {
		if held.holder == holder {
			delete(m.numbers, n)
			previous = "+" + n
		}
	}
	m.numbers[key] = mockNumber{holder: holder, kind: kind}
	return result(true, previous), nil
}

// removeNumbers takes the number named — or, with none named, every number — away from
// its holder, and answers which it removed.
func (m *MockTenant) removeNumbers(identity, number string) (map[string]any, error) {
	holder, err := m.target(identity)
	if err != nil {
		return nil, err
	}
	removed := []string{}
	for n, held := range m.numbers {
		if held.holder == holder && (number == "" || n == numberKey(number)) {
			delete(m.numbers, n)
			removed = append(removed, "+"+n)
		}
	}
	sort.Strings(removed)
	return map[string]any{"removed": len(removed) > 0, "phoneNumbers": removed}, nil
}

// freeNumbers lists the inventory's numbers nobody holds, sorted, no more than most. The
// mock's inventory knows no type and no capability, so each of its numbers is offered for
// every type and every kind of holder; a tenant that has every number lists none, since
// "every number" has no page to show.
func (m *MockTenant) freeNumbers(prefix, kind string, most int) map[string]any {
	keys := []string{}
	for key := range m.inventory {
		if _, held := m.numbers[key]; !held && strings.HasPrefix(key, numberKey(prefix)) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	truncated := len(keys) > most
	if truncated {
		keys = keys[:most]
	}
	numbers, entries := []string{}, []map[string]any{}
	for _, key := range keys {
		numbers = append(numbers, "+"+key)
		entries = append(entries, map[string]any{"phoneNumber": "+" + key, "numberType": kind, "city": "", "isoCountryCode": ""})
	}
	return map[string]any{"numbers": numbers, "entries": entries, "count": len(numbers), "truncated": truncated}
}

func (m *MockTenant) isAssociated(appID string) bool {
	for _, id := range m.associated {
		if id == appID {
			return true
		}
	}
	return false
}

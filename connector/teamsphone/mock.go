package teamsphone

import (
	"context"
	"crypto/sha1"
	"fmt"
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
// created, an account Entra holds but Teams does not show yet is adopted, an enabled
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
		return m.ensureApp("call queue", AccountCallQueue, str("name"), str("resourceAccountId"), "")
	case "ensure-auto-attendant":
		return m.ensureApp("auto attendant", AccountAutoAttendant, str("name"), str("resourceAccountId"), str("transferTarget"))
	case "remove-call-queue":
		return m.removeApp(AccountCallQueue, str("resourceAccountId"), str("name")), nil
	case "remove-auto-attendant":
		return m.removeApp(AccountAutoAttendant, str("resourceAccountId"), str("name")), nil
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
			return nil, fmt.Errorf("teamsphone: %s is a resource account for a %s, not for a %s", upn, acc.accountType, accountType)
		}
		return result(acc, false, "teams"), nil
	}
	if m.users != nil && m.users[strings.ToLower(upn)] {
		return nil, fmt.Errorf("teamsphone: %s belongs to an enabled user account, not to a resource account; choose another user principal name", upn)
	}
	acc := &mockAccount{objectID: mockID("account:" + strings.ToLower(upn)), upn: upn, displayName: displayName, accountType: accountType}
	m.accounts[acc.objectID] = acc
	m.byUPN[strings.ToLower(upn)] = acc.objectID
	return result(acc, true, "created"), nil
}

func (m *MockTenant) ensureApp(label, kind, name, accountID, transferTarget string) (map[string]any, error) {
	acc, ok := m.accounts[accountID]
	if !ok {
		return nil, fmt.Errorf("teamsphone: no resource account has the object id %s", accountID)
	}
	if acc.accountType != kind {
		return nil, fmt.Errorf("teamsphone: resource account %s is for a %s and cannot carry a %s", acc.upn, acc.accountType, label)
	}
	if transferTarget != "" {
		if _, ok := m.accounts[transferTarget]; !ok {
			return nil, fmt.Errorf("teamsphone: the transfer target %s is no resource account in this tenant", transferTarget)
		}
	}
	result := func(app *mockApp, created bool) map[string]any {
		return map[string]any{"id": app.id, "name": app.name, "created": created}
	}
	if id, ok := m.associated[accountID]; ok {
		app := m.apps[id]
		if app.kind != kind {
			return nil, fmt.Errorf("teamsphone: resource account %s is already associated with %q, which is no %s", acc.upn, app.name, label)
		}
		return result(app, false), nil
	}
	for _, app := range m.apps {
		if app.kind == kind && app.name == name {
			m.associated[accountID] = app.id
			return result(app, false), nil
		}
	}
	app := &mockApp{id: mockID(kind + ":" + name), name: name, kind: kind}
	m.apps[app.id] = app
	m.associated[accountID] = app.id
	return result(app, true), nil
}

func (m *MockTenant) removeApp(kind, accountID, name string) map[string]any {
	if id, ok := m.associated[accountID]; ok && m.apps[id].kind == kind {
		delete(m.associated, accountID)
		delete(m.apps, id)
		return map[string]any{"removed": true, "id": id}
	}
	if name != "" {
		for id, app := range m.apps {
			if app.kind == kind && app.name == name && !m.isAssociated(id) {
				delete(m.apps, id)
				return map[string]any{"removed": true, "id": id}
			}
		}
	}
	return map[string]any{"removed": false, "id": ""}
}

func (m *MockTenant) isAssociated(appID string) bool {
	for _, id := range m.associated {
		if id == appID {
			return true
		}
	}
	return false
}

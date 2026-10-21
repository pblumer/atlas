package teamsphone

// WithEndpoints points a Pwsh client at local stand-ins for Microsoft's token and
// Graph endpoints, so the shipped script runs end to end in a test.
func (p *Pwsh) WithEndpoints(loginRoot, graphRoot string) *Pwsh {
	p.loginRoot, p.graphRoot = loginRoot, graphRoot
	return p
}

// MockQueue is what the mock tenant holds for a call queue, so a test sees what a
// reconciliation left behind and not only what it reported.
type MockQueue struct {
	Agents, AuthorizedUsers                           []string
	RoutingMethod                                     string
	PresenceBasedRouting, ConferenceMode, AllowOptOut bool
}

// QueueOf answers what the mock tenant holds for the call queue with the given id.
func (m *MockTenant) QueueOf(queueID string) MockQueue {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[queueID]
	if !ok {
		return MockQueue{}
	}
	return MockQueue{
		Agents: append([]string(nil), app.agents...), AuthorizedUsers: append([]string(nil), app.authorized...),
		RoutingMethod: app.routing, PresenceBasedRouting: app.presence, ConferenceMode: app.conference, AllowOptOut: app.optOut,
	}
}

var (
	LastEnvelope = lastEnvelope
	ASCIIJSON    = asciiJSON
)

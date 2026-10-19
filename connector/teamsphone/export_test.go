package teamsphone

// WithEndpoints points a Pwsh client at local stand-ins for Microsoft's token and
// Graph endpoints, so the shipped script runs end to end in a test.
func (p *Pwsh) WithEndpoints(loginRoot, graphRoot string) *Pwsh {
	p.loginRoot, p.graphRoot = loginRoot, graphRoot
	return p
}

// AgentsOf answers the agents the mock tenant holds for a call queue, so a test sees
// what a reconciliation left behind and not only what it reported.
func (m *MockTenant) AgentsOf(queueID string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if app, ok := m.apps[queueID]; ok {
		return append([]string(nil), app.agents...)
	}
	return nil
}

var (
	LastEnvelope = lastEnvelope
	ASCIIJSON    = asciiJSON
)

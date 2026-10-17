package teamsphone

// WithEndpoints points a Pwsh client at local stand-ins for Microsoft's token and
// Graph endpoints, so the shipped script runs end to end in a test.
func (p *Pwsh) WithEndpoints(loginRoot, graphRoot string) *Pwsh {
	p.loginRoot, p.graphRoot = loginRoot, graphRoot
	return p
}

var (
	LastEnvelope = lastEnvelope
	ASCIIJSON    = asciiJSON
)

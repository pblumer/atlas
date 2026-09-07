package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/formgen"
	"github.com/pblumer/atlas/connector/agent"
)

// What the form generator is allowed to reach (ADR-draft-ai-form-generation).
//
// The generation service itself holds no state and no store — see its package comment.
// These three closures are the whole of its access to this server, and each of them is
// written here rather than there so that the single-writer discipline (I3) and the
// artifact scopes stay in the package that owns them.
//
// Nothing here is on the job path. A generation is an authoring request: no instance, no
// token, no job, no event. What it borrows from the runtime is the *configuration* an
// operator already made for the runtime — an agent Worker's endpoint, wire format, model
// and vault-held key (ADR-0255) — which is the point: there is no second place to
// configure a model, and no second credential to rotate.

// generationMaxTokens bounds one generated form. The Messages adapter's own default is
// 4096, which a long form with grouped fields and prose can reach; a form that needs
// twice this is not a form anybody fills in.
const generationMaxTokens = 8192

// agentWorkersForGeneration lists the enabled agent Workers a request may name.
//
// It lists them for everyone who may author, without a scope check, and that is
// deliberate: it is the *catalog entry* rule (ADR-0205) — existence is not
// configuration. A modeller who cannot see that an AI Worker exists cannot author
// against it, exactly as with the worker picker on a service task. What travels is a
// name, a wire format and a model id: no endpoint, no credential reference, and ADR-0255
// argues the model id is the one part of an agent Worker's configuration that must stay
// readable, because it is what an author is choosing when they choose what a generation
// costs.
func (s *Server) agentWorkersForGeneration(*http.Request) ([]formgen.Worker, error) {
	var (
		recs    []connector
		loadErr error
	)
	s.do(func() { recs, loadErr = s.connectors.LoadAll() })
	if loadErr != nil {
		return nil, loadErr
	}
	out := make([]formgen.Worker, 0, len(recs))
	for _, c := range recs {
		if c.Kind != connectorKindAgent || !c.Enabled {
			continue
		}
		out = append(out, formgen.Worker{
			Name: c.Name, Model: strings.TrimSpace(c.Model), Provider: agentProtocolOf(c),
		})
	}
	return out, nil
}

// dialAgentWorker turns a named agent Worker into something that can be asked: the
// adapter for its wire format, its endpoint, and its API key read from the vault.
//
// The credential is resolved here and goes no further than the adapter — the service
// that calls this receives an [agent.Model] and never a configuration, which is the same
// guarantee a resolved connector Job makes (ADR-0041/0069).
//
// Only Console records are dialled. A model configured purely in a worker's environment
// (ATLAS_AGENT_*) is that worker's own and is not visible here; ADR-0255 made the Console
// record the way to configure an agent, and this is the first thing that depends on it.
func (s *Server) dialAgentWorker(_ *http.Request, name string) (agent.Model, error) {
	var (
		found   connector
		apiKey  string
		ok      bool
		loadErr error
	)
	// The record and its credential are read in one turn of the loop, because the vault
	// store is owned by that goroutine exactly as the worker store is
	// (resolveConnectorSecret, ADR-0069). Nothing else happens in here: the call itself
	// is minutes long and belongs nowhere near the single writer.
	s.do(func() {
		var recs []connector
		if recs, loadErr = s.connectors.LoadAll(); loadErr != nil {
			return
		}
		for _, c := range recs {
			if c.Kind == connectorKindAgent && c.Enabled && c.Name == name {
				found, ok = c, true
				apiKey = strings.TrimSpace(s.resolveConnectorSecret(c.CredentialsRef))
				return
			}
		}
	})
	if loadErr != nil {
		return nil, loadErr
	}
	if !ok {
		return nil, fmt.Errorf("no enabled AI Worker named %q", name)
	}
	endpoint := strings.TrimSpace(found.Endpoint)
	// The worker's own rule (ADR-0254/0255), applied where the author is: pointing at a
	// provider's public endpoint with no credential is certainly a misconfiguration,
	// while a self-hosted endpoint may legitimately need none.
	if apiKey == "" && endpoint == "" {
		return nil, fmt.Errorf("the AI Worker %q has neither a credential nor an endpoint", name)
	}
	model := strings.TrimSpace(found.Model)
	if agentProtocolOf(found) == agentProtocolChatCompletions {
		if model == "" {
			// The adapter has no default and says so at startup; saying it here
			// spares an author a 400 from somebody else's API.
			return nil, fmt.Errorf("the AI Worker %q names no model, and its protocol has no default", name)
		}
		return &agent.ChatCompletionsModel{
			Endpoint: endpoint, APIKey: apiKey, Model: model, MaxTokens: generationMaxTokens,
		}, nil
	}
	return &agent.HTTPModel{
		Endpoint: endpoint, APIKey: apiKey, Model: model, MaxTokens: generationMaxTokens,
	}, nil
}

// agentProtocolOf reads a record's wire format, defaulting as the worker does.
func agentProtocolOf(c connector) string {
	if p := strings.TrimSpace(c.Provider); p != "" {
		return p
	}
	return agentProtocolMessages
}

// processSourceForGeneration reads the BPMN of the process a form belongs to: the draft
// under the author's hands if there is one, otherwise the version currently deployed.
//
// The draft comes first because it is what the author is looking at — generating a form
// for a step they added five minutes ago has to see that step. A draft they may not view
// reads as absent and the deployed version answers instead, which is the same thing the
// Modeler shows them.
func (s *Server) processSourceForGeneration(r *http.Request, processID string) (formgen.Source, bool, error) {
	var (
		rec     draft
		ok      bool
		readErr error
	)
	s.do(func() { rec, ok, readErr = s.drafts.Get(processID) })
	if readErr != nil {
		return formgen.Source{}, false, readErr
	}
	if ok {
		if code, _ := s.authorizeArtifact(r, rec.ProjectID, rec.OwnerID, ScopeRoleViewer); code == 0 {
			return formgen.Source{
				ProcessID: rec.ProcessID, Name: rec.Name, XML: rec.XML, Origin: "draft",
			}, true, nil
		}
	}
	var (
		xml      []byte
		name     string
		deployed bool
	)
	s.do(func() {
		if d := s.latestDeploymentByProcessID(processID); d != nil {
			xml, name, deployed = d.xml, d.Name, true
		}
	})
	if !deployed {
		return formgen.Source{}, false, nil
	}
	return formgen.Source{
		ProcessID: processID, Name: name, XML: string(xml), Origin: "deployment",
	}, true, nil
}

package api

import (
	"net/http"

	"github.com/pblumer/atlas/api/feelgen"
)

// What the FEEL assistant is allowed to reach (ADR-draft-feel-assistant).
//
// It reaches less than form generation does, and the same of it: the agent Workers an
// operator configured, and one of them dialled with its vault-held key. There is no
// process source — an expression is written from the conversation and the author's own
// test variables, not from a model read off the server — so of the three closures form
// generation holds, the assistant needs two, and both are form generation's own. One
// configuration, one credential and one place to change the model (ADR-0255).

// agentWorkersForFeel lists the AI Workers the assistant may ask: exactly the ones form
// generation lists, in the assistant's own type.
func (s *Server) agentWorkersForFeel(r *http.Request) ([]feelgen.Worker, error) {
	listed, err := s.agentWorkersForGeneration(r)
	if err != nil {
		return nil, err
	}
	out := make([]feelgen.Worker, 0, len(listed))
	for _, w := range listed {
		out = append(out, feelgen.Worker{Name: w.Name, Model: w.Model, Provider: w.Provider})
	}
	return out, nil
}

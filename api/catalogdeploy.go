package api

import (
	"bytes"
	"errors"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/compiler"
)

// What a catalogue document's processes ask of the engine
// (ADR-draft-catalogue-export).
//
// A catalogue binds a product to a process by id, and the process lives here, in
// the deployment registry. Exporting a catalogue with its processes reads the model
// the newest version was deployed from; importing one deploys them through
// deployBody, the same door POST /api/v1/deployments is, as the person importing.
// Both live here so the catalogue package stays free of the engine, as it does for
// the fulfilment report (processlookup.go).

// ProcessXML answers the model the newest deployed version of a process id was
// deployed from — the bytes as they arrived, not a re-laid-out copy, so importing an
// export on the server it came from finds the model unchanged and deploys nothing.
func (l processLookup) ProcessXML(processID string) ([]byte, bool) {
	var raw []byte
	l.s.do(func() {
		if d := l.s.latestDeploymentOf(strings.TrimSpace(processID)); d != nil {
			raw = d.xml
		}
	})
	return raw, raw != nil
}

// catalogDeployer deploys a catalogue document's processes.
type catalogDeployer struct{ s *Server }

// CheckProcesses refuses a document whose importer may not deploy, and every model
// that does not compile or does not deploy the process it is filed under. It
// deploys nothing, so a document refused for any reason leaves the registry as it
// was.
//
// The role is asked here because the route's own is productmanager: importing a
// catalogue is a product manager's, deploying a process a modeler's, and a document
// carrying both needs both.
func (d catalogDeployer) CheckProcesses(r *http.Request, procs []catalog.Process) []catalog.DocumentProblem {
	var out []catalog.DocumentProblem
	if d.s.authEnabled && !mayReach(httpapi.PrincipalFrom(r.Context()), RoleModeler) {
		for _, p := range procs {
			out = append(out, catalog.DocumentProblem{Subject: "process:" + p.ID, Problem: catalog.ProblemMayNotDeploy})
		}
		return out
	}
	for _, p := range procs {
		// Keys are the deploy's to assign; a dry compile takes any base.
		deployables, err := compiler.ParseAll(0, 1, strings.NewReader(p.XML))
		if err != nil {
			out = append(out, catalog.DocumentProblem{Subject: "process:" + p.ID, Problem: "does not compile: " + err.Error()})
			continue
		}
		named := false
		for _, dp := range deployables {
			named = named || dp.Process.ProcessId() == p.ID
		}
		if !named {
			out = append(out, catalog.DocumentProblem{Subject: "process:" + p.ID,
				Problem: "the model deploys no process " + p.ID + ", so the products bound to it would still find none"})
		}
	}
	return out
}

// DeployProcesses deploys each process whose newest deployed version is not
// already the document's model, in the document's order. A refusal stops the rest,
// and the processes deployed before it stay deployed, which the import's answer
// says — a deploy is not a write a catalogue import can take back.
func (d catalogDeployer) DeployProcesses(r *http.Request, procs []catalog.Process) (deployed, unchanged []string, problems []catalog.DocumentProblem, err error) {
	look := processLookup{s: d.s}
	for _, p := range procs {
		model := []byte(p.XML)
		if cur, ok := look.ProcessXML(p.ID); ok && bytes.Equal(cur, model) {
			unchanged = append(unchanged, p.ID)
			continue
		}
		if _, refused := d.s.deployBody(r, model); refused != nil {
			if refused.status >= http.StatusInternalServerError {
				return deployed, unchanged, nil, errors.New(refused.reason)
			}
			return deployed, unchanged, []catalog.DocumentProblem{{Subject: "process:" + p.ID, Problem: refused.reason}}, nil
		}
		deployed = append(deployed, p.ID)
	}
	return deployed, unchanged, nil, nil
}

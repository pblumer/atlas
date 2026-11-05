package api

import (
	"encoding/xml"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/logging"
)

// A system process is atlas's own (ADR-0122): the binary deploys it into the
// protected system project, and only a deployment there may act with the rights the
// platform reserves for it — provisioning accounts, minting password links, speaking
// under an atlas.* event name, being started when atlas starts "the" registration or
// fulfilment process by its id.
//
// The process id alone does not say so. Any model may name its process
// proc_passwort_link, and a deployment's version counts per process id across every
// project, so a copy deployed elsewhere became the newest version and was started
// in the system process's place, with its rights. Two rules close that:
//
//   - a process id of a system process is deployed into the system project or not at
//     all (reservedProcessRefusal, and deployModel for every path into it);
//   - where the rights are checked or "the" system process is looked up, the
//     deployment must be in the system project (isSystemDeployment,
//     latestDeploymentOf), so a copy deployed before this rule existed is ordinary
//     tenant content: it runs, but as nobody special, and it can be deleted.

// isSystemDeployment reports whether d is one of atlas's own system processes: a
// system process id, deployed into the protected system project. Runs on the loop.
func (s *Server) isSystemDeployment(d *deployment) bool {
	return d != nil && d.ProjectID == systemProjectID && s.systemPIDs[d.ProcessID]
}

// isSystemDefKey is isSystemDeployment for a definition key. Runs on the loop.
func (s *Server) isSystemDefKey(key uint64) bool {
	return s.isSystemDeployment(s.deployments[key])
}

// reservedProcessRefusal names the first process in body whose id belongs to a system
// process, when body is to be deployed anywhere but the system project, and "" when
// nothing in it is reserved. Only the bootstrap deploys into the system project; the
// project's own guard keeps everybody else out of it. A body that does not parse is
// left to the compiler, which refuses it with a better message.
//
// systemPIDs is written once, while the server is built, and only read afterwards, so
// this may run off the loop.
func (s *Server) reservedProcessRefusal(body []byte, projectID string) string {
	if projectID == systemProjectID || len(s.systemPIDs) == 0 {
		return ""
	}
	var d struct {
		Processes []struct {
			ID string `xml:"id,attr"`
		} `xml:"process"`
	}
	if err := xml.Unmarshal(body, &d); err != nil {
		return ""
	}
	for _, p := range d.Processes {
		if id := strings.TrimSpace(p.ID); s.systemPIDs[id] {
			return reservedProcessMessage(id)
		}
	}
	return ""
}

// reservedProcessMessage is the refusal of a model that reuses the id of a system
// process.
func reservedProcessMessage(id string) string {
	return fmt.Sprintf("process id %q belongs to an atlas system process, which only atlas deploys, into the system project; give the process another id (ADR-0122)", id)
}

// setNoFraming keeps a page out of every frame: the pages a link opens set a password
// or answer a task, and another site that framed them could dress them up or lay its
// own controls over them. frame-ancestors for browsers that read it, X-Frame-Options
// for the ones that do not.
func setNoFraming(h http.Header) {
	h.Set("Content-Security-Policy", "frame-ancestors 'none'")
	h.Set("X-Frame-Options", "DENY")
}

// reportSystemProcessCopies warns about every deployed definition outside the system
// project that carries the id of a system process. None can be deployed any more, and
// none has a system process's rights; one that exists was deployed by a server that
// still ran such a copy in the system process's place, so the operator must look at
// who deployed it and what it did. Runs while the server is built, before the loop
// serves.
func (s *Server) reportSystemProcessCopies() {
	for _, key := range s.order {
		d := s.deployments[key]
		if d == nil || !s.systemPIDs[d.ProcessID] || d.ProjectID == systemProjectID {
			continue
		}
		logging.Warn(logging.DeploymentTookSystemProcessID,
			"a definition outside the system project carries the id of an atlas system process; it no longer acts as one, but an earlier version of atlas ran it with the system process's rights — check who deployed it and what it did, then delete it",
			slog.Uint64("deploymentKey", d.Key),
			slog.String("processId", d.ProcessID),
			slog.Int64("version", int64(d.Version)),
			slog.String("projectId", d.ProjectID),
			slog.String("deployedBy", d.DeployedBy),
		)
	}
}

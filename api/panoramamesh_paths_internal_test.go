package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The Starmap's structural reading (ADR-0211 §1): a call edge follows the
// override the engine itself would follow, and a store the reading depends on that
// cannot be read is an error — never a smaller landscape that looks complete.

// panoramaMeshPathsCaller calls "child" from one call activity.
const panoramaMeshPathsCaller = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="caller" name="Caller" isExecutable="true">
    <startEvent id="s"/>
    <callActivity id="callChild">
      <extensionElements><zeebe:calledElement processId="child" bindingType="latest"/></extensionElements>
    </callActivity>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="callChild"/>
    <sequenceFlow id="f2" sourceRef="callChild" targetRef="e"/>
  </process>
</definitions>`

// panoramaMeshPathsDeploy deploys one model and returns its key.
func panoramaMeshPathsDeploy(t *testing.T, s *Server, model string) uint64 {
	t.Helper()
	code, body := recertifyHTTPPathsCall(t, s.Handler(), http.MethodPost, "/api/v1/deployments", strings.NewReader(model))
	if code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal([]byte(body), &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	return dep.Key
}

// panoramaMeshPathsRead takes one structural reading on the loop, as the landscape does.
func panoramaMeshPathsRead(s *Server, withDrafts bool) (*meshFacts, error) {
	var (
		facts *meshFacts
		err   error
	)
	s.do(func() { facts, err = s.landscapeFacts(withDrafts, time.Now()) })
	return facts, err
}

// TestPanoramaMeshACallEdgeFollowsARedirect. The engine sends a call to "child"
// wherever the override says; an edge drawn to "child" itself would show a
// dependency the engine does not have.
func TestPanoramaMeshACallEdgeFollowsARedirect(t *testing.T) {
	srv := newServerForErrors(t)
	panoramaMeshPathsDeploy(t, srv, pendingWorkPathsTaskBPMN("child"))
	standIn := panoramaMeshPathsDeploy(t, srv, pendingWorkPathsTaskBPMN("stand-in"))
	panoramaMeshPathsDeploy(t, srv, panoramaMeshPathsCaller)
	var err error
	srv.do(func() {
		err = srv.callOverrides.Save(callOverride{CalledProcessID: "child", Action: overrideRedirect,
			TargetProcessID: "stand-in", UpdatedAt: 1})
	})
	if err != nil {
		t.Fatalf("save override: %v", err)
	}

	facts, err := panoramaMeshPathsRead(srv, false)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, p := range facts.procs {
		if p.processID != "caller" {
			continue
		}
		if len(p.calls) != 1 || p.calls[0].TargetKey != standIn {
			t.Errorf("calls = %+v, want the edge to land on stand-in (%d)", p.calls, standIn)
		}
		return
	}
	t.Fatalf("no caller in %+v", facts.procs)
}

// TestPanoramaMeshALandscapeMissingAPartIsNotServed. What the Starmap reads per
// request — the applications, the workers, the catalogue — and the structure it
// hangs them on: if any of them cannot be read the route answers 500 naming the
// failure, rather than a picture with a hole in it that nothing points out.
func TestPanoramaMeshALandscapeMissingAPartIsNotServed(t *testing.T) {
	for _, tc := range []struct {
		name string
		dir  func(*Server) string
	}{
		{"the structure", func(s *Server) string { return s.callOverrides.Dir() }},
		{"the applications", func(s *Server) string { return s.projects.Dir() }},
		{"the workers", func(s *Server) string { return s.connectors.Dir() }},
		{"the catalogues", func(s *Server) string { return filepath.Join(s.dataDir, "catalog", "catalogs") }},
		{"the products", func(s *Server) string { return filepath.Join(s.dataDir, "catalog", "items") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServerForErrors(t)
			recertifyHTTPPathsBreakDir(t, tc.dir(srv))
			code, body := recertifyHTTPPathsCall(t, srv.Handler(), http.MethodGet, "/api/v1/panorama/mesh", nil)
			if code != http.StatusInternalServerError || !strings.Contains(body, "collect starmap") {
				t.Errorf("mesh = %d %s, want 500", code, body)
			}
		})
	}
}

// TestPanoramaMeshAStoreThatCannotBeReadFailsTheReading. The overrides decide
// where edges go, the drafts are what was asked for, the targets are the peers to
// ask: missing any of them, the picture would be wrong in a way it cannot say.
func TestPanoramaMeshAStoreThatCannotBeReadFailsTheReading(t *testing.T) {
	for _, tc := range []struct {
		name       string
		dir        func(*Server) string
		withDrafts bool
	}{
		{"the call overrides", func(s *Server) string { return s.callOverrides.Dir() }, false},
		{"the drafts", func(s *Server) string { return s.drafts.Dir() }, true},
		{"the targets", func(s *Server) string { return filepath.Join(s.dataDir, "targets") }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServerForErrors(t)
			recertifyHTTPPathsBreakDir(t, tc.dir(srv))
			if facts, err := panoramaMeshPathsRead(srv, tc.withDrafts); err == nil {
				t.Errorf("reading = %+v, nil; want an error", facts)
			}
		})
	}
}

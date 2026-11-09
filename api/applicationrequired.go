package api

import "net/http"

// Every process, decision and form belongs to an application
// (ADR-0544).
//
// ADR-0034 made the application tag optional so that the artifacts written before
// applications existed kept working without a migration, and ADR-0128 kept it that
// way. The cost showed on the Modeler home: the "Not assigned" bucket grew larger
// than any application, and what sits in it cannot be published, exported, moved to
// another server or purged with the application it was written for — the four things
// an application exists to do.
//
// So the tag is no longer optional for anything new: a draft, form, decision draft or
// decision reference is not created, a process or decision is not deployed, and
// nothing is moved, outside an application. What is already unfiled stays as it is —
// it may be edited, its instances run on — until it is adopted into an application
// (POST /api/v1/applications/{id}/adopt). It is never filed anywhere silently,
// because which application a process belongs to is a decision about who owns it.

// applicationRequiredByDefault is what every server is built with. Only the api
// package's own tests lower it (TestMain): the engine behaviour they cover is not
// about filing, and hundreds of them deploy a bare model. The tests of this rule
// build their server with withApplicationRequired, and
// TestAServerRequiresAnApplicationByDefault holds the production value.
var applicationRequiredByDefault = applicationRequiredInProduction

// applicationRequiredInProduction is the value a server outside this package's tests
// runs with. A constant, so the tests that lower the default cannot lower it.
const applicationRequiredInProduction = true

// withApplicationRequired builds a server that enforces the rule whatever the
// default is, for the tests that cover it.
func withApplicationRequired() Option {
	return func(s *Server) { s.applicationRequired = true }
}

// unfiledRefusal refuses creating or deploying what (a sentence's subject, e.g. "a
// draft") outside an application. projectID is where it would be filed once every
// inheritance has been applied; whether that application exists, and whether the
// caller may write to it, is the target-scope check's question (ADR-0071), asked
// separately.
func (s *Server) unfiledRefusal(projectID, what string) (int, string) {
	if !s.applicationRequired || projectID != "" {
		return 0, ""
	}
	return http.StatusBadRequest, what + " must belong to an application: name one with projectId " +
		"(create one with POST /api/v1/applications if there is none yet)"
}

// unfileRefusal refuses taking something out of its application, which the move
// routes used to do when they were sent an empty projectId.
func (s *Server) unfileRefusal(projectID string) (int, string) {
	if !s.applicationRequired || projectID != "" {
		return 0, ""
	}
	return http.StatusBadRequest, "an artifact cannot be taken out of its application: " +
		"move it into another application instead"
}

// unfiled reports whether an artifact tagged projectID belongs to no application:
// it carries no tag, or the application it names no longer exists. Both are listed
// under Not assigned, and both may be adopted.
func unfiled(projectID string, projs map[string]project) bool {
	if projectID == "" {
		return true
	}
	_, ok := projs[projectID]
	return !ok
}

// deployedApplicationOf is the application the newest deployed version of processID
// is filed under, when that application still exists; empty otherwise. Must be called
// on the run loop.
func (s *Server) deployedApplicationOf(processID string) (string, error) {
	for i := len(s.order) - 1; i >= 0; i-- {
		d := s.deployments[s.order[i]]
		if d == nil || d.ProcessID != processID || d.ProjectID == "" {
			continue
		}
		_, ok, err := s.projects.Get(d.ProjectID)
		if err != nil || !ok {
			return "", err
		}
		return d.ProjectID, nil
	}
	return "", nil
}

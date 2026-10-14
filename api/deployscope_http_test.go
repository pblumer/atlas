package api_test

import (
	"fmt"
	"net/http"
	"testing"
)

// deployableBPMN is a minimal executable process under a caller-chosen id, so a
// test can deploy two definitions that differ in nothing but identity.
func deployableBPMN(procID string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:process id="%s" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements><zeebe:taskDefinition type="work" retries="3"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`, procID)
}

// TestRawDeployRequiresProjectMembership is the raw-deploy half of ADR-0071's
// "filing an artifact into a project is a write on that project". The project
// deploy path (POST /applications/{id}/deployments) has always checked it; POST
// /deployments checked only that the named project *existed*, so a global
// modeler role was enough to write a definition into any private project whose
// id the caller had — and the refused caller must leave nothing behind.
func TestRawDeployRequiresProjectMembership(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	if login(t, admin, ts, "admin", "password1") != http.StatusOK {
		t.Fatal("admin login")
	}
	createUserWithRoles(t, admin, ts.URL, "outsider", `["modeler"]`)
	outsider := signInAs(t, ts.URL, "outsider", "a-password-that-is-long")

	code, body := cReq(t, admin, ts, "POST", "/api/v1/projects", `{"name":"Private application"}`)
	if code != http.StatusOK {
		t.Fatalf("create project: %d %s", code, body)
	}
	id := decodeProject(t, body).ID

	// Fixture: the project really is invisible to the outsider.
	if code, _ := cReq(t, outsider, ts, "GET", "/api/v1/projects/"+id, ""); code != http.StatusNotFound {
		t.Fatalf("fixture: outsider can read the project: %d", code)
	}

	code, body = cReq(t, outsider, ts, "POST", "/api/v1/deployments?projectId="+id, deployableBPMN("intruder"))
	if code != http.StatusForbidden && code != http.StatusNotFound {
		t.Fatalf("outsider deployed into a private application: %d %s; want 403/404", code, body)
	}
	if got := deployedProcessIDs(t, admin, ts.URL); got["intruder"] {
		t.Fatal("a refused deploy still registered its definition")
	}
}

// TestRawDeployInheritedProjectRequiresMembership covers the other door into the
// same room: with no ?projectId= the deploy inherits the project of whatever
// draft shares its process id. That inheritance is a write into someone else's
// project just as much as naming it outright, so it needs the same grant.
func TestRawDeployInheritedProjectRequiresMembership(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	if login(t, admin, ts, "admin", "password1") != http.StatusOK {
		t.Fatal("admin login")
	}
	createUserWithRoles(t, admin, ts.URL, "outsider", `["modeler"]`)
	outsider := signInAs(t, ts.URL, "outsider", "a-password-that-is-long")

	code, body := cReq(t, admin, ts, "POST", "/api/v1/projects", `{"name":"Private application"}`)
	if code != http.StatusOK {
		t.Fatalf("create project: %d %s", code, body)
	}
	id := decodeProject(t, body).ID
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/drafts?projectId="+id, deployableBPMN("inherited")); code != http.StatusOK {
		t.Fatalf("file draft: %d %s", code, b)
	}

	code, body = cReq(t, outsider, ts, "POST", "/api/v1/deployments", deployableBPMN("inherited"))
	if code != http.StatusForbidden && code != http.StatusNotFound {
		t.Fatalf("outsider deployed into an inherited private application: %d %s; want 403/404", code, body)
	}
	if got := deployedProcessIDs(t, admin, ts.URL); got["inherited"] {
		t.Fatal("a refused deploy still registered its definition")
	}
}

// TestRawDeployAllowedForMemberAndUngrouped is the other side of the gate: the
// check must not lock out the callers it was never about. An editor member
// deploys into the project, and a deploy that names no project at all stays
// Ungrouped and open.
func TestRawDeployAllowedForMemberAndUngrouped(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	if login(t, admin, ts, "admin", "password1") != http.StatusOK {
		t.Fatal("admin login")
	}
	memberID := createUserWithRoles(t, admin, ts.URL, "member", `["modeler"]`)
	member := signInAs(t, ts.URL, "member", "a-password-that-is-long")

	code, body := cReq(t, admin, ts, "POST", "/api/v1/projects", `{"name":"Shared application"}`)
	if code != http.StatusOK {
		t.Fatalf("create project: %d %s", code, body)
	}
	id := decodeProject(t, body).ID
	if code, b := cReq(t, admin, ts, "PUT", "/api/v1/projects/"+id+"/members/"+memberID, `{"role":"editor"}`); code != http.StatusOK {
		t.Fatalf("share as editor: %d %s", code, b)
	}

	if code, b := cReq(t, member, ts, "POST", "/api/v1/deployments?projectId="+id, deployableBPMN("welcome")); code != http.StatusOK {
		t.Fatalf("editor member refused: %d %s", code, b)
	}
	// No project named at all: Ungrouped, and open as it has always been.
	if code, b := cReq(t, member, ts, "POST", "/api/v1/deployments", deployableBPMN("loose")); code != http.StatusOK {
		t.Fatalf("ungrouped deploy refused: %d %s", code, b)
	}
	got := deployedProcessIDs(t, admin, ts.URL)
	if !got["welcome"] || !got["loose"] {
		t.Fatalf("expected both definitions to be registered, got %v", got)
	}
}

// TestRawDeployViewerCannotDeploy pins the boundary at editor: read access to a
// project is not permission to publish a runnable definition into it.
func TestRawDeployViewerCannotDeploy(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	if login(t, admin, ts, "admin", "password1") != http.StatusOK {
		t.Fatal("admin login")
	}
	viewerID := createUserWithRoles(t, admin, ts.URL, "onlooker", `["modeler"]`)
	viewer := signInAs(t, ts.URL, "onlooker", "a-password-that-is-long")

	code, body := cReq(t, admin, ts, "POST", "/api/v1/projects", `{"name":"Shared application"}`)
	if code != http.StatusOK {
		t.Fatalf("create project: %d %s", code, body)
	}
	id := decodeProject(t, body).ID
	if code, b := cReq(t, admin, ts, "PUT", "/api/v1/projects/"+id+"/members/"+viewerID, `{"role":"viewer"}`); code != http.StatusOK {
		t.Fatalf("share as viewer: %d %s", code, b)
	}

	if code, b := cReq(t, viewer, ts, "POST", "/api/v1/deployments?projectId="+id, deployableBPMN("readonly")); code != http.StatusForbidden {
		t.Fatalf("viewer deployed into a project: %d %s; want 403", code, b)
	}
	if got := deployedProcessIDs(t, admin, ts.URL); got["readonly"] {
		t.Fatal("a refused deploy still registered its definition")
	}
}

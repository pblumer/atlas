package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// The ADR-0128 application tools drive the whole design-time loop an agent needs:
// create an application, file a draft into it, publish it as a versioned release,
// then read the release history and what is deployed. The registry tests only
// prove these tools are advertised and dispatchable; this drives them against a
// real Atlas so their request paths and payloads are exercised end to end.

// applicationBPMN is a straight-through executable process, so publishing an
// application holding it actually deploys something.
const applicationBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="mcp-app-proc" isExecutable="true">
    <startEvent id="s"/>
    <endEvent id="e"/>
    <sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`

func TestApplicationToolsPublishLoop(t *testing.T) {
	atlas := newAtlas(t)

	// Create → the application shows up in the listing.
	created := callOne(t, atlas, "atlas_create_application", map[string]any{"name": "MCP Onboarding"})
	var app struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(created), &app); err != nil || app.ID == "" {
		t.Fatalf("create application = %q (err=%v), want an id", created, err)
	}
	if app.Name != "MCP Onboarding" {
		t.Errorf("created application name = %q, want MCP Onboarding", app.Name)
	}
	if listed := callOne(t, atlas, "atlas_list_applications", map[string]any{}); !strings.Contains(listed, app.ID) {
		t.Fatalf("list applications = %s, want the new application", listed)
	}

	// File a draft into it, then publish the application as one bundle.
	if saved := callOne(t, atlas, "atlas_save_draft", map[string]any{
		"xml": applicationBPMN, "projectId": app.ID,
	}); !strings.Contains(saved, "mcp-app-proc") {
		t.Fatalf("save draft = %s, want the process id", saved)
	}

	published := callOne(t, atlas, "atlas_publish_application", map[string]any{
		"id": app.ID, "note": "first release",
	})
	var pub struct {
		Deployed bool `json:"deployed"`
		Release  *struct {
			Version int32  `json:"version"`
			Note    string `json:"note"`
		} `json:"release"`
	}
	if err := json.Unmarshal([]byte(published), &pub); err != nil {
		t.Fatalf("decode publish = %q: %v", published, err)
	}
	if !pub.Deployed || pub.Release == nil {
		t.Fatalf("publish = %s, want deployed with a release", published)
	}
	if pub.Release.Version != 1 || pub.Release.Note != "first release" {
		t.Fatalf("release = %+v, want v1 with the note", pub.Release)
	}

	// The history reports it, and the deployments view reports what went live.
	releases := callOne(t, atlas, "atlas_application_releases", map[string]any{"id": app.ID})
	var hist []struct {
		Version int32 `json:"version"`
	}
	if err := json.Unmarshal([]byte(releases), &hist); err != nil {
		t.Fatalf("decode releases = %q: %v", releases, err)
	}
	if len(hist) != 1 || hist[0].Version != 1 {
		t.Fatalf("releases = %s, want exactly v1", releases)
	}

	// The release carries its architecture snapshot (ADR-0471), readable as data.
	architecture := callOne(t, atlas, "atlas_release_architecture", map[string]any{"id": app.ID, "version": 1})
	var arch struct {
		Version      int32 `json:"version"`
		Architecture struct {
			Processes []struct {
				ProcessID string `json:"processId"`
			} `json:"processes"`
		} `json:"architecture"`
	}
	if err := json.Unmarshal([]byte(architecture), &arch); err != nil {
		t.Fatalf("decode architecture = %q: %v", architecture, err)
	}
	if arch.Version != 1 || len(arch.Architecture.Processes) != 1 || arch.Architecture.Processes[0].ProcessID != "mcp-app-proc" {
		t.Fatalf("architecture = %s, want v1 with its one process", architecture)
	}
	// The layers argument narrows the answer the way the route's ?layers= does.
	logical := callOne(t, atlas, "atlas_release_architecture", map[string]any{"id": app.ID, "version": 1, "layers": "application"})
	var narrowed struct {
		Layers struct {
			Shown []string `json:"shown"`
		} `json:"layers"`
		Architecture struct {
			Physical json.RawMessage `json:"physical"`
		} `json:"architecture"`
	}
	if err := json.Unmarshal([]byte(logical), &narrowed); err != nil {
		t.Fatalf("decode narrowed architecture = %q: %v", logical, err)
	}
	if len(narrowed.Layers.Shown) != 1 || narrowed.Layers.Shown[0] != "application" || narrowed.Architecture.Physical != nil {
		t.Fatalf("architecture with layers=application = %s, want the application layer alone", logical)
	}

	deployments := callOne(t, atlas, "atlas_application_deployments", map[string]any{"id": app.ID})
	var view struct {
		Version     int32 `json:"version"`
		Definitions []struct {
			ProcessID string `json:"processId"`
			Active    bool   `json:"active"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal([]byte(deployments), &view); err != nil {
		t.Fatalf("decode deployments = %q: %v", deployments, err)
	}
	if view.Version != 1 {
		t.Errorf("deployments view version = %d, want 1", view.Version)
	}
	if len(view.Definitions) != 1 || view.Definitions[0].ProcessID != "mcp-app-proc" || !view.Definitions[0].Active {
		t.Fatalf("deployments view = %s, want one active mcp-app-proc definition", deployments)
	}

	// Publishing again advances the application version.
	republished := callOne(t, atlas, "atlas_publish_application", map[string]any{"id": app.ID})
	if !strings.Contains(republished, `"version":2`) {
		t.Fatalf("second publish = %s, want version 2", republished)
	}
}

// TestDeployApplicationToolDeploysWithoutRelease covers the lower-level deploy
// tool: it registers the bundle but, unlike publish, records no release.
func TestDeployApplicationToolDeploysWithoutRelease(t *testing.T) {
	atlas := newAtlas(t)
	created := callOne(t, atlas, "atlas_create_application", map[string]any{"name": "Deploy Only"})
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(created), &app); err != nil || app.ID == "" {
		t.Fatalf("create application = %q (err=%v)", created, err)
	}
	if saved := callOne(t, atlas, "atlas_save_draft", map[string]any{
		"xml": applicationBPMN, "projectId": app.ID,
	}); !strings.Contains(saved, "mcp-app-proc") {
		t.Fatalf("save draft = %s", saved)
	}

	deployed := callOne(t, atlas, "atlas_deploy_application", map[string]any{"id": app.ID})
	if !strings.Contains(deployed, `"deployed":true`) {
		t.Fatalf("deploy application = %s, want deployed true", deployed)
	}
	// No release was minted, so the history stays empty and the view reports v0.
	if releases := callOne(t, atlas, "atlas_application_releases", map[string]any{"id": app.ID}); strings.TrimSpace(releases) != "[]" {
		t.Fatalf("releases after a plain deploy = %s, want []", releases)
	}
}

// TestDeleteApplicationToolIsIdempotent mirrors the project-tool contract: the
// delete confirms, repeats cleanly, and the application leaves the listing.
func TestDeleteApplicationToolIsIdempotent(t *testing.T) {
	atlas := newAtlas(t)
	created := callOne(t, atlas, "atlas_create_application", map[string]any{"name": "Temporary App"})
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(created), &app); err != nil || app.ID == "" {
		t.Fatalf("create application = %q (err=%v)", created, err)
	}

	for range 2 {
		confirmation := callOne(t, atlas, "atlas_delete_application", map[string]any{"id": app.ID})
		if !strings.Contains(confirmation, `"deleted":true`) {
			t.Fatalf("delete confirmation = %s, want deleted true", confirmation)
		}
	}
	if listed := callOne(t, atlas, "atlas_list_applications", map[string]any{}); strings.Contains(listed, app.ID) {
		t.Fatalf("list applications = %s, deleted application still visible", listed)
	}
}

// atlas_application_constants and atlas_server_constants report what the processes require
// of this installation — an application's constants and the installation's own — so an
// agent can explain a start refused for a missing one (ADR-0453,
// ADR-0456).
func TestHeldConstantsTools(t *testing.T) {
	atlas := newAtlas(t)
	created := callOne(t, atlas, "atlas_create_application", map[string]any{"name": "MCP Constants"})
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(created), &app); err != nil || app.ID == "" {
		t.Fatalf("create application = %q (err=%v), want an id", created, err)
	}
	const bound = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0">
  <process id="mcp-bound" isExecutable="true">
    <extensionElements><atlas:constants>
      <atlas:constant name="TENANT_ID" type="string" source="application"/>
      <atlas:constant name="REGION" source="server"/>
    </atlas:constants></extensionElements>
    <startEvent id="s"/>
    <endEvent id="e"/>
    <sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`
	callOne(t, atlas, "atlas_save_draft", map[string]any{"xml": bound, "projectId": app.ID})
	callOne(t, atlas, "atlas_publish_application", map[string]any{"id": app.ID})

	got := callOne(t, atlas, "atlas_application_constants", map[string]any{"id": app.ID})
	var view struct {
		Required []struct {
			Name      string   `json:"name"`
			Processes []string `json:"processes"`
			State     string   `json:"state"`
		} `json:"required"`
	}
	if err := json.Unmarshal([]byte(got), &view); err != nil {
		t.Fatalf("decode constants = %q: %v", got, err)
	}
	if len(view.Required) != 1 || view.Required[0].Name != "TENANT_ID" || view.Required[0].State != "missing" ||
		len(view.Required[0].Processes) != 1 || view.Required[0].Processes[0] != "mcp-bound" {
		t.Fatalf("constants = %s, want TENANT_ID missing, required by mcp-bound", got)
	}

	got = callOne(t, atlas, "atlas_server_constants", map[string]any{})
	view.Required = nil
	if err := json.Unmarshal([]byte(got), &view); err != nil {
		t.Fatalf("decode server constants = %q: %v", got, err)
	}
	if len(view.Required) != 1 || view.Required[0].Name != "REGION" || view.Required[0].State != "missing" {
		t.Fatalf("server constants = %s, want REGION missing", got)
	}
}

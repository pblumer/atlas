package mcp_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// callTolerant calls one tool and returns its text plus whether it was a tool
// error, so a test can assert on a refusal — the shape half of this file needs.
func callTolerant(t *testing.T, ts *httptest.Server, name string, args map[string]any) (string, bool) {
	t.Helper()
	resps := run(t, ts, callTool(1, name, args))
	return toolText(t, result(t, resps[0]))
}

// infomodelDataBPMN carries a typed data object an agent would want to look the
// type up for, and parks so the instance stays running.
const infomodelDataBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="orders" isExecutable="true">
    <dataObject id="DO_order" name="order" itemSubjectRef="Order"><dataState name="received"/></dataObject>
    <dataObjectReference id="Ref_w" name="order" dataObjectRef="DO_order"><dataState name="approved"/></dataObjectReference>
    <startEvent id="s"/>
    <scriptTask id="seed"><extensionElements><zeebe:script expression="= 100" resultVariable="amount"/></extensionElements></scriptTask>
    <task id="record">
      <dataOutputAssociation id="doa"><targetRef>Ref_w</targetRef><assignment><from>= amount</from></assignment></dataOutputAssociation>
    </task>
    <intermediateCatchEvent id="w"><timerEventDefinition><timeDuration>PT3600S</timeDuration></timerEventDefinition></intermediateCatchEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="seed"/>
    <sequenceFlow id="f2" sourceRef="seed" targetRef="record"/>
    <sequenceFlow id="f3" sourceRef="record" targetRef="w"/>
    <sequenceFlow id="f4" sourceRef="w" targetRef="e"/>
  </process>
</definitions>`

// TestInformationModelToolsScenario drives the whole information-model surface the
// way an agent would: read the rules, create a model, write classes into it, read
// the derived schema, and find the running instances carrying that type. It is the
// flow the tools exist for — an agent that writes itemSubjectRef="Order" needs
// somewhere to say what an Order is.
func TestInformationModelToolsScenario(t *testing.T) {
	atlas := newAtlas(t)

	// 1. The rules, before writing anything against them.
	subsetJSON := callOne(t, atlas, "atlas_infomodel_subset", map[string]any{})
	var subset struct {
		Version     int `json:"version"`
		Stereotypes []struct {
			Stereotype string `json:"stereotype"`
		} `json:"stereotypes"`
		Matrix map[string][]string `json:"matrix"`
		Limits []struct {
			Area string `json:"area"`
		} `json:"limits"`
	}
	if err := json.Unmarshal([]byte(subsetJSON), &subset); err != nil {
		t.Fatalf("decode subset: %v (%s)", err, subsetJSON)
	}
	if subset.Version == 0 || len(subset.Stereotypes) != 3 || len(subset.Limits) == 0 {
		t.Fatalf("subset = %+v", subset)
	}
	// The matrix is what an agent has instead of a canvas that greys out a line.
	if got := subset.Matrix["enumeration>businessObject"]; len(got) != 0 {
		t.Errorf("matrix says an enumeration may be related to: %v", got)
	}

	// 2. An application to own the model, then the model.
	appJSON := callOne(t, atlas, "atlas_create_application", map[string]any{"name": "Sales"})
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(appJSON), &app); err != nil || app.ID == "" {
		t.Fatalf("create application: %v (%s)", err, appJSON)
	}
	createdJSON := callOne(t, atlas, "atlas_create_information_model", map[string]any{
		"applicationId": app.ID, "name": "Sales data", "documentation": "What the order process moves.",
	})
	var created struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
	}
	if err := json.Unmarshal([]byte(createdJSON), &created); err != nil || created.ID == "" {
		t.Fatalf("create model: %v (%s)", err, createdJSON)
	}

	// 3. Write the vocabulary. Ids are left empty, so the server mints them and
	//    rewrites the association end that pointed at the placeholder.
	savedJSON := callOne(t, atlas, "atlas_save_information_model", map[string]any{
		"id":       created.ID,
		"revision": created.Revision,
		"classes": []any{
			map[string]any{
				"id": "tmp-order", "name": "Order", "stereotype": "businessObject",
				"identity": []any{"id"},
				"attributes": []any{
					map[string]any{"name": "id", "type": "string", "multiplicity": "1"},
					map[string]any{"name": "total", "type": "number", "multiplicity": "0..1"},
				},
			},
			map[string]any{
				"id": "tmp-line", "name": "OrderLine", "stereotype": "businessObject",
				"attributes": []any{
					map[string]any{"name": "quantity", "type": "number", "multiplicity": "1"},
				},
			},
		},
		"associations": []any{
			map[string]any{
				"id": "tmp-owns", "kind": "composition",
				"from": map[string]any{"classId": "tmp-order", "multiplicity": "1"},
				"to":   map[string]any{"classId": "tmp-line", "role": "lines", "multiplicity": "1..*"},
			},
		},
	})
	var saved struct {
		Revision int64 `json:"revision"`
		Classes  []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"classes"`
		Associations []struct {
			From struct {
				ClassID string `json:"classId"`
			} `json:"from"`
		} `json:"associations"`
		Validation struct {
			Valid bool `json:"valid"`
		} `json:"validation"`
	}
	if err := json.Unmarshal([]byte(savedJSON), &saved); err != nil {
		t.Fatalf("decode save: %v (%s)", err, savedJSON)
	}
	if !saved.Validation.Valid || saved.Revision != created.Revision+1 {
		t.Fatalf("save = %+v", saved)
	}
	var orderID string
	for _, c := range saved.Classes {
		if strings.HasPrefix(c.ID, "tmp-") {
			t.Errorf("class %s kept the agent's placeholder id %q", c.Name, c.ID)
		}
		if c.Name == "Order" {
			orderID = c.ID
		}
	}
	if saved.Associations[0].From.ClassID != orderID {
		t.Errorf("the association end was not remapped: %q, want %q", saved.Associations[0].From.ClassID, orderID)
	}

	// 4. A model the subset refuses comes back as a tool error rather than being
	//    stored — the agent's equivalent of the canvas refusing a line mid-drag.
	if _, isErr := callTolerant(t, atlas, "atlas_save_information_model", map[string]any{
		"id": created.ID,
		"classes": []any{map[string]any{
			"id": "c1", "name": "Address", "stereotype": "valueType", "identity": []any{"city"},
			"attributes": []any{map[string]any{"name": "city", "type": "string", "multiplicity": "1"}},
		}},
	}); !isErr {
		t.Error("a value type declaring a business key was accepted")
	}

	// 5. The derived contract, and what deriving it cost.
	schemaJSON := callOne(t, atlas, "atlas_information_model_schema", map[string]any{
		"id": created.ID, "class": "Order",
	})
	var projection struct {
		Class  string         `json:"class"`
		Schema map[string]any `json:"schema"`
		Loss   []struct {
			Area string `json:"area"`
		} `json:"loss"`
	}
	if err := json.Unmarshal([]byte(schemaJSON), &projection); err != nil {
		t.Fatalf("decode schema: %v (%s)", err, schemaJSON)
	}
	if projection.Class != "Order" || projection.Schema["type"] != "object" {
		t.Fatalf("projection = %+v", projection)
	}
	if len(projection.Loss) == 0 {
		t.Error("the projection reported no loss; the business key alone cannot survive it")
	}

	// 6. Listing and reading it back the way another agent would find it.
	listJSON := callOne(t, atlas, "atlas_list_information_models", map[string]any{"applicationId": app.ID})
	var list []struct {
		ID      string `json:"id"`
		Classes int    `json:"classes"`
	}
	if err := json.Unmarshal([]byte(listJSON), &list); err != nil {
		t.Fatalf("decode list: %v (%s)", err, listJSON)
	}
	if len(list) != 1 || list[0].ID != created.ID || list[0].Classes != 2 {
		t.Fatalf("list = %+v", list)
	}
	getJSON := callOne(t, atlas, "atlas_get_information_model", map[string]any{"id": created.ID})
	if !strings.Contains(getJSON, `"OrderLine"`) {
		t.Errorf("reading the model back lost a class: %s", getJSON)
	}

	// 7. The other direction: which running instances carry an Order right now.
	callOne(t, atlas, "atlas_deploy", map[string]any{"xml": infomodelDataBPMN})
	var procs []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal([]byte(callOne(t, atlas, "atlas_list_processes", map[string]any{})), &procs); err != nil {
		t.Fatalf("list processes: %v", err)
	}
	callOne(t, atlas, "atlas_create_instance", map[string]any{"key": procs[0].Key})

	readIndex := func(args map[string]any) struct {
		Objects []struct {
			Name        string `json:"name"`
			ItemType    string `json:"itemType"`
			State       string `json:"state"`
			InstanceKey uint64 `json:"instanceKey"`
			Key         string `json:"key"`
		} `json:"objects"`
		Scanned   int  `json:"scanned"`
		Truncated bool `json:"truncated"`
		History   bool `json:"history"`
	} {
		t.Helper()
		out := struct {
			Objects []struct {
				Name        string `json:"name"`
				ItemType    string `json:"itemType"`
				State       string `json:"state"`
				InstanceKey uint64 `json:"instanceKey"`
				Key         string `json:"key"`
			} `json:"objects"`
			Scanned   int  `json:"scanned"`
			Truncated bool `json:"truncated"`
			History   bool `json:"history"`
		}{}
		raw := callOne(t, atlas, "atlas_data_objects", args)
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatalf("decode data objects: %v (%s)", err, raw)
		}
		return out
	}

	index := readIndex(map[string]any{"class": "Order"})
	if len(index.Objects) != 1 || index.Objects[0].ItemType != "Order" ||
		index.Objects[0].State != "approved" || index.Objects[0].InstanceKey == 0 {
		t.Fatalf("data objects = %+v", index.Objects)
	}
	// The answer says what it looked at, so a truncated sweep cannot be read as the
	// whole truth.
	if index.Scanned != 1 || index.Truncated || index.History {
		t.Errorf("sweep = %+v", index)
	}
	// A class nothing declares is an empty list, not an error.
	if got := readIndex(map[string]any{"class": "Invoice"}); len(got.Objects) != 0 {
		t.Errorf("filtering by an unmodeled class = %+v, want none", got.Objects)
	}
	// The process was deployed outside any application, so nothing resolves its
	// class to an identity and no key can be matched on.
	if got := readIndex(map[string]any{"key": "ORD-1"}); len(got.Objects) != 0 {
		t.Errorf("a key matched without an identity to match on: %+v", got.Objects)
	}
	if got := readIndex(map[string]any{"history": true}); !got.History {
		t.Error("the answer does not say it swept history")
	}

	// 8. The same instance, drawn as objects: the class diagram's run-time twin.
	graphJSON := callOne(t, atlas, "atlas_instance_object_graph", map[string]any{"key": index.Objects[0].InstanceKey})
	var graph struct {
		Nodes []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
			Class string `json:"class"`
			State string `json:"state"`
		} `json:"nodes"`
		Degraded bool `json:"degraded"`
	}
	if err := json.Unmarshal([]byte(graphJSON), &graph); err != nil {
		t.Fatalf("decode object graph: %v (%s)", err, graphJSON)
	}
	if len(graph.Nodes) != 1 {
		t.Fatalf("object graph = %+v, want the one object the instance carries", graph.Nodes)
	}
	// The process was deployed outside any application, so nothing resolves its
	// declared type — the graph says it is showing less than it could rather than
	// inventing a class.
	if !graph.Degraded || graph.Nodes[0].Class != "" || graph.Nodes[0].Label != "order" {
		t.Errorf("node = %+v (degraded=%v)", graph.Nodes[0], graph.Degraded)
	}
	if graph.Nodes[0].State != "approved" {
		t.Errorf("state = %q, want the data state the write advanced it to", graph.Nodes[0].State)
	}

	// 9. And it can be removed.
	if out := callOne(t, atlas, "atlas_delete_information_model", map[string]any{"id": created.ID}); !strings.Contains(out, "deleted") {
		t.Errorf("delete said %q", out)
	}
	if _, isErr := callTolerant(t, atlas, "atlas_get_information_model", map[string]any{"id": created.ID}); !isErr {
		t.Error("reading a deleted model succeeded")
	}
}

// TestInformationModelToolArgumentErrors covers the required-argument guards, so a
// missing id is a tool error naming it rather than a request to a malformed path.
func TestInformationModelToolArgumentErrors(t *testing.T) {
	atlas := newAtlas(t)
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"atlas_get_information_model", map[string]any{}},
		{"atlas_create_information_model", map[string]any{}},
		{"atlas_create_information_model", map[string]any{"applicationId": "a"}},
		{"atlas_save_information_model", map[string]any{}},
		{"atlas_save_information_model", map[string]any{"id": "x", "revision": 0}},
		{"atlas_delete_information_model", map[string]any{}},
		{"atlas_information_model_schema", map[string]any{}},
		{"atlas_information_model_schema", map[string]any{"id": "x"}},
		{"atlas_instance_object_graph", map[string]any{}},
	} {
		if _, isErr := callTolerant(t, atlas, tc.tool, tc.args); !isErr {
			t.Errorf("%s(%v): want a tool error, got success", tc.tool, tc.args)
		}
	}
}

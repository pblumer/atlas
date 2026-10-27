package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// ADR-0495 slice C: a release records its data layer — the data objects its processes
// declare with the elements that read and write them, the stores they name, the
// classes they keep data as, and the personal data they declare with the workers they
// hand it to.

// dataBPMN declares a typed and an untyped data object, a store, and personal data,
// and hands work to a Worker.
const dataBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <itemDefinition id="Item_Order" structureRef="Order"/>
  <itemDefinition id="Item_Note" structureRef="Notiz"/>
  <process id="bestellung" name="Bestellung" isExecutable="true" atlas:personal="email,name" atlas:dataSubject="kunde">
    <dataObject id="DO_order" name="order" itemSubjectRef="Item_Order"/>
    <dataObject id="DO_note" name="note" itemSubjectRef="Item_Note"/>
    <dataObjectReference id="Ref_w" name="order" dataObjectRef="DO_order"/>
    <dataObjectReference id="Ref_r" name="order" dataObjectRef="DO_order"/>
    <dataStoreReference id="Store_Ref" name="Kundenstamm"/>
    <startEvent id="Start"/>
    <task id="Record">
      <dataOutputAssociation id="doa"><targetRef>Ref_w</targetRef>
        <assignment><from>= "A-1"</from><to>id</to></assignment></dataOutputAssociation>
    </task>
    <serviceTask id="Notify">
      <extensionElements><atlas:clioConnector connector="events" subject="bestellung/neu" eventType="BestellungEingegangen"/></extensionElements>
      <dataInputAssociation id="dia"><sourceRef>Ref_r</sourceRef><targetRef>orderCopy</targetRef></dataInputAssociation>
    </serviceTask>
    <endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Record"/>
    <sequenceFlow id="f2" sourceRef="Record" targetRef="Notify"/>
    <sequenceFlow id="f3" sourceRef="Notify" targetRef="End"/>
  </process>
</definitions>`

type dataRead struct {
	Layers struct {
		Recorded []string `json:"recorded"`
	} `json:"layers"`
	Architecture struct {
		Data *struct {
			Objects []struct {
				Name      string   `json:"name"`
				Class     string   `json:"class"`
				ReadBy    []string `json:"readBy"`
				WrittenBy []struct {
					ElementID string   `json:"elementId"`
					Members   []string `json:"members"`
				} `json:"writtenBy"`
			} `json:"objects"`
			Stores []struct {
				Name   string `json:"name"`
				Class  string `json:"class"`
				UsedBy []struct {
					ElementID string `json:"elementId"`
				} `json:"usedBy"`
			} `json:"stores"`
			Classes []struct {
				Name    string `json:"name"`
				Modeled bool   `json:"modeled"`
			} `json:"classes"`
			Personal []struct {
				ProcessID string   `json:"processId"`
				Variables []string `json:"variables"`
				Subject   string   `json:"subject"`
				HandedTo  []struct {
					Kind string `json:"kind"`
					Name string `json:"name"`
				} `json:"handedTo"`
			} `json:"personal"`
		} `json:"data"`
	} `json:"architecture"`
}

func TestAReleaseRecordsItsDataLayer(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications", `{"name":"Bestellwesen"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create application: status=%d body=%s", code, body)
	}
	var app struct{ ID string }
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatal(err)
	}
	// The application's information model defines Order and the store that keeps it;
	// Notiz is only a type the process model declares.
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/infomodel/models",
		fmt.Sprintf(`{"applicationId":%q,"name":"Bestelldaten"}`, app.ID), "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create information model: status=%d body=%s", code, body)
	}
	var model struct{ ID string }
	if err := json.Unmarshal(body, &model); err != nil {
		t.Fatal(err)
	}
	const content = `{"classes":[{"id":"c1","name":"Order","stereotype":"businessObject","identity":["id"],
	  "attributes":[{"name":"id","type":"string","multiplicity":"1"}]}],
	  "stores":[{"id":"s1","name":"Kundenstamm","class":"Order","mode":"read"}]}`
	if code, b := doReq(t, ts, http.MethodPut, "/api/v1/infomodel/models/"+model.ID, content, "application/json"); code != http.StatusOK {
		t.Fatalf("save the model: status=%d body=%s", code, b)
	}
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/drafts?projectId="+app.ID, dataBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("save draft: status=%d body=%s", code, b)
	}
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/applications/"+app.ID+"/publish", `{}`, "application/json"); code != http.StatusOK {
		t.Fatalf("publish: status=%d body=%s", code, b)
	}

	base := "/api/v1/applications/" + app.ID + "/releases/1/architecture"
	code, body = doReq(t, ts, http.MethodGet, base+"?layers=data", "", "")
	if code != http.StatusOK {
		t.Fatalf("architecture: status=%d body=%s", code, body)
	}
	var read dataRead
	if err := json.Unmarshal(body, &read); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(read.Layers.Recorded, ","); got != "strategy,business,application,data,technology" {
		t.Errorf("recorded layers = %s, want all five", got)
	}
	d := read.Architecture.Data
	if d == nil {
		t.Fatalf("no data part in %s", body)
	}
	if len(d.Objects) != 2 || d.Objects[1].Name != "order" || d.Objects[1].Class != "Order" ||
		len(d.Objects[1].ReadBy) != 1 || d.Objects[1].ReadBy[0] != "Notify" ||
		len(d.Objects[1].WrittenBy) != 1 || d.Objects[1].WrittenBy[0].ElementID != "Record" ||
		strings.Join(d.Objects[1].WrittenBy[0].Members, ",") != "id" || d.Objects[0].Class != "Notiz" {
		t.Errorf("objects = %+v, want note and order with order's reader, writer and member", d.Objects)
	}
	if len(d.Stores) != 1 || d.Stores[0].Name != "Kundenstamm" || d.Stores[0].Class != "Order" || d.Stores[0].UsedBy[0].ElementID != "Store_Ref" {
		t.Errorf("stores = %+v, want Kundenstamm keeping Order, named at Store_Ref", d.Stores)
	}
	if len(d.Classes) != 2 || d.Classes[0].Name != "Notiz" || d.Classes[0].Modeled || d.Classes[1].Name != "Order" || !d.Classes[1].Modeled {
		t.Errorf("classes = %+v, want Notiz not modeled and Order modeled", d.Classes)
	}
	if len(d.Personal) != 1 || strings.Join(d.Personal[0].Variables, ",") != "email,name" || d.Personal[0].Subject != "kunde" ||
		len(d.Personal[0].HandedTo) != 1 || d.Personal[0].HandedTo[0].Name != "events" {
		t.Errorf("personal = %+v, want email and name about kunde, handed to events", d.Personal)
	}

	// The picture and the document carry it when the data layer is asked for, and not
	// otherwise.
	_, svg := doReq(t, ts, http.MethodGet, base+".svg?layers=application,data", "", "")
	for _, want := range []string{"Personal data of bestellung", "email, name", "readable in plaintext by events", "keeps class Order"} {
		if !strings.Contains(string(svg), want) {
			t.Errorf("the sketch with the data layer lacks %q", want)
		}
	}
	_, plain := doReq(t, ts, http.MethodGet, base+".svg", "", "")
	if strings.Contains(string(plain), "Personal data of") || !strings.Contains(string(plain), "Switched off for this picture: strategy, business, data.") {
		t.Error("the default sketch draws the data layer, or does not say it is switched off")
	}
	_, xml := doReq(t, ts, http.MethodGet, base+".xml?layers=data", "", "")
	if !strings.Contains(string(xml), `xsi:type="BusinessObject"`) || !strings.Contains(string(xml), `accessType="ReadWrite"`) {
		t.Errorf("the document with the data layer lacks its business object or access type:\n%s", xml)
	}
}

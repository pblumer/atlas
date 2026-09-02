package infomodel

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// TestNewStoreRefusesAnUnusableDirectory covers the one way opening a store fails:
// the directory cannot be created because a file is already sitting on the path.
func TestNewStoreRefusesAnUnusableDirectory(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "in-the-way")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	if _, err := NewStore(blocked); err == nil {
		t.Error("opening a store over a file succeeded")
	}
}

// TestServiceDeleteAccessRules mirrors the read and write rules for the one verb
// that removes work: a model the caller cannot see is a 404, one they may only read
// is a 403, and a protected application refuses outright.
func TestServiceDeleteAccessRules(t *testing.T) {
	fx := newFixture(t)
	id := fx.create(t, "app-1", "Sales").ID

	del := func(target string, status int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/infomodel/models/"+target, nil)
		req.SetPathValue("id", target)
		invoke(t, fx.service.HandleDelete, req, status)
	}

	del("0000000000000000000000000000dead", http.StatusNotFound)
	fx.access["app-1"] = ApplicationAccess{Exists: true}
	del(id, http.StatusNotFound)
	fx.access["app-1"] = ApplicationAccess{Exists: true, CanView: true}
	del(id, http.StatusForbidden)
	fx.access["app-1"] = ApplicationAccess{Exists: true, CanView: true, CanEdit: true, Protected: true}
	del(id, http.StatusForbidden)
	fx.access["app-1"] = ApplicationAccess{Exists: true, CanView: true, CanEdit: true}
	del(id, http.StatusNoContent)
}

// TestServiceRefusesAnOversizedBody covers the cap. A class diagram a person can
// read is small, so meeting this cap means something other than modeling is going on.
func TestServiceRefusesAnOversizedBody(t *testing.T) {
	fx := newFixture(t)
	huge := bytes.Repeat([]byte("x"), maxJSONBytes+1)
	request(t, fx.service.HandleCreate, http.MethodPost, "/api/v1/infomodel/models",
		bytes.NewReader(huge), http.StatusRequestEntityTooLarge)
}

// TestServiceRecordsTheActor pins that a save carries who made it. A model is a
// shared statement about the business, so "who last changed this" is part of it.
func TestServiceRecordsTheActor(t *testing.T) {
	fx := newFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/infomodel/models",
		jsonBody(t, map[string]any{"applicationId": "app-1", "name": "Sales"}))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(httpapi.WithPrincipal(req.Context(), &httpapi.Principal{Username: "patrick"}))

	rec := invoke(t, fx.service.HandleCreate, req, http.StatusCreated)
	var out Summary
	decodeResponse(t, rec, &out)
	if out.CreatedBy != "patrick" || out.UpdatedBy != "patrick" {
		t.Errorf("actor = %q/%q, want patrick", out.CreatedBy, out.UpdatedBy)
	}
	_ = context.Background()
}

// TestServiceIDMintingFailure covers the two places an id is needed and the mint
// can refuse: starting a model, and saving a canvas that drew a new box.
func TestServiceIDMintingFailure(t *testing.T) {
	fx := newFixture(t)
	id := fx.create(t, "app-1", "Sales").ID

	fx.idErr = os.ErrClosed
	requestJSON(t, fx.service.HandleCreate, http.MethodPost, "/api/v1/infomodel/models",
		map[string]any{"applicationId": "app-1", "name": "Other"}, http.StatusInternalServerError)

	// A class with no id needs one minted; an association with no id does too.
	fx.putModel(t, id, map[string]any{
		"classes": []Class{{Name: "Invoice", Stereotype: StereotypeBusinessObject}},
	}, http.StatusInternalServerError)
	fx.putModel(t, id, map[string]any{
		"classes": []Class{{ID: "c1", Name: "Invoice", Stereotype: StereotypeBusinessObject}},
		"associations": []Association{{Kind: KindAssociation,
			From: End{ClassID: "c1"}, To: End{ClassID: "c1"}}},
	}, http.StatusInternalServerError)
}

// TestSchemaProjectsAggregationAndCompositeKeys covers the two projection paths the
// order fixture does not reach: an aggregation (reported, never embedded) and a
// business key made of more than one attribute.
func TestSchemaProjectsAggregationAndCompositeKeys(t *testing.T) {
	m := Model{
		ID: "m", Name: "Warehouse",
		Classes: []Class{
			{ID: "c1", Name: "Bin", Stereotype: StereotypeBusinessObject,
				Identity: []string{"aisle", "slot"},
				Attributes: []Attribute{
					{Name: "aisle", Type: TypeString, Multiplicity: MultOne},
					{Name: "slot", Type: TypeNumber, Multiplicity: MultOne},
				}},
			{ID: "c2", Name: "Pallet", Stereotype: StereotypeBusinessObject,
				Attributes: []Attribute{{Name: "code", Type: TypeString, Multiplicity: MultOne}}},
		},
		Associations: []Association{{ID: "a1", Kind: KindAggregation,
			From: End{ClassID: "c1", Multiplicity: MultOne},
			To:   End{ClassID: "c2", Role: "pallets", Multiplicity: MultMany}}},
	}
	if res := Validate(m); !res.Valid {
		t.Fatalf("fixture is invalid: %v", findingCodes(res))
	}

	schema, p := mustSchema(t, m, "Bin")
	props := schema["properties"].(map[string]any)
	// Aggregated parts go on existing without the whole, so they are not in its value.
	if props["pallets"] != nil {
		t.Errorf("an aggregation was embedded: %v", props["pallets"])
	}
	if !strings.Contains(lossAreas(p), "Aggregation") {
		t.Errorf("loss = %v, want the aggregation reported", p.Loss)
	}
	// A composite key names both parts, joined, so the note says what identity means
	// for this class rather than only that identity was dropped.
	var keyNote string
	for _, n := range p.Loss {
		if n.Area == "Business key" {
			keyNote = n.Reason
		}
	}
	if !strings.Contains(keyNote, "Bin.aisle + Bin.slot") {
		t.Errorf("business-key note = %q, want both key parts named", keyNote)
	}
}

// TestSchemaSkipsAnUnnamedContainment covers a composition drawn but not yet named:
// there is no property for it to be, so it is left out rather than guessed at, and
// an end whose multiplicity is unset defaults to optional.
func TestSchemaSkipsAnUnnamedContainment(t *testing.T) {
	m := Model{
		ID: "m", Name: "Doc",
		Classes: []Class{
			{ID: "c1", Name: "Folder", Stereotype: StereotypeBusinessObject},
			{ID: "c2", Name: "Sheet", Stereotype: StereotypeBusinessObject},
			{ID: "c3", Name: "Note", Stereotype: StereotypeBusinessObject},
		},
		Associations: []Association{
			{ID: "a1", Kind: KindComposition, From: End{ClassID: "c1"}, To: End{ClassID: "c2"}},
			{ID: "a2", Kind: KindComposition, Name: "notes",
				From: End{ClassID: "c1"}, To: End{ClassID: "c3"}},
		},
	}
	if res := Validate(m); !res.Valid {
		t.Fatalf("fixture is invalid: %v", findingCodes(res))
	}
	schema, _ := mustSchema(t, m, "Folder")
	props := schema["properties"].(map[string]any)
	if len(props) != 1 {
		t.Fatalf("properties = %v, want only the named containment", props)
	}
	// The association's own name stands in when the end has no role.
	notes, _ := props["notes"].(map[string]any)
	if notes["$ref"] != "#/$defs/Note" {
		t.Errorf("notes = %v, want a $ref to Note", notes)
	}
	if schema["required"] != nil {
		t.Errorf("required = %v, want none: an end with no multiplicity is optional", schema["required"])
	}
}

// TestValidateAttributeTypedAsItself covers the one attribute type that resolves
// and is still wrong: a value that contains its own kind has no end.
func TestValidateAttributeTypedAsItself(t *testing.T) {
	m := orderModel()
	m.Classes[1].Attributes[3].Type = "Order"
	if res := Validate(m); !hasCode(res, CodeAttributeTypedAsSelf) {
		t.Errorf("a self-typed attribute was accepted: %v", findingCodes(res))
	}
}

// TestValidateStructuralDuplicatesAndBlanks covers the remaining shape checks: two
// classes or associations sharing an id, and a nameless attribute.
func TestValidateStructuralDuplicatesAndBlanks(t *testing.T) {
	m := orderModel()
	m.Classes = append(m.Classes, Class{ID: "c1", Name: "Other", Stereotype: StereotypeBusinessObject})
	if res := Validate(m); !hasCode(res, CodeDuplicateClassID) {
		t.Errorf("a duplicate class id was accepted: %v", findingCodes(res))
	}

	m = orderModel()
	m.Associations = append(m.Associations, Association{ID: "a1", Kind: KindAssociation,
		From: End{ClassID: "c1"}, To: End{ClassID: "c2"}})
	if res := Validate(m); !hasCode(res, CodeDuplicateAssociationID) {
		t.Errorf("a duplicate association id was accepted: %v", findingCodes(res))
	}

	m = orderModel()
	m.Classes[1].Attributes[0].Name = "  "
	if res := Validate(m); !hasCode(res, CodeMissingAttributeName) {
		t.Errorf("a nameless attribute was accepted: %v", findingCodes(res))
	}

	m = orderModel()
	m.Associations[0].To.Multiplicity = "7"
	if res := Validate(m); !hasCode(res, CodeUnknownMultiplicity) {
		t.Errorf("an unsupported end multiplicity was accepted: %v", findingCodes(res))
	}
}

// TestModelLookupsMiss covers the two finders answering "no" — the branch every
// caller depends on and no happy path reaches.
func TestModelLookupsMiss(t *testing.T) {
	m := orderModel()
	if _, ok := m.ClassByName("Invoice"); ok {
		t.Error("ClassByName found a class that is not there")
	}
	if _, ok := m.ClassByID("nope"); ok {
		t.Error("ClassByID found a class that is not there")
	}
}

// failingReader is the body a request has when the connection dies mid-read.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, os.ErrClosed }

// TestServiceStoreFailures covers what every handler does when the store beneath it
// cannot be read: a 500 that names the operation, rather than an empty list or a
// silently missing model. Removing the directory is what makes the store fail.
func TestServiceStoreFailures(t *testing.T) {
	// A record on disk that will not decode is what makes a *point read* fail; a
	// directory that is not there is what makes a *listing* fail. The two handlers
	// take different paths into the store, so each needs the failure it can meet.
	t.Run("undecodable record", func(t *testing.T) {
		fx := newFixture(t)
		id := fx.create(t, "app-1", "Sales").ID
		if err := os.WriteFile(fx.store.FileFor(id), []byte("{not json"), 0o600); err != nil {
			t.Fatalf("corrupt record: %v", err)
		}
		for _, h := range []http.HandlerFunc{fx.service.HandleGet, fx.service.HandleSchema} {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/infomodel/models/"+id, nil)
			req.SetPathValue("id", id)
			invoke(t, h, req, http.StatusInternalServerError)
		}
		fx.putModel(t, id, classesBody(), http.StatusInternalServerError)

		delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/infomodel/models/"+id, nil)
		delReq.SetPathValue("id", id)
		invoke(t, fx.service.HandleDelete, delReq, http.StatusInternalServerError)
	})

	t.Run("unreadable directory", func(t *testing.T) {
		fx := newFixture(t)
		fx.create(t, "app-1", "Sales")
		if err := os.RemoveAll(fx.store.Dir()); err != nil {
			t.Fatalf("remove store: %v", err)
		}
		request(t, fx.service.HandleList, http.MethodGet, "/api/v1/infomodel/models", nil,
			http.StatusInternalServerError)

		var err error
		fx.service.loop.Do(func() { _, err = fx.service.VocabularyOnLoop("app-1") })
		if err == nil {
			t.Error("VocabularyOnLoop on a missing store: want an error")
		}
	})
}

// TestServiceAuthorizationFailures covers the resolver itself failing. It is a
// different answer from "you may not": the server could not find out, so it says so
// rather than defaulting either way.
func TestServiceAuthorizationFailures(t *testing.T) {
	fx := newFixture(t)
	id := fx.create(t, "app-1", "Sales").ID
	fx.accessErr = os.ErrPermission

	requestJSON(t, fx.service.HandleCreate, http.MethodPost, "/api/v1/infomodel/models",
		map[string]any{"applicationId": "app-1", "name": "Other"}, http.StatusInternalServerError)
	request(t, fx.service.HandleList, http.MethodGet, "/api/v1/infomodel/models", nil, http.StatusInternalServerError)
	fx.get(t, id, http.StatusInternalServerError)
	fx.putModel(t, id, classesBody(), http.StatusInternalServerError)

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/infomodel/models/"+id, nil)
	delReq.SetPathValue("id", id)
	invoke(t, fx.service.HandleDelete, delReq, http.StatusInternalServerError)
}

// TestServiceUnreadableBody covers a request whose body dies mid-read: a bad
// request, not a 500, because the fault is on the wire and not in the server.
func TestServiceUnreadableBody(t *testing.T) {
	fx := newFixture(t)
	request(t, fx.service.HandleCreate, http.MethodPost, "/api/v1/infomodel/models",
		failingReader{}, http.StatusBadRequest)
}

// TestSchemaOfANonObjectClass covers projecting an enumeration directly: it is a set
// of values rather than an object, so its schema is an enum and nothing else.
func TestSchemaOfANonObjectClass(t *testing.T) {
	schema, _ := mustSchema(t, orderModel(), "OrderStatus")
	if schema["enum"] == nil {
		t.Fatalf("schema = %v, want an enum", schema)
	}
	if schema["type"] == "object" {
		t.Error("an enumeration projected as an object")
	}
}

// TestSchemaCarriesDocumentation pins that the words a modeler wrote survive into
// the derived contract — description is the standard's own keyword for them, and a
// schema nobody can read is a schema nobody checks against.
func TestSchemaCarriesDocumentation(t *testing.T) {
	m := orderModel()
	m.Classes[1].Documentation = "A customer's request to buy."
	m.Classes[1].Attributes[1].Documentation = "The day it was placed."
	m.Classes[4].Documentation = "Where an order stands."
	schema, _ := mustSchema(t, m, "Order")
	if schema["description"] != "A customer's request to buy." {
		t.Errorf("class description = %v", schema["description"])
	}
	props := schema["properties"].(map[string]any)
	placedOn, _ := props["placedOn"].(map[string]any)
	if placedOn["description"] != "The day it was placed." {
		t.Errorf("attribute description = %v", placedOn)
	}
	defs := schema["$defs"].(map[string]any)
	enum, _ := defs["OrderStatus"].(map[string]any)
	if enum["description"] != "Where an order stands." {
		t.Errorf("enumeration description = %v", enum)
	}
}

// TestSchemaDocumentsACollection covers the wrap path where documentation rides on
// an array rather than on the value inside it.
func TestSchemaDocumentsACollection(t *testing.T) {
	m := orderModel()
	m.Classes[1].Attributes = append(m.Classes[1].Attributes,
		Attribute{Name: "tags", Type: TypeString, Multiplicity: MultMany, Documentation: "Free labels."})
	schema, _ := mustSchema(t, m, "Order")
	props := schema["properties"].(map[string]any)
	tags, _ := props["tags"].(map[string]any)
	if tags["type"] != "array" || tags["description"] != "Free labels." {
		t.Errorf("tags = %v, want a documented array", tags)
	}
}

package infomodel

import (
	"errors"
	"strings"
	"testing"
)

const bundledOrderModel = `{
  "name": "  Orders  ",
  "classes": [
    {"name":"Order","stereotype":"businessObject",
     "attributes":[{"name":"id","type":"string","multiplicity":"1"}]}
  ]
}`

// TestABundledModelIsFiledUnderItsNameAndReplacedInPlace covers the import path of an
// application bundle's information model: parse, check, file it the first time, find
// it by name, and replace it in place on the next import, keeping id and creation.
func TestABundledModelIsFiledUnderItsNameAndReplacedInPlace(t *testing.T) {
	fx := newFixture(t)

	m, _, err := ParseBundleModel([]byte(bundledOrderModel))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Name != "Orders" {
		t.Fatalf("name = %q, want the trimmed document name", m.Name)
	}
	if err := fx.service.CheckBundleModel(m); err != nil {
		t.Fatalf("a valid bundled model is refused by the check: %v", err)
	}

	if _, found, err := fx.service.ApplicationModel("app-1", "Orders"); err != nil || found {
		t.Fatalf("before the import the model is found = %v, %v", found, err)
	}
	first, err := fx.service.FileBundleModel("app-1", m, "alice")
	if err != nil {
		t.Fatalf("file: %v", err)
	}
	if first.ID == "" || first.Revision != 1 || first.CreatedBy != "alice" || first.ApplicationID != "app-1" {
		t.Fatalf("first import stored %+v", first)
	}

	got, found, err := fx.service.ApplicationModel("app-1", "Orders")
	if err != nil || !found || got.ID != first.ID {
		t.Fatalf("the filed model is not found by name: %+v %v %v", got, found, err)
	}
	if _, found, _ := fx.service.ApplicationModel("other", "Orders"); found {
		t.Error("another application finds this application's model")
	}

	again, _, _ := ParseBundleModel([]byte(bundledOrderModel))
	second, err := fx.service.FileBundleModel("app-1", again, "bob")
	if err != nil {
		t.Fatalf("second file: %v", err)
	}
	if second.ID != first.ID || second.Revision != 2 || second.CreatedBy != "alice" || second.UpdatedBy != "bob" {
		t.Fatalf("the second import did not replace in place: %+v", second)
	}
	if all, _ := fx.service.ModelsForApplicationOnLoop("app-1"); len(all) != 1 {
		t.Fatalf("app-1 owns %d models after two imports, want one", len(all))
	}
}

// TestABundledModelWithoutANameOrUnparseableIsRefused: the name is what a later import
// finds the model by, so a document without one cannot be filed.
func TestABundledModelWithoutANameOrUnparseableIsRefused(t *testing.T) {
	if _, _, err := ParseBundleModel([]byte(`{"name":"   ","classes":[{"name":"Order","stereotype":"businessObject"}]}`)); err == nil || !strings.Contains(err.Error(), "no name") {
		t.Errorf("a nameless model: err = %v", err)
	}
	if _, _, err := ParseBundleModel([]byte(`not json`)); err == nil {
		t.Error("an unreadable document was accepted")
	}
}

// TestAnInvalidBundledModelIsRefusedBeforeAnythingIsWritten: the check and the filing
// refuse what the validator refuses, with the validator's sentences.
func TestAnInvalidBundledModelIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	fx := newFixture(t)
	bad := Model{Name: "Twice", Classes: []Class{
		{Name: "Order", Stereotype: "businessObject"},
		{Name: "Order", Stereotype: "businessObject"},
	}}
	err := fx.service.CheckBundleModel(bad)
	if !errors.Is(err, ErrBundleModelInvalid) {
		t.Fatalf("check err = %v, want ErrBundleModelInvalid", err)
	}
	if _, err := fx.service.FileBundleModel("app-1", bad, "alice"); !errors.Is(err, ErrBundleModelInvalid) {
		t.Fatalf("file err = %v, want ErrBundleModelInvalid", err)
	}
	if all, _ := fx.service.ModelsForApplicationOnLoop("app-1"); len(all) != 0 {
		t.Errorf("a refused model was written: %+v", all)
	}
}

// TestAnExportedModelImportedAgainSaysTheSameThing: the bundle document drops the
// record's fields, and a model read back from it is the same content as the stored one
// — unless its elements still need ids minted.
func TestAnExportedModelImportedAgainSaysTheSameThing(t *testing.T) {
	fx := newFixture(t)
	m, _, _ := ParseBundleModel([]byte(bundledOrderModel))
	stored, err := fx.service.FileBundleModel("app-1", m, "alice")
	if err != nil {
		t.Fatal(err)
	}

	doc, err := BundleDocument(stored)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(doc), stored.ID) || strings.Contains(string(doc), "alice") || !strings.HasSuffix(string(doc), "\n") {
		t.Fatalf("the bundle document carries record fields or lacks a final newline:\n%s", doc)
	}
	back, _, err := ParseBundleModel(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !SameContent(stored, back) {
		t.Error("a model exported and read back is not the same content")
	}
	if string(ContentFingerprint(stored)) != string(ContentFingerprint(back)) {
		t.Error("fingerprints differ")
	}

	fresh, _, _ := ParseBundleModel([]byte(bundledOrderModel))
	if SameContent(stored, fresh) {
		t.Error("a model whose classes still need ids counts as the same content")
	}
	withAssoc := back
	withAssoc.Associations = []Association{{ID: "not-hex"}}
	if SameContent(stored, withAssoc) {
		t.Error("an association without a minted id counts as the same content")
	}
	withStore := back
	withStore.Stores = []DataStore{{ID: "not-hex"}}
	if SameContent(stored, withStore) {
		t.Error("a store without a minted id counts as the same content")
	}
	edited := back
	edited.Name = "changed"
	if SameContent(stored, edited) {
		t.Error("an edited model counts as the same content")
	}
}

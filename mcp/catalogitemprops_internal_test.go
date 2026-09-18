package mcp

import (
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
)

// The product tool's schema against the record it writes.
//
// atlas_save_catalog_product is a full replace and [catalogBody] marshals exactly
// the declared properties, so a field the schema does not name is a field every
// documented read-modify-write CLEARS. That is not a missing feature, it is silent
// data loss: the agent reads the record, sends it back, and the value is gone with
// nothing anywhere saying so.
//
// It happened. `productGroup` was added to the record and to the Console form and
// missed here, so the portal's second column emptied itself for every product an
// agent touched. The Console's own guard
// (TestTheGroupIsMaintainableBesideTheCategory) could not see it, because it reads
// the form and not this schema.
//
// So the guard is derived from the struct rather than from a list somebody
// maintains beside it: a list is the thing that was already wrong.

// itemFieldsAbsentFromTheSchema are the record's fields a caller may not write,
// each with the reason it is not an omission.
//
// One entry, and it earns its place: updatedAt is assigned by the server on every
// write. A caller cannot set it, and a schema advertising it would invite an agent
// to send a value that is discarded — unlike createdAt beside it, which a replace
// really does reset and which the schema therefore names.
var itemFieldsAbsentFromTheSchema = map[string]string{
	"updatedAt": "the server stamps it on every write; a caller cannot set it",
}

// TestTheProductSchemaNamesEveryFieldTheRecordCarries.
//
// Read off the marshalled field names, so renaming a Go field or its tag moves the
// requirement with it instead of leaving this guard checking a name nothing uses.
func TestTheProductSchemaNamesEveryFieldTheRecordCarries(t *testing.T) {
	props := catalogItemProps()
	for _, name := range itemJSONFields(t) {
		if why, ok := itemFieldsAbsentFromTheSchema[name]; ok {
			if _, declared := props[name]; declared {
				t.Errorf("the schema declares %q, which is excluded because %s; either the "+
					"exclusion is stale or the property is wrong", name, why)
			}
			continue
		}
		if _, ok := props[name]; !ok {
			t.Errorf("a product carries %q and atlas_save_catalog_product does not declare it, "+
				"so every documented read-modify-write CLEARS it — the write is a full replace "+
				"and only declared properties are sent", name)
		}
	}
}

// TestTheProductSchemaDeclaresNothingTheRecordHasNot.
//
// The other direction, and it is not symmetry for its own sake: a property whose
// name no field carries is accepted from the caller, marshalled into the body and
// then dropped by the server, so an agent sets a value, reads no error, and finds
// the field unchanged. A misspelling is exactly as silent as the omission above.
func TestTheProductSchemaDeclaresNothingTheRecordHasNot(t *testing.T) {
	carried := map[string]bool{}
	for _, name := range itemJSONFields(t) {
		carried[name] = true
	}
	for name := range catalogItemProps() {
		if !carried[name] {
			t.Errorf("the schema declares %q and a product carries no such field, so a caller "+
				"that sets it is told nothing and changes nothing", name)
		}
	}
}

// itemJSONFields is how a product arrives on the wire: the tag names, which are
// what a tool property has to match.
func itemJSONFields(t *testing.T) []string {
	t.Helper()
	rt := reflect.TypeOf(catalog.Item{})
	var out []string
	for i := range rt.NumField() {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			name = f.Name
		}
		out = append(out, name)
	}
	if len(out) == 0 {
		t.Fatal("a product carries no marshalled field; this guard has lost its subject")
	}
	return out
}

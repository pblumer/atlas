package api

import (
	"slices"
	"testing"
)

// shapeCheck runs a submission, written as the JSON a browser posts, against a
// form-js schema, the way the public start does: decoded into start variables first.
func shapeCheck(t *testing.T, schema, variables string) []string {
	t.Helper()
	vars, err := parseStartVariables([]byte(`{"variables":` + variables + `}`))
	if err != nil {
		t.Fatalf("parse %s: %v", variables, err)
	}
	return formShapeOf([]byte(schema)).notOnForm(vars)
}

// TestPublicStartAcceptsWhatTheFormSubmits pins the shapes form-js produces. A refusal
// here is a public form a visitor can fill in and cannot send, so each case is the data
// the vendored viewer's _getSubmitData builds for that schema.
func TestPublicStartAcceptsWhatTheFormSubmits(t *testing.T) {
	cases := []struct {
		name, schema, vars string
	}{
		{"flat keys",
			`{"components":[{"type":"textfield","key":"customer"},{"type":"number","key":"seats"}]}`,
			`{"customer":"Acme","seats":3}`},
		{"a subset, as a hidden field is left out",
			`{"components":[{"type":"textfield","key":"customer"},{"type":"textfield","key":"passport"}]}`,
			`{"customer":"Acme"}`},
		{"a dotted key nests its value",
			`{"components":[{"type":"textfield","key":"address.street"},{"type":"textfield","key":"address.city"}]}`,
			`{"address":{"street":"Main 1","city":"Bern"}}`},
		{"a group's path nests its fields",
			`{"components":[{"type":"group","path":"contact","components":[{"type":"textfield","key":"phone"}]}]}`,
			`{"contact":{"phone":"031"}}`},
		{"a group without a path leaves its fields where it is",
			`{"components":[{"type":"group","components":[{"type":"textfield","key":"phone"}]}]}`,
			`{"phone":"031"}`},
		{"nested paths compose",
			`{"components":[{"type":"group","path":"a","components":[{"type":"group","path":"b.c","components":[{"type":"textfield","key":"d.e"}]}]}]}`,
			`{"a":{"b":{"c":{"d":{"e":"x"}}}}}`},
		{"a repeating list is an array of items",
			`{"components":[{"type":"dynamiclist","path":"items","isRepeating":true,"components":[{"type":"textfield","key":"sku"},{"type":"number","key":"qty"}]}]}`,
			`{"items":[{"sku":"a","qty":1},{"sku":"b"}]}`},
		{"a list that does not repeat is an object",
			`{"components":[{"type":"dynamiclist","path":"item","isRepeating":false,"components":[{"type":"textfield","key":"sku"}]}]}`,
			`{"item":{"sku":"a"}}`},
		{"a field's structured value is its own",
			`{"components":[{"type":"checklist","key":"tags"},{"type":"filepicker","key":"upload"}]}`,
			`{"tags":["a","b"],"upload":{"name":"x.pdf","any":{"depth":1}}}`},
		{"null anywhere the form has a path",
			`{"components":[{"type":"textfield","key":"address.street"},{"type":"dynamiclist","path":"items","isRepeating":true,"components":[{"type":"textfield","key":"sku"}]}]}`,
			`{"address":null,"items":[null,{"sku":null}]}`},
		{"no submission at all, even without a form",
			``,
			`{}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if bad := shapeCheck(t, c.schema, c.vars); len(bad) > 0 {
				t.Errorf("refused %v; the form submits %s", bad, c.vars)
			}
		})
	}
}

// TestPublicStartRefusesWhatTheFormCannotSubmit is the hole ADR-0029 promised to close:
// an anonymous caller adding a variable, or a member of one, that no field of the form
// produces.
func TestPublicStartRefusesWhatTheFormCannotSubmit(t *testing.T) {
	form := `{"components":[
		{"type":"text","text":"Order"},
		{"type":"textfield","key":"customer"},
		{"type":"textfield","key":"address.street"},
		{"type":"group","path":"contact","components":[{"type":"textfield","key":"phone"}]},
		{"type":"dynamiclist","path":"items","isRepeating":true,"components":[{"type":"textfield","key":"sku"}]},
		{"type":"button","action":"submit","label":"Send"}]}`
	cases := []struct {
		name, vars string
		want       []string
	}{
		{"a variable the form has no field for",
			`{"customer":"Acme","approved":true}`, []string{"approved"}},
		{"every one of them, sorted",
			`{"zeta":1,"alpha":2,"customer":"Acme"}`, []string{"alpha", "zeta"}},
		{"a member beside a dotted key",
			`{"address":{"street":"Main 1","verified":true}}`, []string{"address.verified"}},
		{"a member in a group",
			`{"contact":{"phone":"031","role":"admin"}}`, []string{"contact.role"}},
		{"a group's field outside its path",
			`{"phone":"031"}`, []string{"phone"}},
		{"a member in a list item",
			`{"items":[{"sku":"a"},{"sku":"b","price":0}]}`, []string{"items[1].price"}},
		{"a plain value where the form nests fields",
			`{"address":"Main 1"}`, []string{"address"}},
		{"an object where the form has a list",
			`{"items":{"sku":"a"}}`, []string{"items"}},
		{"an item that is not an object",
			`{"items":["a"]}`, []string{"items[0]"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if bad := shapeCheck(t, form, c.vars); !slices.Equal(bad, c.want) {
				t.Errorf("refused %v, want %v", bad, c.want)
			}
		})
	}
}

// TestAFormThatCannotRenderAcceptsNoField: the schema route serves the form a visitor
// fills in, so a form that is missing or does not parse is one nobody was shown. Its
// link accepts nothing but an empty submission.
func TestAFormThatCannotRenderAcceptsNoField(t *testing.T) {
	for _, schema := range []string{``, `{not json`, `{"type":"default"}`, `{"components":"x"}`} {
		if bad := shapeCheck(t, schema, `{"customer":"Acme"}`); !slices.Equal(bad, []string{"customer"}) {
			t.Errorf("schema %q: refused %v, want [customer]", schema, bad)
		}
	}
}

// TestShapeReadsStoredStructuredValues: the check runs on start variables as they are
// stored, where an object is canonical JSON text. A text that will not decode is
// refused rather than waved through.
func TestShapeReadsStoredStructuredValues(t *testing.T) {
	shape := formShapeOf([]byte(`{"components":[{"type":"textfield","key":"address.street"}]}`))
	vars, err := parseStartVariables([]byte(`{"variables":{"address":{"street":"x"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if bad := shape.notOnForm(vars); len(bad) > 0 {
		t.Fatalf("refused a stored object: %v", bad)
	}
	vars[0].Text = "{broken"
	if bad := shape.notOnForm(vars); !slices.Equal(bad, []string{"address"}) {
		t.Errorf("refused %v, want [address]", bad)
	}
}

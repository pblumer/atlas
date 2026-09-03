package openapimock

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Turning a JSON Schema into the value this mock answers with.
//
// Two rules decide everything here. **The document wins**: a `const`, an `example`, a
// `default` or an `enum` is a person's statement about this field and is used as
// written. And **the same document always produces the same value**: no clock, no
// randomness, no map iteration order. A demo that ran yesterday runs identically today,
// a test can assert on a body, and a diff of two mock runs shows what the caller did
// rather than what the generator felt like.
//
// The generated values are deliberately unmistakable — the Unix epoch, the nil UUID,
// the address ranges RFC 5737 and RFC 3849 reserve for documentation, example.com —
// so nothing this mock produced can be read later as though it had been real.

const (
	// maxDepth bounds a schema that nests without end (a $ref cycle is caught by name,
	// but allOf/items chains can recurse structurally too).
	maxDepth = 12
	// maxItems bounds a generated array. minItems is honoured up to this; a document
	// asking for a thousand items wants a load test, not a mockup.
	maxItems = 5
)

// formatValues are the placeholders for the string formats OpenAPI documents use.
var formatValues = map[string]string{
	"binary":        "",
	"byte":          "YXRsYXM=",
	"date":          "1970-01-01",
	"date-time":     "1970-01-01T00:00:00Z",
	"duration":      "PT0S",
	"email":         "user@example.com",
	"hostname":      "example.com",
	"idn-email":     "user@example.com",
	"ipv4":          "192.0.2.1",
	"ipv6":          "2001:db8::1",
	"password":      "password",
	"time":          "00:00:00Z",
	"uri":           "https://example.com",
	"uri-reference": "/example",
	"url":           "https://example.com",
	"uuid":          "00000000-0000-0000-0000-000000000000",
}

// generator builds example values from the schemas of one document.
//
// active holds the $refs on the current path. A schema that refers to itself — a tree
// node, a pet with a parent pet — is ordinary and must not recurse forever; the second
// visit yields null, which says "there is a field here" without pretending to know how
// deep the real data goes.
type generator struct {
	doc    map[string]any
	active map[string]bool
}

// value builds the example for one schema node.
func (g *generator) value(node any, depth int) (any, error) {
	if depth > maxDepth {
		return nil, nil
	}
	schema, ok := node.(map[string]any)
	if !ok {
		// `true`, `{}` and a missing schema all mean "anything", and the honest
		// example of anything is null.
		return nil, nil
	}
	if ref, ok := schema["$ref"].(string); ok {
		if g.active[ref] {
			return nil, nil
		}
		target, err := g.resolve(ref)
		if err != nil {
			return nil, err
		}
		g.active[ref] = true
		defer delete(g.active, ref)
		return g.value(target, depth+1)
	}
	for _, key := range []string{"const", "example", "default"} {
		if stated, ok := schema[key]; ok {
			return stated, nil
		}
	}
	if enum, ok := schema["enum"].([]any); ok && len(enum) > 0 {
		return enum[0], nil
	}
	if all, ok := schema["allOf"].([]any); ok && len(all) > 0 {
		return g.merge(all, depth)
	}
	for _, key := range []string{"oneOf", "anyOf"} {
		// One of several shapes is a choice this mock cannot make for the caller, so
		// it makes the first one — stated, deterministic, and the one a reader of the
		// document sees first.
		if choice, ok := schema[key].([]any); ok && len(choice) > 0 {
			return g.value(choice[0], depth+1)
		}
	}
	return g.byType(schema, depth)
}

// byType builds the example for a schema that states (or implies) a type.
func (g *generator) byType(schema map[string]any, depth int) (any, error) {
	switch typeOf(schema) {
	case "object":
		return g.object(schema, depth)
	case "array":
		return g.array(schema, depth)
	case "string":
		return stringValue(schema), nil
	case "integer":
		return int64(numberValue(schema)), nil
	case "number":
		return numberValue(schema), nil
	case "boolean":
		return false, nil
	default:
		return nil, nil
	}
}

// object builds one object: every property the schema names, required or not. A mock
// exists to be read, and a response carrying only its required half sends the caller
// looking for a bug that is not there.
func (g *generator) object(schema map[string]any, depth int) (any, error) {
	props, _ := schema["properties"].(map[string]any)
	out := make(map[string]any, len(props))
	for _, name := range sortedKeys(props) {
		value, err := g.value(props[name], depth+1)
		if err != nil {
			return nil, err
		}
		out[name] = value
	}
	// A free-form object stays empty: inventing key names would be inventing the API.
	return out, nil
}

// array builds a list, honouring minItems up to [maxItems].
func (g *generator) array(schema map[string]any, depth int) (any, error) {
	items, ok := schema["items"]
	if !ok {
		return []any{}, nil
	}
	count := 1
	if min, ok := number(schema["minItems"]); ok && int(min) > count {
		count = int(min)
	}
	count = min(count, maxItems)
	out := make([]any, 0, count)
	for range count {
		value, err := g.value(items, depth+1)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, nil
}

// merge combines the members of an allOf. Objects are merged into one, which is what
// allOf means to a reader; anything else takes the first member that produced a value.
func (g *generator) merge(members []any, depth int) (any, error) {
	merged := map[string]any{}
	var other any
	for _, member := range members {
		value, err := g.value(member, depth+1)
		if err != nil {
			return nil, err
		}
		if fields, ok := value.(map[string]any); ok {
			for name, field := range fields {
				merged[name] = field
			}
			continue
		}
		if other == nil {
			other = value
		}
	}
	if len(merged) == 0 && other != nil {
		return other, nil
	}
	return merged, nil
}

// resolve follows a local JSON pointer into the document. A `$ref` this mock cannot
// follow is an error at load time, where a person is watching, rather than a null at
// request time in the middle of a demo.
func (g *generator) resolve(ref string) (any, error) {
	if !strings.HasPrefix(ref, "#/") {
		// A document split across a tree of files is how most large APIs are
		// published, and this mock reads the one file it was given. Loading it anyway
		// would serve every operation in it with an empty body, which looks like a
		// working mock and is not one.
		return nil, fmt.Errorf("$ref %q: this mock reads one file, so only local refs (#/…) resolve — bundle the document into a single file first", ref)
	}
	var node any = g.doc
	for _, token := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		token = strings.ReplaceAll(token, "~1", "/")
		token = strings.ReplaceAll(token, "~0", "~")
		mapping, ok := node.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("$ref %q: cannot be resolved", ref)
		}
		if node, ok = mapping[token]; !ok {
			return nil, fmt.Errorf("$ref %q: cannot be resolved", ref)
		}
	}
	return node, nil
}

// maxRefHops bounds a chain of references that point at each other. A document whose
// refs form a loop is not servable, and following it is not a way to find that out.
const maxRefHops = 8

// deref follows a node that may itself be a $ref, leaving anything else alone, and
// follows a chain of them. Path items, operations, responses and named examples are all
// places a document commonly refs something that is not a schema.
func (g *generator) deref(node any) (any, error) {
	for hops := 0; ; hops++ {
		mapping, ok := node.(map[string]any)
		if !ok {
			return node, nil
		}
		ref, ok := mapping["$ref"].(string)
		if !ok {
			return node, nil
		}
		if hops >= maxRefHops {
			return nil, fmt.Errorf("$ref %q: the references point in a circle", ref)
		}
		var err error
		if node, err = g.resolve(ref); err != nil {
			return nil, err
		}
	}
}

// typeOf reads a schema's type, which OpenAPI 3.1 allows to be a list ("string" or
// null). The first non-null entry is the one to build. With no type at all, the
// keywords say it: properties mean an object, items mean an array.
func typeOf(schema map[string]any) string {
	switch declared := schema["type"].(type) {
	case string:
		return declared
	case []any:
		for _, entry := range declared {
			if name, ok := entry.(string); ok && name != "null" {
				return name
			}
		}
		return "null"
	}
	if _, ok := schema["properties"]; ok {
		return "object"
	}
	if _, ok := schema["items"]; ok {
		return "array"
	}
	return ""
}

// stringValue builds a string: the format's placeholder if the schema names one,
// otherwise the word "string", brought inside minLength/maxLength.
func stringValue(schema map[string]any) string {
	out := "string"
	if format, ok := schema["format"].(string); ok {
		if placeholder, ok := formatValues[format]; ok {
			return placeholder
		}
	}
	if minLen, ok := number(schema["minLength"]); ok && int(minLen) > len(out) {
		out += strings.Repeat("x", int(minLen)-len(out))
	}
	if maxLen, ok := number(schema["maxLength"]); ok && int(maxLen) < len(out) {
		out = out[:int(maxLen)]
	}
	return out
}

// numberValue builds a number inside the schema's bounds, preferring the smallest
// value it allows — a minimum of 1 is usually there because 0 is not a real id.
func numberValue(schema map[string]any) float64 {
	if exclusive, ok := number(schema["exclusiveMinimum"]); ok {
		return exclusive + 1
	}
	minimum, ok := number(schema["minimum"])
	if !ok {
		if maximum, ok := number(schema["maximum"]); ok && maximum < 0 {
			return maximum
		}
		return 0
	}
	// OpenAPI 3.0 spells the exclusive bound as a flag beside `minimum`.
	if flag, ok := schema["exclusiveMinimum"].(bool); ok && flag {
		return minimum + 1
	}
	return minimum
}

// number reads a numeric schema keyword from whichever of Go's number types the JSON
// or YAML decoder produced.
func number(value any) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float64:
		return v, true
	case json.Number:
		parsed, err := v.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

package expr

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"

	"github.com/pblumer/feel/value"
)

// This file is Atlas's bridge between JSON and FEEL values, so structured start
// variables (objects and arrays) can be authored as JSON, persisted as canonical
// JSON text (model.VarJSON), and bound back into FEEL as contexts and lists for
// property access (ADR-0037). Scalars are handled by Classify/FromStored; the
// helpers here own the recursive object/list cases.

// ToJSON encodes a FEEL value as canonical JSON, reporting whether it could. It
// is canonical — object keys are sorted (encoding/json sorts map keys) and
// numbers keep their exact decimal text — so the same value always yields the
// same bytes, which replay depends on. ok is false only if the value contains
// something with no JSON image (it never does for lists/contexts of scalars).
func ToJSON(v Value) (string, bool) {
	b, err := json.Marshal(valueToGo(v))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// ParseJSON decodes canonical JSON text into a FEEL value, the inverse of ToJSON.
// Numbers are read exactly (json.Number) so decimals aren't routed through a
// float before FEEL parses them.
func ParseJSON(text string) (Value, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(text)))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return value.Null, err
	}
	return FromJSON(raw), nil
}

// FromJSON converts a decoded JSON value (map[string]any / []any / json.Number /
// string / bool / nil, as produced by encoding/json with UseNumber) into a FEEL
// value: an object becomes a context, an array a list, recursively. A number
// that fails to parse degrades to FEEL null rather than erroring, matching
// FromStored's defensive contract.
func FromJSON(raw any) Value {
	switch x := raw.(type) {
	case nil:
		return value.Null
	case bool:
		return value.BoolOf(x)
	case string:
		return value.Str(x)
	case json.Number:
		n, err := value.ParseNumber(x.String())
		if err != nil {
			return value.Null
		}
		return n
	case float64:
		// Defensive: a decoder without UseNumber yields float64. Format compactly
		// so the FEEL number parser sees a clean decimal.
		n, err := value.ParseNumber(strconv.FormatFloat(x, 'f', -1, 64))
		if err != nil {
			return value.Null
		}
		return n
	case []any:
		elems := make([]Value, len(x))
		for i, e := range x {
			elems[i] = FromJSON(e)
		}
		return value.NewList(elems...)
	case map[string]any:
		// Insert in sorted key order so the rebuilt context's iteration order is
		// deterministic — Go map ranging is not — which keeps a re-Classify's
		// canonical JSON identical across replays.
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		ctx := value.NewContext()
		for _, k := range keys {
			ctx.Put(k, FromJSON(x[k]))
		}
		return ctx
	default:
		return value.Null
	}
}

// valueToGo maps a FEEL value to the Go shape encoding/json marshals into
// canonical JSON. Numbers become json.Number so their exact decimal text is
// emitted verbatim; a value with no JSON image (temporal, range, function)
// falls back to its canonical FEEL string so nested members stay encodable.
func valueToGo(v Value) any {
	if value.IsNull(v) {
		return nil
	}
	switch v.Kind() {
	case value.KindBool:
		return bool(v.(value.Bool))
	case value.KindNumber:
		return json.Number(v.String())
	case value.KindString:
		return string(v.(value.Str))
	case value.KindList:
		l := v.(value.List)
		out := make([]any, len(l.Elements))
		for i, e := range l.Elements {
			out[i] = valueToGo(e)
		}
		return out
	case value.KindContext:
		c := v.(*value.Context)
		out := make(map[string]any, c.Len())
		for _, k := range c.Keys() {
			member, _ := c.Get(k)
			out[k] = valueToGo(member)
		}
		return out
	default:
		return v.String()
	}
}

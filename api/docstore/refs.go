package docstore

import "encoding/json"

// maxRefDepth bounds how deep FindRefs looks into a value. A reference is a field of a
// record or an element of a list of attachments; a structure deeper than this is not
// where a document is named, and a bound keeps a hostile value from costing a stack.
const maxRefDepth = 16

// RefID returns the document id a value names when it is a reference — a JSON object
// whose "$document" is a well-formed id — and "" otherwise.
func RefID(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	id, _ := m[RefKey].(string)
	if !ValidID(id) {
		return ""
	}
	return id
}

// FindRefs returns the ids of every document reference in a set of decoded variables
// (maps, slices, scalars), each once and in no particular order. For a value held as
// JSON text use FindRefsInJSON.
func FindRefs(vars map[string]any) []string {
	var out []string
	seen := map[string]bool{}
	var walk func(v any, depth int)
	walk = func(v any, depth int) {
		if depth > maxRefDepth {
			return
		}
		switch x := v.(type) {
		case map[string]any:
			if id := RefID(x); id != "" {
				if !seen[id] {
					seen[id] = true
					out = append(out, id)
				}
				return
			}
			for _, e := range x {
				walk(e, depth+1)
			}
		case []any:
			for _, e := range x {
				walk(e, depth+1)
			}
		}
	}
	for _, v := range vars {
		walk(v, 0)
	}
	return out
}

// FindRefsInJSON is FindRefs over one JSON text, for a value stored as text (a JSON
// variable's stored form). Text that is not JSON, or holds no reference, yields none.
func FindRefsInJSON(text string) []string {
	if len(text) == 0 || (text[0] != '{' && text[0] != '[') {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return nil
	}
	return FindRefs(map[string]any{"": v})
}

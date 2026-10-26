package wikiwikibangbang

import (
	"encoding/json"
	"strings"
)

// textToBlocks turns plain text into the wiki's blocks: each run of lines separated by
// an empty line is a paragraph, and a single line break stays inside its paragraph,
// which is how the wiki keeps a hard break. Text that is only whitespace is no blocks.
func textToBlocks(text string) []any {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	var out []any
	var lines []string
	flush := func() {
		if p := strings.TrimSpace(strings.Join(lines, "\n")); p != "" {
			out = append(out, map[string]any{"kind": "paragraph", "text": p})
		}
		lines = lines[:0]
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		lines = append(lines, line)
	}
	flush()
	return out
}

// endsWith reports whether a body's last blocks are add, as the wiki returns blocks: a
// field the wiki leaves out when it is empty is not a difference, and neither are the
// mentions it derives from the marks.
func endsWith(body, add []any) bool {
	if len(add) == 0 || len(body) < len(add) {
		return false
	}
	tail := body[len(body)-len(add):]
	for i := range add {
		if canonical(tail[i]) != canonical(add[i]) {
			return false
		}
	}
	return true
}

// canonical is a block's comparable form: empty values and derived mentions dropped,
// keys in order (encoding/json sorts a map's keys).
func canonical(v any) string {
	raw, err := json.Marshal(prune(v, true))
	if err != nil {
		return ""
	}
	return string(raw)
}

func prune(v any, block bool) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if block && k == "mentions" {
				continue
			}
			if p := prune(val, false); !empty(p) {
				out[k] = p
			}
		}
		return out
	case []any:
		out := make([]any, 0, len(t))
		for _, e := range t {
			out = append(out, prune(e, false))
		}
		return out
	}
	return v
}

func empty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case bool:
		return !t
	case json.Number:
		f, err := t.Float64()
		return err == nil && f == 0
	case float64:
		return t == 0
	case []any:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	}
	return false
}

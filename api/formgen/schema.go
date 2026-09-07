package formgen

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// Turning what a model wrote into a form the editor can open.
//
// Nothing here trusts the answer. It arrived over the network from a third party that
// was asked politely for JSON, and the two things it lands in — a browser rendering it
// with form-js, and a store that keeps it — both deserve a document that has already
// been read. So this file is the gate: the answer is unwrapped from whatever prose came
// with it, checked against the vocabulary a task form can actually render, and given the
// identity the *editor* holds rather than the one the model invented.
//
// What it deliberately does not do is repair meaning. A missing key is derived, because
// a key is a technical name a model has no opinion about; a component form-js cannot
// render is refused by name, because a form quietly missing the field the author asked
// for is worse than one that says it could not be written.

const (
	// maxAnswerBytes bounds what is even parsed. A model that loops — the same field a
	// thousand times — is a real failure mode, and stopping it here costs nothing.
	maxAnswerBytes = 512 << 10
	// maxComponents bounds the form itself, nested components included. Well past any
	// form a person would fill in, and far short of a document that would wedge the
	// editor.
	maxComponents = 250
	// maxKeyRunes bounds a derived key. Labels are sentences often enough.
	maxKeyRunes = 48
)

// renderable is the vocabulary a generated form may use: the form-js components that
// render in a task form with no further wiring, and nothing else.
//
// It is a subset on purpose. form-js 1.24 also has an iframe, an html block, a file
// picker, a document preview and an expression field — each of which needs something the
// generator cannot supply (an origin to embed, sanitized markup, a document store, a
// FEEL expression over variables it has not seen). A model reaching for one of those has
// misunderstood the request, and the honest answer is to say so rather than to ship a
// form with a hole in it.
var renderable = map[string]bool{
	// Says something to the person filling the form in.
	"text": true, "separator": true, "spacer": true,
	// Holds other components.
	"group": true, "dynamiclist": true,
	// Asks something.
	"textfield": true, "textarea": true, "number": true, "checkbox": true,
	"checklist": true, "radio": true, "select": true, "taglist": true, "datetime": true,
}

// keyed lists the components whose answer becomes a process variable, and which
// therefore need a key. The rest are layout and prose.
var keyed = map[string]bool{
	"textfield": true, "textarea": true, "number": true, "checkbox": true,
	"checklist": true, "radio": true, "select": true, "taglist": true, "datetime": true,
}

// holdsComponents lists the layout components whose children are components in their own
// right, so the walk below reaches them.
var holdsComponents = map[string]bool{"group": true, "dynamiclist": true}

// SchemaFrom turns a model's answer into a form-js schema the editor can open, under the
// identity formID names. The returned map is plain JSON values, ready to marshal into a
// response; every error it returns is written to be shown to the author as it stands.
func SchemaFrom(answer, formID string) (map[string]any, error) {
	if len(answer) > maxAnswerBytes {
		return nil, fmt.Errorf("the model answered with %d KB, which is not a form anyone fills in; ask for something smaller",
			len(answer)/1024)
	}
	doc, err := jsonObject(answer)
	if err != nil {
		return nil, err
	}
	// form-js has exactly one root type, and a model that wrote another was describing
	// something else. The author's id wins over whatever the model called the form:
	// the editor is holding this form under an id a user task may already bind, and a
	// generated rename would unbind it on the next save (ADR-0222).
	doc["type"] = "default"
	if formID != "" {
		doc["id"] = formID
	}
	raw, ok := doc["components"]
	if !ok {
		return nil, fmt.Errorf(`the document has no "components" list, so it is not a form`)
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf(`"components" is not a list`)
	}
	n := &normalizer{keys: map[string]bool{}}
	if err := n.walk(list, ""); err != nil {
		return nil, err
	}
	return doc, nil
}

// normalizer carries what the walk has to remember across the whole document: the keys
// already handed out, so two fields cannot land in one variable, and how many components
// have been seen.
type normalizer struct {
	keys  map[string]bool
	count int
}

// walk checks and completes one list of components, recursing into the layout ones.
func (n *normalizer) walk(list []any, path string) error {
	for i, raw := range list {
		where := fmt.Sprintf("component %d", i+1)
		if path != "" {
			where = path + " → " + where
		}
		if n.count++; n.count > maxComponents {
			return fmt.Errorf("the form has more than %d components; that is not a form anyone fills in", maxComponents)
		}
		c, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("%s is not an object", where)
		}
		kind, _ := c["type"].(string)
		if kind == "" {
			return fmt.Errorf(`%s has no "type"`, where)
		}
		if !renderable[kind] {
			return fmt.Errorf("%s is a %q, which a task form cannot render; the form was not generated", where, kind)
		}
		if keyed[kind] {
			c["key"] = n.key(c, kind)
		}
		if holdsComponents[kind] {
			if inner, ok := c["components"].([]any); ok {
				if err := n.walk(inner, where); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// key settles what variable a field's answer lands in. An authored key is kept verbatim
// — naming a field the way the process already names that datum is precisely what the
// outline was handed to the model for — and only a missing or colliding one is derived.
func (n *normalizer) key(c map[string]any, kind string) string {
	k, _ := c["key"].(string)
	k = strings.TrimSpace(k)
	if k != "" && !n.keys[k] {
		n.keys[k] = true
		return k
	}
	if k == "" {
		label, _ := c["label"].(string)
		k = technicalName(label)
		if k == "" {
			k = kind
		}
	}
	base := k
	for i := 2; n.keys[k]; i++ {
		k = fmt.Sprintf("%s%d", base, i)
	}
	n.keys[k] = true
	return k
}

// technicalName turns a label into a variable name: ASCII, lowerCamelCase, bounded. It
// is the name a process will refer to for the life of the model, so it transliterates
// rather than strips — a German label whose umlauts were dropped yields `bergabe`, which
// nobody would have chosen.
func technicalName(label string) string {
	var (
		b     strings.Builder
		upper bool
	)
	for _, r := range strings.TrimSpace(label) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			if b.Len() == 0 && unicode.IsDigit(r) {
				continue // a name may not start with a digit
			}
			if upper {
				b.WriteRune(unicode.ToUpper(r))
				upper = false
			} else if b.Len() == 0 {
				b.WriteRune(unicode.ToLower(r))
			} else {
				b.WriteRune(r)
			}
		default:
			if s, ok := transliteration[r]; ok {
				for i, r2 := range s {
					if upper && i == 0 {
						b.WriteRune(unicode.ToUpper(r2))
						upper = false
						continue
					}
					if b.Len() == 0 {
						b.WriteRune(unicode.ToLower(r2))
						continue
					}
					b.WriteRune(r2)
				}
				continue
			}
			upper = b.Len() > 0
		}
	}
	// Bounded at the end rather than inside the loop, so a label of nothing but
	// transliterated letters is cut too: a key is a name somebody types.
	if r := []rune(b.String()); len(r) > maxKeyRunes {
		return string(r[:maxKeyRunes])
	}
	return b.String()
}

// transliteration covers the letters the languages Atlas is authored in actually use.
// It is a list and not a library: pulling in Unicode folding to spell "ä" would be a
// dependency for one table.
var transliteration = map[rune]string{
	'ä': "ae", 'ö': "oe", 'ü': "ue", 'ß': "ss",
	'Ä': "Ae", 'Ö': "Oe", 'Ü': "Ue",
	'à': "a", 'á': "a", 'â': "a", 'å': "a", 'é': "e", 'è': "e", 'ê': "e", 'ë': "e",
	'í': "i", 'ì': "i", 'î': "i", 'ï': "i", 'ó': "o", 'ò': "o", 'ô': "o", 'ø': "o",
	'ú': "u", 'ù': "u", 'û': "u", 'ç': "c", 'ñ': "n",
	'À': "A", 'Á': "A", 'Â': "A", 'Å': "A", 'É': "E", 'È': "E", 'Ê': "E",
	'Í': "I", 'Ó': "O", 'Ø': "O", 'Ú': "U", 'Ç': "C", 'Ñ': "N",
}

// jsonObject finds the document in an answer that may have arrived wrapped in the habits
// of conversation: a sentence of introduction, a fenced code block, a closing offer to
// adjust it. The document is in there, and failing a good form over its wrapping would
// be a poor trade.
func jsonObject(answer string) (map[string]any, error) {
	s := strings.TrimSpace(answer)
	if fence := strings.Index(s, "```"); fence >= 0 {
		rest := s[fence+3:]
		// ```json — the language tag is on the fence line, not in the document.
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[nl+1:]
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			s = strings.TrimSpace(rest[:end])
		} else {
			s = strings.TrimSpace(rest)
		}
	}
	start, end := strings.IndexByte(s, '{'), strings.LastIndexByte(s, '}')
	if start < 0 || end <= start {
		return nil, errNoJSON
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(s[start:end+1]), &doc); err != nil || doc == nil {
		return nil, errNoJSON
	}
	return doc, nil
}

// errNoJSON is one message for every way an answer can fail to be a document, because
// the author's next move is the same in all of them: read what the model said, and
// either rephrase or try again.
var errNoJSON = fmt.Errorf("the model did not answer with a form: there is no JSON object in what it wrote")

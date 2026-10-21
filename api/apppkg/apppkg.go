// Package apppkg is the application package (ADR-0437, ADR-0467): the directory or
// archive an application and its shop move between installations in, as `atlas
// export` writes it from a terminal and the Console downloads it.
//
//	atlas.json, *.bpmn, *.form.json, *.dmn  the application's source (ADR-0134)
//	konstanten.json                         its constants, as `atlas export` writes them
//	katalog.json                            the catalogues, as one catalogue document
//	fragen.json                             one question per group and person they name
//	antworten.json                          the answers, as the exporting server names them
//
// What turns a catalogue export into a package another installation can take is
// here, once, for both: every group and person a catalogue names becomes a
// {{question}} answered by name, because ids are minted per server and names are
// not; a process a product is bound to travels once; and what cannot travel is
// named. The import half is here too: the answers are found in the target's
// directory, by id or by name, and put in place of the questions.
//
// It works on data, not on the server's types: a field it does not know — written
// by a newer server — travels as it came.
package apppkg

import (
	"bytes"
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// The files of a package, beside the source tree.
const (
	CatalogueFile = "katalog.json"
	QuestionsFile = "fragen.json"
	AnswersFile   = "antworten.json"
	ConstantsFile = "konstanten.json"
)

// Placeholder is a value a catalogue document leaves to the installation it is
// imported into: an audience group, an approver.
var Placeholder = regexp.MustCompile(`\{\{([a-z0-9-]+)\}\}`)

// Question is one entry of fragen.json. Kind says where an answer must be found:
// "group" and "user" in the server's directory, anything else is taken as written.
type Question struct {
	Kind  string            `json:"kind"`
	Label map[string]string `json:"label"`
	Hint  map[string]string `json:"hint"`
}

// Text is the question in English when it has English, else in whatever it has,
// else its name.
func (q Question) Text(name string) string {
	if t := q.Label["en"]; t != "" {
		return t
	}
	for _, lang := range slices.Sorted(maps.Keys(q.Label)) {
		return q.Label[lang]
	}
	return name
}

// DirectoryEntry is one principal of a server's directory, as GET
// /api/v1/principals lists it: a "user" or a "group" by id. An "account" is a user
// by username — what a fixed approver names, because the approval becomes a task
// assigned to that username (catalog/approverreport.go) — with its display name as
// its name; GET /api/v1/users/assignable lists them.
type DirectoryEntry struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Question kinds an answer is looked up for in the directory. Any other kind is
// taken as written.
const (
	KindGroup   = "group"
	KindUser    = "user"
	KindAccount = "account"
)

// directoryKind says a question of this kind is answered from the directory.
func directoryKind(kind string) bool {
	return kind == KindGroup || kind == KindUser || kind == KindAccount
}

// sameID compares an entry's id with a value: a username ignoring case, as an
// assignee is matched, anything else exactly.
func sameID(e DirectoryEntry, value string) bool {
	if e.Type == KindAccount {
		return strings.EqualFold(e.ID, value)
	}
	return e.ID == value
}

// EncodeJSON writes a package file: indented for a diff, newline-terminated, and
// without escaping < and > — a BPMN model in katalog.json stays readable XML.
func EncodeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Decode reads a JSON document as data, numbers kept as written: a document goes
// back out as it came in, a rank or a price included.
func Decode(raw []byte, into any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return dec.Decode(into)
}

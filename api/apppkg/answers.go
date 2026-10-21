package apppkg

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Why an answer does not answer its question.
const (
	ProblemUnanswered = "unanswered"
	ProblemUnknown    = "unknown"
	ProblemAmbiguous  = "ambiguous"
)

// Problem is one question an import cannot answer from what it was given.
type Problem struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Question is the question as fragen.json words it, in English when it can.
	Question string `json:"question"`
	Answer   string `json:"answer,omitempty"`
	// Reason is unanswered, unknown or ambiguous; Detail says it in a sentence.
	Reason string `json:"reason"`
	Detail string `json:"detail"`
	// Candidates are the ids of the principals an ambiguous name matches.
	Candidates []string `json:"candidates,omitempty"`
}

// Asked names the questions a document leaves, each once, in the order it asks them.
func Asked(document []byte) []string {
	var names []string
	for _, m := range Placeholder.FindAllSubmatch(document, -1) {
		if name := string(m[1]); !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

// Resolve answers every question a catalogue document leaves. An answer to a group
// or person question must be one the server's directory has, given by its id or by
// its name: an audience group that does not exist reaches nobody, and the import
// would succeed without saying so. Names are accepted because ids are minted per
// server, so an answer that names "Alle Mitarbeitenden" serves the test installation
// and the production one alike. Every problem is reported at once; the directory is
// read only when a question needs it.
//
// It answers the resolved values by question, notes on how an answer was taken (a
// name found as an id, an answer nothing asks for), and the problems.
func Resolve(document []byte, questions map[string]Question, answers map[string]string, directory func() ([]DirectoryEntry, error)) (map[string]string, []string, []Problem, error) {
	names := Asked(document)
	var (
		problems []Problem
		notes    []string
		dir      []DirectoryEntry
		dirRead  bool
	)
	resolved := map[string]string{}
	for _, name := range names {
		q := questions[name]
		value := strings.TrimSpace(answers[name])
		if value == "" {
			problems = append(problems, Problem{Name: name, Kind: q.Kind, Question: q.Text(name),
				Reason: ProblemUnanswered, Detail: "is not answered"})
			continue
		}
		if !directoryKind(q.Kind) {
			resolved[name] = value
			continue
		}
		if !dirRead {
			var err error
			if dir, err = directory(); err != nil {
				return nil, nil, nil, err
			}
			dirRead = true
		}
		id, candidates, why := FindPrincipal(dir, q.Kind, value)
		if why != "" {
			reason := ProblemUnknown
			if len(candidates) > 0 {
				reason = ProblemAmbiguous
			}
			problems = append(problems, Problem{Name: name, Kind: q.Kind, Question: q.Text(name), Answer: value,
				Reason: reason, Detail: why, Candidates: candidates})
			continue
		}
		if id != value {
			notes = append(notes, fmt.Sprintf("%s: %q is %s %s", name, value, q.Kind, id))
		}
		resolved[name] = id
	}
	for _, k := range slices.Sorted(maps.Keys(answers)) {
		if !slices.Contains(names, k) {
			notes = append(notes, fmt.Sprintf("note: the package does not ask for %s; that answer is not used", k))
		}
	}
	return resolved, notes, problems, nil
}

// FindPrincipal finds a principal of a kind by id, then by name: a group or a user
// by its id, an account by its username. A name two principals share is refused
// rather than guessed: it answers the ids it matches and why it took none. why is
// empty when it found exactly one.
func FindPrincipal(dir []DirectoryEntry, kind, value string) (id string, candidates []string, why string) {
	var byName []DirectoryEntry
	for _, e := range dir {
		if e.Type != kind {
			continue
		}
		if sameID(e, value) {
			return e.ID, nil, ""
		}
		if e.Name == value {
			byName = append(byName, e)
		}
	}
	switch len(byName) {
	case 1:
		return byName[0].ID, nil, ""
	case 0:
		return "", nil, fmt.Sprintf("no %s with the id or name %q on this server", kind, value)
	default:
		ids := make([]string, 0, len(byName))
		for _, e := range byName {
			ids = append(ids, e.ID)
		}
		return "", ids, fmt.Sprintf("%d %ss are named %q; answer with the id of one: %s", len(byName), kind, value, strings.Join(ids, ", "))
	}
}

// Fill puts the answers in place of the questions. Each goes in escaped as a JSON
// string, so an answer cannot break the document it is put in.
func Fill(doc []byte, answers map[string]string) ([]byte, error) {
	filled := Placeholder.ReplaceAllFunc(doc, func(m []byte) []byte {
		name := string(Placeholder.FindSubmatch(m)[1])
		quoted, _ := json.Marshal(answers[name])
		return quoted[1 : len(quoted)-1]
	})
	if !json.Valid(filled) {
		return nil, fmt.Errorf("%s is not JSON", CatalogueFile)
	}
	return filled, nil
}

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

// Mapping is one entry of a target's table of names (ADR-0467, amended): what a
// package's answer of a kind — a group, a person or an account as the exporting
// server names it — means on this server. The table is the target's own, because
// only the target knows that INT's "Fachbereich Bau" is its "FB Bau"; it is filled
// from the choices people make when a name is not found, and read before the
// directory, so the same question is not asked twice.
type Mapping struct {
	Kind   string `json:"kind"`
	Answer string `json:"answer"`
	// ID is the principal it means here: a group's or a user's id, an account's
	// username. Name is that principal's name here, for whoever reads the table.
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// Answers are what an import answers a package's questions with.
type Answers struct {
	// Packaged are antworten.json's: the names the exporting server gives.
	Packaged map[string]string
	// Chosen are the person's, and win: an answers file or --set in a terminal, a
	// choice in the Console's dialog.
	Chosen map[string]string
	// Mappings are this server's table, read for a packaged answer before the
	// directory is.
	Mappings []Mapping
}

// Resolution is how the questions were answered.
type Resolution struct {
	// Values are the answers by question, ready to fill: ids, usernames, text.
	Values map[string]string
	// Notes say how an answer was taken: a name found as an id, a remembered
	// answer, an answer nothing asks for.
	Notes    []string
	Problems []Problem
	// Learned are the choices worth remembering: a packaged answer the directory
	// did not answer by name as the person did. Recorded in the table once the import
	// went through, they answer the same question next time without asking.
	Learned []Mapping
	// Remembered names the questions the table answered.
	Remembered []string
}

// Resolve answers every question a catalogue document leaves. An answer to a group,
// person or account question must be one the server's directory has, given by its
// id (an account's username) or by its name: an audience group that does not exist
// reaches nobody, and the import would succeed without saying so. Names are accepted
// because ids are minted per server, so an answer that names "Alle Mitarbeitenden"
// serves the test installation and the production one alike.
//
// For each question, in this order: the person's choice; for the packaged answer,
// the table's entry, while the principal it names still exists here; the packaged
// answer found in the directory. Every problem is reported at once; the directory is
// read only when a question needs it.
func Resolve(document []byte, questions map[string]Question, given Answers, directory func() ([]DirectoryEntry, error)) (Resolution, error) {
	names := Asked(document)
	res := Resolution{Values: map[string]string{}}
	var (
		dir     []DirectoryEntry
		dirRead bool
	)
	readDir := func() error {
		if dirRead {
			return nil
		}
		var err error
		dir, err = directory()
		dirRead = err == nil
		return err
	}
	remembered := map[string]Mapping{}
	for _, m := range given.Mappings {
		remembered[m.Kind+"\x00"+m.Answer] = m
	}
	for _, name := range names {
		q := questions[name]
		packaged := strings.TrimSpace(given.Packaged[name])
		chosen := strings.TrimSpace(given.Chosen[name])
		value := chosen
		if value == "" {
			value = packaged
		}
		if value == "" {
			res.Problems = append(res.Problems, Problem{Name: name, Kind: q.Kind, Question: q.Text(name),
				Reason: ProblemUnanswered, Detail: "is not answered"})
			continue
		}
		if !directoryKind(q.Kind) {
			res.Values[name] = value
			continue
		}
		if err := readDir(); err != nil {
			return Resolution{}, err
		}
		if chosen == "" {
			if m, ok := remembered[q.Kind+"\x00"+packaged]; ok {
				if entry, found := findByID(dir, q.Kind, m.ID); found && entry.Type == q.Kind {
					res.Values[name] = m.ID
					res.Remembered = append(res.Remembered, name)
					res.Notes = append(res.Notes, fmt.Sprintf("%s: %q is %s %s, as this server remembers", name, packaged, q.Kind, m.ID))
					continue
				}
				res.Notes = append(res.Notes, fmt.Sprintf("%s: this server remembers %q as %s %s, which is no longer here; it is looked up by name", name, packaged, q.Kind, m.ID))
			}
		}
		id, candidates, why := FindPrincipal(dir, q.Kind, value)
		if why != "" {
			reason := ProblemUnknown
			if len(candidates) > 0 {
				reason = ProblemAmbiguous
			}
			res.Problems = append(res.Problems, Problem{Name: name, Kind: q.Kind, Question: q.Text(name), Answer: value,
				Reason: reason, Detail: why, Candidates: candidates})
			continue
		}
		if id != value {
			res.Notes = append(res.Notes, fmt.Sprintf("%s: %q is %s %s", name, value, q.Kind, id))
		}
		res.Values[name] = id
		// A choice that answers the packaged name differently from the directory —
		// or answers what the directory could not — is worth remembering.
		if chosen != "" && packaged != "" {
			if byName, _, _ := FindPrincipal(dir, q.Kind, packaged); byName != id {
				entry, _ := findByID(dir, q.Kind, id)
				res.Learned = append(res.Learned, Mapping{Kind: q.Kind, Answer: packaged, ID: id, Name: entry.Name})
			}
		}
	}
	for _, k := range slices.Sorted(maps.Keys(given.Chosen)) {
		if !slices.Contains(names, k) {
			res.Notes = append(res.Notes, fmt.Sprintf("note: the package does not ask for %s; that answer is not used", k))
		}
	}
	return res, nil
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

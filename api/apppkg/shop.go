package apppkg

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Part is one catalogue as GET /api/v1/catalogs/{id}/document?processes=true answers
// it, decoded as data, and whether it has a release on the exporting server.
type Part struct {
	ID       string
	Document map[string]any
	Released bool
}

// Application is what of the application a package carries that a shop refers to:
// its processes and forms, by id.
type Application struct {
	Processes []string
	Forms     []string
}

// Shop is katalog.json and what goes with it.
type Shop struct {
	Document  []byte
	Questions map[string]Question
	Answers   map[string]string
	Publish   bool
	Products  int
	// Notes say what the import will do; Warnings what will not work on the
	// target as it does here.
	Notes    []string
	Warnings []string
}

// BuildShop makes the catalogues one document and that document a package's: the
// processes the application carries are left to it, the groups and people become
// questions, and what cannot travel is named. dir is the exporting server's
// directory; earlier are the questions an earlier export of the same package
// asked, whose wording is kept when the kind is the same.
func BuildShop(parts []Part, app Application, dir []DirectoryEntry, earlier map[string]Question) (Shop, error) {
	var (
		s          Shop
		catalogs   []any
		products   = map[string]any{}
		processes  = map[string]string{}
		missing    = map[string]bool{}
		unreleased []string
	)
	for _, part := range parts {
		catalogs = append(catalogs, list(part.Document["catalogs"])...)
		for _, p := range list(part.Document["products"]) {
			if pid := str(object(p)["id"]); products[pid] == nil {
				products[pid] = p
			}
		}
		for _, p := range list(part.Document["processes"]) {
			processes[str(object(p)["id"])] = str(object(p)["xml"])
		}
		for _, m := range list(part.Document["missingProcesses"]) {
			missing[str(m)] = true
		}
		if !part.Released {
			unreleased = append(unreleased, part.ID)
		}
	}
	// Sorted, so the same shop is the same file whichever catalogue offered a product
	// first.
	var ordered []any
	for _, pid := range slices.Sorted(maps.Keys(products)) {
		ordered = append(ordered, products[pid])
	}
	s.Products = len(ordered)
	doc := map[string]any{"catalogs": catalogs, "products": ordered}
	if s.Publish = len(unreleased) == 0; s.Publish {
		doc["publish"] = true
	}
	for _, id := range unreleased {
		s.Notes = append(s.Notes, fmt.Sprintf("catalogue %s has no release here, so the import does not publish; publish it on the target once it is complete", id))
	}

	inApp := map[string]bool{}
	for _, id := range app.Processes {
		inApp[id] = true
	}
	var carried []any
	for _, id := range slices.Sorted(maps.Keys(processes)) {
		if inApp[id] {
			continue
		}
		carried = append(carried, map[string]any{"id": id, "xml": processes[id]})
		s.Notes = append(s.Notes, fmt.Sprintf("process %s travels in %s: a product is bound to it, and it is not in the application", id, CatalogueFile))
	}
	if len(carried) > 0 {
		doc["processes"] = carried
	}
	var stillMissing []any
	for _, id := range slices.Sorted(maps.Keys(missing)) {
		if inApp[id] {
			continue
		}
		stillMissing = append(stillMissing, id)
		s.Warnings = append(s.Warnings, fmt.Sprintf("process %s is bound to a product and deployed nowhere on this server; the target refuses to publish the product until a process of that id is deployed there", id))
	}
	if len(stillMissing) > 0 {
		doc["missingProcesses"] = stillMissing
	}
	s.Warnings = append(s.Warnings, unreachable(catalogs, ordered, app)...)

	q := newQuestioner(dir, earlier)
	q.literals(doc)
	q.walk(catalogs, ordered)
	s.Questions, s.Answers = q.questions, q.answers
	s.Warnings = append(s.Warnings, q.warnings...)
	var err error
	if s.Document, err = EncodeJSON(doc); err != nil {
		return s, err
	}
	return s, nil
}

// unreachable names what a product needs that the package does not carry: a form
// outside the application, and a home catalogue the package leaves out.
func unreachable(catalogs, products []any, app Application) []string {
	inApp := map[string]bool{}
	for _, f := range app.Forms {
		inApp[f] = true
	}
	exported := map[string]bool{}
	for _, c := range catalogs {
		exported[str(object(c)["id"])] = true
	}
	var out []string
	for _, p := range products {
		item := object(p)
		pid := str(item["id"])
		forms := []string{str(item["configForm"])}
		for _, a := range list(item["actions"]) {
			forms = append(forms, str(object(a)["form"]))
		}
		var named []string
		for _, f := range forms {
			if f != "" && !inApp[f] && !slices.Contains(named, f) {
				named = append(named, f)
				out = append(out, fmt.Sprintf("form %s is asked by product %s and is not in the application; the target needs a form of that id", f, pid))
			}
		}
		if home := str(item["homeCatalog"]); home != "" && !exported[home] {
			out = append(out, fmt.Sprintf("product %s is maintained in catalogue %s, which the package does not carry; a server without it refuses the product (export catalogue %s with it)", pid, home, home))
		}
	}
	return out
}

// questioner turns the groups and people a shop names into questions.
type questioner struct {
	dir       []DirectoryEntry
	earlier   map[string]Question
	byRef     map[string]string
	uses      map[string][2][]string
	questions map[string]Question
	answers   map[string]string
	warnings  []string
}

func newQuestioner(dir []DirectoryEntry, earlier map[string]Question) *questioner {
	return &questioner{dir: dir, earlier: earlier, byRef: map[string]string{}, uses: map[string][2][]string{},
		questions: map[string]Question{}, answers: map[string]string{}}
}

// literals asks for every {{name}} a text of the shop already carries, and answers
// it with itself. Left alone, the import would take it for a question nobody
// answers and refuse the package; answered with itself, the text arrives as it was.
// The names are taken before any group or person is given one.
func (q *questioner) literals(v any) {
	switch x := v.(type) {
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(x)) {
			q.literals(x[k])
		}
	case []any:
		for _, e := range x {
			q.literals(e)
		}
	case string:
		for _, m := range Placeholder.FindAllStringSubmatch(x, -1) {
			name := m[1]
			if _, asked := q.questions[name]; asked {
				continue
			}
			q.questions[name] = q.keep(name, "text", Question{Kind: "text",
				Label: map[string]string{"de": "Platzhalter «{{" + name + "}}» in einem Text des Katalogs", "en": `The placeholder "{{` + name + `}}" in a text of the catalogue`},
				Hint: map[string]string{"de": "Der Katalog trägt diesen Text wörtlich; die mitgelieferte Antwort setzt ihn unverändert wieder ein.",
					"en": "The catalogue carries this text literally; the answer that comes with it puts it back unchanged."}})
			q.answers[name] = "{{" + name + "}}"
		}
	}
}

// walk asks for every principal the catalogues and products name, in the order a
// reader meets them, and puts the question in its place.
func (q *questioner) walk(catalogs, products []any) {
	for _, c := range catalogs {
		cat := object(c)
		de, en := textIn(cat, "de"), textIn(cat, "en")
		groups := list(cat["groups"])
		for i, g := range groups {
			groups[i] = q.ask("group", str(g), "Zielgruppe des Katalogs «"+de+"»", `audience of the catalogue "`+en+`"`)
		}
		for _, m := range list(cat["members"]) {
			member := object(m)
			ref, role := object(member["ref"]), str(member["role"])
			if ref != nil {
				ref["id"] = q.ask(str(ref["type"]), str(ref["id"]),
					"Mitglied ("+role+") des Katalogs «"+de+"»", "member ("+role+`) of the catalogue "`+en+`"`)
			}
		}
	}
	for _, p := range products {
		item := object(p)
		de, en := textIn(item, "de"), textIn(item, "en")
		if approval := object(item["approval"]); approval != nil {
			// A fixed approver is an account by username, a role a group by id.
			kind := map[string]string{"fixed": KindAccount, "role": KindGroup}[str(approval["kind"])]
			if kind != "" {
				approval["ref"] = q.ask(kind, str(approval["ref"]), "genehmigt «"+de+"»", `approves "`+en+`"`)
			}
		}
		eligible := list(item["eligible"])
		for i, g := range eligible {
			eligible[i] = q.ask("group", str(g), "darf «"+de+"» erhalten", `may receive "`+en+`"`)
		}
	}
	for name, uses := range q.uses {
		question := q.questions[name]
		if _, kept := q.earlier[name]; kept && q.earlier[name].Kind == question.Kind {
			continue
		}
		noun := map[string][2]string{KindGroup: {"Gruppe", "Group"}, KindUser: {"Person", "Person"}, KindAccount: {"Konto", "Account"}}[question.Kind]
		if noun[0] == "" {
			noun = [2]string{question.Kind, question.Kind}
		}
		question.Label = map[string]string{"de": noun[0] + ": " + strings.Join(uses[0], "; "), "en": noun[1] + ": " + strings.Join(uses[1], "; ")}
		q.questions[name] = question
	}
}

// ask returns the {{question}} that stands for a principal, asking it the first
// time the principal is met and noting each use. The kind is the directory's when
// the directory has the id, else the one the field implies. A value that is empty or
// already a question is left as it is.
func (q *questioner) ask(kind, id, useDE, useEN string) string {
	if id == "" || Placeholder.MatchString(id) && "{{"+Placeholder.FindStringSubmatch(id)[1]+"}}" == id {
		return id
	}
	entry, found := findByID(q.dir, kind, id)
	if found {
		kind = entry.Type
	}
	name, asked := q.byRef[kind+"\x00"+id]
	if !asked {
		base := ""
		if found {
			base = slug(entry.Name)
		}
		if base == "" {
			base = slug(id)
		}
		if base == "" {
			base = kind
		}
		name = base
		for n := 2; q.taken(name); n++ {
			name = fmt.Sprintf("%s-%d", base, n)
		}
		q.byRef[kind+"\x00"+id] = name
		hint := map[string]string{"de": "Auf der exportierenden Instanz: «" + entry.Name + "».", "en": `On the exporting instance: "` + entry.Name + `".`}
		if found {
			q.answers[name] = entry.Name
			if same := sameName(q.dir, entry); same > 1 {
				q.warnings = append(q.warnings, fmt.Sprintf("%d %ss are named %q here; if the target has as many, answer %s with an id", same, kind, entry.Name, name))
			}
		} else {
			hint = map[string]string{"de": "Die exportierende Instanz kennt die ID «" + id + "» nicht; die Frage ist unbeantwortet.",
				"en": `The exporting instance does not know the id "` + id + `"; the question has no answer.`}
			q.warnings = append(q.warnings, fmt.Sprintf("%s %s is not in this server's directory; %s in %s has no answer", kind, id, name, QuestionsFile))
		}
		q.questions[name] = q.keep(name, kind, Question{Kind: kind, Hint: hint})
	}
	uses := q.uses[name]
	if !slices.Contains(uses[0], useDE) {
		uses[0], uses[1] = append(uses[0], useDE), append(uses[1], useEN)
		q.uses[name] = uses
	}
	return "{{" + name + "}}"
}

func (q *questioner) taken(name string) bool {
	_, ok := q.questions[name]
	return ok
}

// keep answers the earlier export's question of that name when it asks the same
// kind: its wording may have been improved by hand, and an export must not undo it.
func (q *questioner) keep(name, kind string, fresh Question) Question {
	if prev, ok := q.earlier[name]; ok && prev.Kind == kind {
		return prev
	}
	return fresh
}

// findByID finds a principal by id, preferring the kind the field implies.
func findByID(dir []DirectoryEntry, kind, id string) (DirectoryEntry, bool) {
	var other *DirectoryEntry
	for i, e := range dir {
		if !sameID(e, id) {
			continue
		}
		if e.Type == kind {
			return e, true
		}
		if other == nil {
			other = &dir[i]
		}
	}
	if other != nil {
		return *other, true
	}
	return DirectoryEntry{}, false
}

func sameName(dir []DirectoryEntry, entry DirectoryEntry) int {
	n := 0
	for _, e := range dir {
		if e.Type == entry.Type && e.Name == entry.Name {
			n++
		}
	}
	return n
}

// slug is a question's name from a principal's: lower-case ASCII letters and digits
// joined by hyphens, umlauts written out — the shape an import reads as a
// placeholder.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if t, ok := map[rune]string{'ä': "ae", 'ö': "oe", 'ü': "ue", 'ß': "ss"}[r]; ok {
			b.WriteString(t)
			dash = false
			continue
		}
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// textIn is a catalogue's or product's text in a language, else in the other of
// the two, else in any it has, else its id.
func textIn(v map[string]any, lang string) string {
	texts := object(v["texts"])
	other := map[string]string{"de": "en", "en": "de"}[lang]
	for _, l := range []string{lang, other} {
		if t := str(texts[l]); t != "" {
			return t
		}
	}
	for _, l := range slices.Sorted(maps.Keys(texts)) {
		if t := str(texts[l]); t != "" {
			return t
		}
	}
	return str(v["id"])
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func object(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

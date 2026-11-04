package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/pblumer/atlas/api/apppkg"
	"github.com/pblumer/atlas/api/importplan"
)

// The plan `atlas import` shows before its first write
// (ADR-0467). The server compares what the package
// carries with what it holds — the application's processes, forms, decisions and
// decision models, the shop's catalogues, products and processes — and the command
// compares the application's constants itself. Every artifact is then new,
// unchanged or changed:
//
//   - new is written, unchanged is left alone, so a package installed twice changes
//     nothing the second time and the application is published again only when it
//     changed;
//   - changed is work done on the target — a model edited there, a constant that
//     holds INT's value on INT and PROD's on PROD — and is replaced only when the
//     operator says so: --overwrite or --keep in a pipeline, or a question per
//     artifact at a terminal. In a pipeline nobody can answer, so an undecided change
//     stops the run before the first write, naming each one.

// packageConstants is the file that carries an application's constants beside its
// manifest: their values on the installation the package was exported from, as a
// suggestion for the one it is imported into.
const packageConstants = apppkg.ConstantsFile

// packageConstant is one constant as the Console holds it.
type packageConstant struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

// constantsFile is konstanten.json, the shape PUT /api/v1/applications/{id}/constants
// takes.
type constantsFile struct {
	Constants []packageConstant `json:"constants"`
}

// packageNeed is one thing the application's processes need from a server and no
// package carries, as the manifest lists it and the source import answers it: a
// server constant, an application constant, a configured worker (ADR-0467).
type packageNeed struct {
	Kind      string   `json:"kind"`
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Processes []string `json:"processes"`
	Fallback  bool     `json:"fallback"`
	State     string   `json:"state"`
	Detail    string   `json:"detail"`
}

// what names the need as an operator reads it.
func (n packageNeed) what() string {
	fallback := ""
	if n.Fallback {
		fallback = ", has a fallback"
	}
	switch n.Kind {
	case "server-constant":
		return fmt.Sprintf("server constant %s (%s%s)", n.Name, n.Type, fallback)
	case "application-constant":
		return fmt.Sprintf("application constant %s (%s%s)", n.Name, n.Type, fallback)
	case "worker":
		return fmt.Sprintf("worker %q (%s)", n.Name, n.Type)
	}
	return n.Kind + " " + n.Name
}

// where says where a server sets the need up.
func (n packageNeed) where() string {
	switch n.Kind {
	case "server-constant":
		return "set it in the Console under Admin → Engine → Server constants"
	case "application-constant":
		return "set it on the application's Constants tab"
	}
	return "create a worker of that name and Worker Type under Workers"
}

// open says the server does not answer the need: the value is missing or held with
// another type, the worker is missing, configured as another Worker Type or not
// usable. A fallback that covers a missing constant answers it.
func (n packageNeed) open() bool {
	switch n.State {
	case "missing", "type-mismatch", "other-type", "unusable":
		return true
	}
	return false
}

// how is the state as a phrase, with what the server holds instead.
func (n packageNeed) how() string {
	switch n.State {
	case "type-mismatch":
		return "held here as " + n.Detail
	case "other-type":
		return "configured here as a " + n.Detail + " worker"
	case "unusable":
		return "configured here, not usable: " + n.Detail
	}
	return n.State + " here"
}

// decisionPrompt is where a decision about a changed artifact is asked: standard
// input when it is a terminal, and nobody in a pipeline, where a question would
// hang the job instead of failing it.
var decisionPrompt = func() (io.Reader, bool) {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return nil, false
	}
	return os.Stdin, true
}

// refFlag collects --overwrite or --keep: refs separated by commas, or all.
type refFlag struct {
	refs map[string]bool
	all  bool
}

func (f *refFlag) String() string { return "" }

func (f *refFlag) Set(v string) error {
	for _, ref := range strings.Split(v, ",") {
		switch ref = strings.TrimSpace(ref); ref {
		case "":
		case "all", "*":
			f.all = true
		default:
			if !strings.Contains(ref, ":") {
				return fmt.Errorf("%q is not a ref; name an artifact as kind:id, such as process:onboarding or constant:TENANT_ID", ref)
			}
			f.refs[ref] = true
		}
	}
	return nil
}

func newRefFlag() *refFlag { return &refFlag{refs: map[string]bool{}} }

// mapFlag collects repeated --map ref=key values.
type mapFlag []string

func (f *mapFlag) String() string { return strings.Join(*f, ",") }

func (f *mapFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}

// decisions is what the flags decided, in the server's terms. --adopt has no all:
// taking an artifact out of another application is decided artifact by artifact.
func decisions(overwrite, keep, adopt *refFlag) (importplan.Decisions, error) {
	return decisionsWithGaps(overwrite, keep, adopt, newRefFlag(), nil)
}

// decisionsWithGaps is decisions with what --omit and --map say of the capability
// map's gaps (ADR-draft-application-bundle-architecture). mapped holds ref=key pairs.
func decisionsWithGaps(overwrite, keep, adopt, omit *refFlag, mapped []string) (importplan.Decisions, error) {
	d := importplan.Decisions{Overwrite: overwrite.refs, Keep: keep.refs, OverwriteAll: overwrite.all, KeepAll: keep.all,
		Adopt: adopt.refs, Omit: omit.refs, OmitAll: omit.all, Map: map[string]string{}}
	for _, pair := range mapped {
		for _, one := range strings.Split(pair, ",") {
			if one = strings.TrimSpace(one); one == "" {
				continue
			}
			ref, key, ok := strings.Cut(one, "=")
			if !ok || strings.TrimSpace(ref) == "" || strings.TrimSpace(key) == "" {
				return d, fmt.Errorf("--map takes ref=key, such as capability:konten=identitaet, not %q", one)
			}
			d.Map[strings.TrimSpace(ref)] = strings.TrimSpace(key)
		}
	}
	for ref := range d.Map {
		if d.Adopt[ref] || d.Omit[ref] {
			return d, fmt.Errorf("%s is mapped and also named under --adopt or --omit", ref)
		}
	}
	for ref := range d.Omit {
		if d.Adopt[ref] {
			return d, fmt.Errorf("%s is named under both --adopt and --omit", ref)
		}
	}
	if adopt.all {
		return d, errors.New("--adopt names each artifact to take from another application, as process:ID, form:ID or decision:ID; all is not accepted")
	}
	if d.OverwriteAll && d.KeepAll {
		return d, errors.New("--overwrite all and --keep all contradict each other")
	}
	for ref := range d.Overwrite {
		if d.Keep[ref] {
			return d, fmt.Errorf("%s is named under both --overwrite and --keep", ref)
		}
	}
	return d, nil
}

// planned is one artifact of the plan, with what tells the operator the two
// versions apart when the server's and the package's differ.
type planned struct {
	importplan.Artifact
	detail string
	// decided says the outcome is the operator's — a flag naming the artifact, an
	// "all" over what is changed, or an answer at the terminal — and is sent to the
	// server by name. An outcome that follows from behind or ahead is left to the
	// server, which settles it again where the write happens: an artifact edited
	// in between is then refused rather than replaced.
	decided bool
	// ifAdopted and decidedIfAdopted are an Elsewhere artifact's outcome and
	// decided once it is taken: its content, decided by the flags as any other
	// artifact's is.
	ifAdopted        string
	decidedIfAdopted bool
}

// held says the artifact belongs to another application and is not taken.
func (p planned) held() bool { return p.Status == importplan.Elsewhere && !p.Adopted }

// holder names the application that holds an Elsewhere artifact, as far as the
// server lets the caller see it.
func (p planned) holder() string {
	if p.HeldBy != nil {
		return fmt.Sprintf("application %q", p.HeldBy.Name)
	}
	return "an application you cannot see"
}

func (p planned) label() string {
	if p.Name != "" && p.Name != strings.SplitN(p.Ref, ":", 2)[1] {
		return fmt.Sprintf("%s %q", p.Ref, p.Name)
	}
	return p.Ref
}

// importPlan is everything the package would do to the server, and what the
// server lacks that the application needs.
type importPlan struct {
	source    sourceImportBody
	catalogue catalogueImportBody
	artifacts []planned
	// needs are those the server does not answer, less the application constants
	// the package carries a value for: whether those are set is the constants'
	// import to say.
	needs []packageNeed
}

// plan asks the server for its two plans and compares the constants.
func (c *importClient) plan(pkg importPackage, document []byte) (importPlan, error) {
	var p importPlan
	var err error
	if p.source, err = c.importSource(pkg.archive, "?dryRun=true"); err != nil {
		return p, err
	}
	for _, a := range p.source.Artifacts {
		p.artifacts = append(p.artifacts, planned{Artifact: a})
	}
	carried := map[string]bool{}
	for _, k := range pkg.constants {
		carried[k.Name] = true
	}
	for _, n := range p.source.Needs {
		if n.open() && !(n.Kind == "application-constant" && carried[n.Name]) {
			p.needs = append(p.needs, n)
		}
	}
	if pkg.constants != nil {
		consts, err := c.planConstants(p.source, pkg.constants)
		if err != nil {
			return p, err
		}
		p.artifacts = append(p.artifacts, consts...)
	}
	if document != nil {
		if p.catalogue, err = c.importCatalogue(document, "?dryRun=true"); err != nil {
			return p, err
		}
		for _, a := range p.catalogue.Artifacts {
			p.artifacts = append(p.artifacts, planned{Artifact: a})
		}
	}
	return p, nil
}

// planConstants asks the server how the package's constants compare with those the
// application holds here and with what the last import suggested. An application the
// import creates holds none, and has no plan to ask for: every constant is new. A
// constant the server holds and the package does not name is not touched, and not
// listed.
func (c *importClient) planConstants(src sourceImportBody, consts []packageConstant) ([]planned, error) {
	if src.ApplicationID == "" || src.Created {
		out := make([]planned, 0, len(consts))
		for _, k := range consts {
			out = append(out, planned{Artifact: importplan.Artifact{Ref: "constant:" + k.Name, Name: k.Name, Status: importplan.New}})
		}
		return out, nil
	}
	res, err := c.importConstants(src.ApplicationID, consts, "?dryRun=true")
	if err != nil {
		return nil, err
	}
	held, err := c.constants(src.ApplicationID)
	if err != nil {
		return nil, err
	}
	here := map[string]packageConstant{}
	for _, h := range held.Constants {
		here[h.Name] = h
	}
	suggested := map[string]packageConstant{}
	for _, k := range consts {
		suggested[k.Name] = k
	}
	out := make([]planned, 0, len(res.Artifacts))
	for _, a := range res.Artifacts {
		p := planned{Artifact: a}
		if h, ok := here[a.Name]; ok && a.Status != importplan.Unchanged {
			k := suggested[a.Name]
			p.detail = fmt.Sprintf("here %s %q, in the package %s %q", h.Type, h.Value, k.Type, k.Value)
		}
		out = append(out, p)
	}
	return out, nil
}

// decide settles each artifact's outcome as the flags decide it. An artifact another
// application holds is taken only when --adopt names it, and its content is then
// decided as an artifact of this application's would be (ADR-0467 amendment
// 2026-11-03).
func (p *importPlan) decide(d importplan.Decisions) {
	for i := range p.artifacts {
		a := &p.artifacts[i]
		switch a.Status {
		case importplan.Linked, importplan.Owned:
			// Nothing to decide: the server's outcome stands.
			continue
		case importplan.Gap:
			// A key the capability map lacks: adopted, mapped or omitted as --adopt,
			// --map and --omit say, and named to the server when they say anything.
			a.Outcome, a.MappedTo = d.GapOutcome(a.Ref)
			a.decided = a.Outcome != importplan.Undecided
			continue
		}
		if a.Status == importplan.Elsewhere {
			a.Outcome = importplan.Undecided
			a.ifAdopted = d.Outcome(a.Ref, a.Compared)
			a.decidedIfAdopted = d.Overwrite[a.Ref] || d.Keep[a.Ref] || importplan.Asks(a.Compared) && a.ifAdopted != importplan.Undecided
			if d.Adopt[a.Ref] {
				a.adopt()
			}
			continue
		}
		a.Outcome = d.Outcome(a.Ref, a.Status)
		a.decided = d.Overwrite[a.Ref] || d.Keep[a.Ref] || importplan.Asks(a.Status) && a.Outcome != importplan.Undecided
	}
}

// adopt takes an Elsewhere artifact: it moves into the importing application, and
// its content compares as the server reported.
func (p *planned) adopt() {
	p.Adopted, p.Outcome, p.decided = true, p.ifAdopted, p.decidedIfAdopted
}

// show writes the plan: how many artifacts are new, unchanged, behind, ahead and
// changed, and each that is not new or unchanged with its outcome; with all, each
// new one too.
func (p importPlan) show(out io.Writer, all bool) {
	count := map[string]int{}
	for _, a := range p.artifacts {
		count[a.Status]++
	}
	fmt.Fprintf(out, "plan: %d new, %d unchanged, %d behind, %d ahead, %d changed", count[importplan.New],
		count[importplan.Unchanged], count[importplan.Behind], count[importplan.Ahead], count[importplan.Changed])
	if n := count[importplan.Elsewhere]; n > 0 {
		fmt.Fprintf(out, ", %d held by another application", n)
	}
	if n := count[importplan.Gap]; n > 0 {
		fmt.Fprintf(out, ", %d missing from the capability map", n)
	}
	fmt.Fprintln(out)
	for _, a := range p.artifacts {
		switch a.Status {
		case importplan.Gap:
			line := fmt.Sprintf("  %-9s %s — ", a.Status, a.label())
			switch a.Outcome {
			case importplan.Created:
				line += "the package's proposal is filed in the map"
			case importplan.Mapped:
				line += "filed under " + a.MappedTo
			case importplan.Omitted:
				line += "left out"
			default:
				if a.Proposed {
					line += "--adopt " + a.Ref + " files the package's proposal, "
				}
				line += "--map " + a.Ref + "=KEY uses a key the map has, --omit " + a.Ref + " leaves it out"
			}
			fmt.Fprintln(out, line)
			continue
		case importplan.Linked:
			if a.Compared == importplan.Changed {
				fmt.Fprintf(out, "  %-9s %s — the map's record differs from the package's proposal and stays as it is\n", a.Status, a.label())
			}
			continue
		}
		if a.Status == importplan.Elsewhere {
			line := fmt.Sprintf("  %-9s %s, held by %s — ", a.Status, a.label(), a.holder())
			switch {
			case !a.Adopted:
				line += "not taken; --adopt " + a.Ref + " moves it into this application"
			case a.Compared == importplan.Changed:
				line += "adopted, content differs — " + a.Outcome
			default:
				line += "adopted, content unchanged"
			}
			fmt.Fprintln(out, line)
			continue
		}
		moved := a.Status == importplan.Changed || a.Status == importplan.Behind || a.Status == importplan.Ahead
		if moved || all && a.Status == importplan.New {
			line := fmt.Sprintf("  %-9s %s", a.Status, a.label())
			if a.detail != "" {
				line += ": " + a.detail
			}
			if moved {
				line += " — " + a.Outcome
			}
			fmt.Fprintln(out, line)
		}
	}
	if len(p.needs) > 0 {
		fmt.Fprintln(out, "this server lacks what the application needs; publishing warns about it, and an instance runs without it:")
		for _, n := range p.needs {
			fmt.Fprintf(out, "  %s, %s: %s\n", n.what(), n.how(), n.where())
		}
	}
	if len(p.source.NeedsUnread) > 0 {
		fmt.Fprintf(out, "  not read for what they need, because they do not compile: %s\n", strings.Join(p.source.NeedsUnread, ", "))
	}
}

// ask settles what the flags left undecided: at a terminal, one question per
// artifact, whose default is to keep the server's version; in a pipeline, a
// refusal that names every one and the flags that decide them.
func (p *importPlan) ask(out io.Writer) error {
	var held, open, gaps []int
	for i, a := range p.artifacts {
		switch {
		case a.held():
			held = append(held, i)
		case a.Status == importplan.Gap && a.Outcome == importplan.Undecided:
			gaps = append(gaps, i)
		case a.Outcome == importplan.Undecided:
			open = append(open, i)
		}
	}
	if len(held) == 0 && len(open) == 0 && len(gaps) == 0 {
		return nil
	}
	in, interactive := decisionPrompt()
	var sc *bufio.Scanner
	if interactive {
		sc = bufio.NewScanner(in)
	}
	if len(gaps) > 0 {
		if err := p.askGaps(out, gaps, sc); err != nil {
			return err
		}
		if len(held) == 0 && len(open) == 0 {
			return nil
		}
	}
	if !interactive && len(held) > 0 {
		lines := make([]string, 0, len(held))
		for _, i := range held {
			lines = append(lines, p.artifacts[i].label()+", held by "+p.artifacts[i].holder())
		}
		return fmt.Errorf("%d artifacts belong to another application and were not taken; nothing was imported:\n  %s\n"+
			"take each with --adopt REF, which needs the editor role on the application it leaves",
			len(held), strings.Join(lines, "\n  "))
	}
	// An artifact another application holds is asked about first: declined, the
	// import cannot go on, and nothing is asked about its content.
	for _, i := range held {
		a := &p.artifacts[i]
		fmt.Fprintf(out, "%s belongs to %s. Move it into this application? [y/N] ", a.label(), a.holder())
		if !sc.Scan() {
			fmt.Fprintln(out)
			return errors.New("no answer was given; nothing was imported")
		}
		switch strings.ToLower(strings.TrimSpace(sc.Text())) {
		case "y", "yes", "j", "ja":
		default:
			return fmt.Errorf("%s stays in %s, and the package cannot be imported without it; nothing was imported", a.label(), a.holder())
		}
		a.adopt()
		if a.Outcome == importplan.Undecided {
			open = append(open, i)
		}
	}
	if len(open) == 0 {
		return nil
	}
	if !interactive {
		lines := make([]string, 0, len(open))
		for _, i := range open {
			line := p.artifacts[i].label()
			if d := p.artifacts[i].detail; d != "" {
				line += ": " + d
			}
			lines = append(lines, line)
		}
		return fmt.Errorf("%d artifacts differ from the server's and were not decided; nothing was imported:\n  %s\n"+
			"decide each with --overwrite REF or --keep REF, or all of them with --overwrite all or --keep all",
			len(open), strings.Join(lines, "\n  "))
	}
	for _, i := range open {
		a := &p.artifacts[i]
		question := a.label() + " differs from the server's"
		if a.detail != "" {
			question += " (" + a.detail + ")"
		}
		fmt.Fprintf(out, "%s. Replace it with the package's? [y/N] ", question)
		if !sc.Scan() {
			fmt.Fprintln(out)
			return errors.New("no answer was given; nothing was imported")
		}
		a.decided = true
		switch strings.ToLower(strings.TrimSpace(sc.Text())) {
		case "y", "yes", "j", "ja":
			a.Outcome = importplan.Updated
		default:
			a.Outcome = importplan.Kept
		}
	}
	return nil
}

// query is the decisions as the server takes them: every artifact the operator
// decided, named under overwrite or keep. They are named one by one rather than as
// *, and what follows from behind or ahead is not named at all, so an artifact that
// changed after the plan was shown is refused rather than replaced on the strength
// of a decision about something else.
func (p importPlan) query() string {
	var overwrite, keep, adopt, mapped, omit []string
	for _, a := range p.artifacts {
		if a.Adopted {
			adopt = append(adopt, a.Ref)
		}
		if !a.decided {
			continue
		}
		if a.Status == importplan.Gap {
			switch a.Outcome {
			case importplan.Created:
				adopt = append(adopt, a.Ref)
			case importplan.Mapped:
				mapped = append(mapped, a.Ref+"="+a.MappedTo)
			case importplan.Omitted:
				omit = append(omit, a.Ref)
			}
			continue
		}
		switch a.Outcome {
		case importplan.Updated:
			overwrite = append(overwrite, a.Ref)
		case importplan.Kept:
			keep = append(keep, a.Ref)
		}
	}
	q := url.Values{}
	if len(overwrite) > 0 {
		q.Set("overwrite", strings.Join(overwrite, ","))
	}
	if len(keep) > 0 {
		q.Set("keep", strings.Join(keep, ","))
	}
	if len(adopt) > 0 {
		q.Set("adopt", strings.Join(adopt, ","))
	}
	if len(mapped) > 0 {
		q.Set("map", strings.Join(mapped, ","))
	}
	if len(omit) > 0 {
		q.Set("omit", strings.Join(omit, ","))
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}

// sourceChanged reports whether the source import wrote anything, so the
// application has something to publish. A constant the tree carried is no reason:
// an instance reads it where it starts, and a release deploys nothing new for it.
func sourceChanged(src sourceImportBody) bool {
	if src.Created {
		return true
	}
	for _, a := range src.Artifacts {
		if importplan.Writes(a.Outcome) && !strings.HasPrefix(a.Ref, "constant:") {
			return true
		}
	}
	return false
}

// kept names the artifacts the import left as the server has them.
func kept(artifacts []importplan.Artifact) []string {
	var out []string
	for _, a := range artifacts {
		if a.Outcome == importplan.Kept {
			out = append(out, a.Ref)
		}
	}
	return out
}

// --- constants on the server ---------------------------------------------------

type constantsBody struct {
	Constants []packageConstant `json:"constants"`
	Required  []struct {
		Name  string `json:"name"`
		Type  string `json:"type"`
		State string `json:"state"`
	} `json:"required"`
}

// constants reads what the application holds here and what its deployed processes
// require of it.
func (c *apiClient) constants(appID string) (constantsBody, error) {
	var body constantsBody
	err := c.do(http.MethodGet, "/api/v1/applications/"+url.PathEscape(appID)+"/constants", nil, &body)
	return body, err
}

// constantsImportBody is what POST /api/v1/applications/{id}/constants/import
// answers.
type constantsImportBody struct {
	Artifacts []importplan.Artifact `json:"artifacts"`
	Set       []string              `json:"set"`
	NotSet    []string              `json:"notSet"`
}

// importConstants sends the package's constants with a query: ?dryRun=true for the
// plan, or the decisions.
func (c *importClient) importConstants(appID string, consts []packageConstant, query string) (constantsImportBody, error) {
	var res constantsImportBody
	body, err := encodeJSON(constantsFile{Constants: consts})
	if err != nil {
		return res, err
	}
	err = c.do(http.MethodPost, "/api/v1/applications/"+url.PathEscape(appID)+"/constants/import"+query, body, &res)
	return res, err
}

// installConstants imports the package's constants with the decisions the plan
// settled. Setting a value is an administrator's, and a token never carries that
// role: what the server did not set for that reason is not a failure of the import
// but a list of what an administrator still has to set.
func (c *importClient) installConstants(out io.Writer, appID string, consts []packageConstant, decided string) error {
	if len(consts) == 0 {
		return nil
	}
	res, err := c.importConstants(appID, consts, decided)
	if err != nil {
		return err
	}
	byName := map[string]packageConstant{}
	for _, k := range consts {
		byName[k.Name] = k
	}
	describe := func(names []string) string {
		parts := make([]string, 0, len(names))
		for _, n := range names {
			k := byName[n]
			parts = append(parts, fmt.Sprintf("%s (%s) = %q", k.Name, k.Type, k.Value))
		}
		return strings.Join(parts, ", ")
	}
	if len(res.Set) > 0 {
		fmt.Fprintf(out, "constants set: %s\n", describe(res.Set))
	}
	if len(res.NotSet) > 0 {
		fmt.Fprintf(out, "  warning: constants not set — setting them is an administrator's, and a token never carries that role; set them in the Console under the application's constants: %s\n",
			describe(res.NotSet))
	}
	if k := kept(res.Artifacts); len(k) > 0 {
		fmt.Fprintf(out, "  constants kept as the server holds them: %s\n", strings.Join(k, ", "))
	}
	return nil
}

// reportConstants names what the published application reads and this server does
// not hold, or holds with another type than a process declares.
func (c *importClient) reportConstants(out io.Writer, appID string) error {
	held, err := c.constants(appID)
	if err != nil {
		return err
	}
	for _, r := range held.Required {
		switch r.State {
		case "missing":
			fmt.Fprintf(out, "  warning: the application reads constant %s (%s), and this server holds no value for it\n", r.Name, r.Type)
		case "type-mismatch":
			fmt.Fprintf(out, "  warning: the application reads constant %s as %s, and this server holds it with another type\n", r.Name, r.Type)
		}
	}
	return nil
}

// latestRelease is the newest release's version, 0 for an application never
// published.
func (c *importClient) latestRelease(appID string) (int, error) {
	var releases []struct {
		Version int `json:"version"`
	}
	if err := c.do(http.MethodGet, "/api/v1/applications/"+url.PathEscape(appID)+"/releases", nil, &releases); err != nil {
		return 0, err
	}
	latest := 0
	for _, r := range releases {
		latest = max(latest, r.Version)
	}
	return latest, nil
}

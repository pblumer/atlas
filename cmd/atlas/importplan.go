package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/importplan"
)

// The plan `atlas import` shows before its first write
// (ADR-draft-application-package-export). The server compares what the package
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
const packageConstants = "konstanten.json"

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

// decisions is what the flags decided, in the server's terms.
func decisions(overwrite, keep *refFlag) (importplan.Decisions, error) {
	d := importplan.Decisions{Overwrite: overwrite.refs, Keep: keep.refs, OverwriteAll: overwrite.all, KeepAll: keep.all}
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
}

func (p planned) label() string {
	if p.Name != "" && p.Name != strings.SplitN(p.Ref, ":", 2)[1] {
		return fmt.Sprintf("%s %q", p.Ref, p.Name)
	}
	return p.Ref
}

// importPlan is everything the package would do to the server.
type importPlan struct {
	source    sourceImportBody
	catalogue catalogueImportBody
	artifacts []planned
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

// planConstants compares the package's constants with those the application holds
// here. An application the import creates holds none. A constant the server holds
// and the package does not name is not touched, and not listed.
func (c *importClient) planConstants(src sourceImportBody, consts []packageConstant) ([]planned, error) {
	held := map[string]packageConstant{}
	if src.ApplicationID != "" && !src.Created {
		list, err := c.constants(src.ApplicationID)
		if err != nil {
			return nil, err
		}
		for _, h := range list.Constants {
			held[h.Name] = h
		}
	}
	var out []planned
	for _, k := range consts {
		a := planned{Artifact: importplan.Artifact{Ref: "constant:" + k.Name, Status: importplan.New}}
		if h, ok := held[k.Name]; ok {
			a.Status = importplan.Compare(h.Type == k.Type && h.Value == k.Value)
			if a.Status == importplan.Changed {
				a.detail = fmt.Sprintf("here %s %q, in the package %s %q", h.Type, h.Value, k.Type, k.Value)
			}
		}
		out = append(out, a)
	}
	return out, nil
}

// decide settles each artifact's outcome as the flags decide it.
func (p *importPlan) decide(d importplan.Decisions) {
	for i := range p.artifacts {
		p.artifacts[i].Outcome = d.Outcome(p.artifacts[i].Ref, p.artifacts[i].Status)
	}
}

// show writes the plan: how many artifacts are new, unchanged and changed, and each
// changed one with its outcome; with all, each new one too.
func (p importPlan) show(out io.Writer, all bool) {
	count := map[string]int{}
	for _, a := range p.artifacts {
		count[a.Status]++
	}
	fmt.Fprintf(out, "plan: %d new, %d unchanged, %d changed\n", count[importplan.New], count[importplan.Unchanged], count[importplan.Changed])
	for _, a := range p.artifacts {
		if a.Status == importplan.Changed || all && a.Status == importplan.New {
			line := fmt.Sprintf("  %-9s %s", a.Status, a.label())
			if a.detail != "" {
				line += ": " + a.detail
			}
			if a.Status == importplan.Changed {
				line += " — " + a.Outcome
			}
			fmt.Fprintln(out, line)
		}
	}
}

// ask settles what the flags left undecided: at a terminal, one question per
// artifact, whose default is to keep the server's version; in a pipeline, a
// refusal that names every one and the flags that decide them.
func (p *importPlan) ask(out io.Writer) error {
	var open []int
	for i, a := range p.artifacts {
		if a.Outcome == importplan.Undecided {
			open = append(open, i)
		}
	}
	if len(open) == 0 {
		return nil
	}
	in, interactive := decisionPrompt()
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
	sc := bufio.NewScanner(in)
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
		switch strings.ToLower(strings.TrimSpace(sc.Text())) {
		case "y", "yes", "j", "ja":
			a.Outcome = importplan.Updated
		default:
			a.Outcome = importplan.Kept
		}
	}
	return nil
}

// query is the decisions as the server takes them: every changed artifact named,
// under overwrite or keep. They are named one by one rather than as *, so an
// artifact that changed after the plan was shown is refused rather than replaced on
// the strength of a decision about something else.
func (p importPlan) query() string {
	var overwrite, keep []string
	for _, a := range p.artifacts {
		if a.Status != importplan.Changed {
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
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}

// constantsToSet are the package's constants the plan writes: those the server does
// not hold, and those it holds otherwise that the operator chose to replace.
func (p importPlan) constantsToSet(consts []packageConstant) []packageConstant {
	writes := map[string]bool{}
	for _, a := range p.artifacts {
		if strings.HasPrefix(a.Ref, "constant:") && importplan.Writes(a.Outcome) {
			writes[strings.TrimPrefix(a.Ref, "constant:")] = true
		}
	}
	var out []packageConstant
	for _, k := range consts {
		if writes[k.Name] {
			out = append(out, k)
		}
	}
	return out
}

// sourceChanged reports whether the source import wrote anything, so the
// application has something to publish.
func sourceChanged(src sourceImportBody) bool {
	if src.Created {
		return true
	}
	for _, a := range src.Artifacts {
		if importplan.Writes(a.Outcome) {
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

// setConstants writes the constants into what the application holds, leaving every
// other constant as it is: the route replaces the whole list, so the list is read
// and merged first. Setting a constant is an administrator's, and a token never
// carries that role; a refusal for want of it is not a failure of the import, but
// a list of what an administrator still has to set.
func (c *importClient) setConstants(out io.Writer, appID string, set []packageConstant) error {
	if len(set) == 0 {
		return nil
	}
	held, err := c.constants(appID)
	if err != nil {
		return err
	}
	merged := held.Constants
	for _, k := range set {
		replaced := false
		for i := range merged {
			if merged[i].Name == k.Name {
				merged[i], replaced = k, true
			}
		}
		if !replaced {
			merged = append(merged, k)
		}
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Name < merged[j].Name })
	body, err := encodeJSON(constantsFile{Constants: merged})
	if err != nil {
		return err
	}
	names := make([]string, 0, len(set))
	for _, k := range set {
		names = append(names, fmt.Sprintf("%s (%s) = %q", k.Name, k.Type, k.Value))
	}
	err = c.do(http.MethodPut, "/api/v1/applications/"+url.PathEscape(appID)+"/constants", body, nil)
	var refused *statusError
	if errors.As(err, &refused) && (refused.status == http.StatusForbidden || refused.status == http.StatusUnauthorized) {
		fmt.Fprintf(out, "  warning: constants not set — setting them is an administrator's, and a token never carries that role; set them in the Console under the application's constants: %s\n",
			strings.Join(names, ", "))
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "constants set: %s\n", strings.Join(names, ", "))
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

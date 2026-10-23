package authz

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/pblumer/atlas/dmn"
	"github.com/pblumer/atlas/expr"
)

// Rule-combining algorithms a policy may declare. There is no default: an algorithm
// left out is refused, because the two answer the same rules differently.
const (
	FirstApplicable = "first-applicable"
	DenyOverrides   = "deny-overrides"
)

// FEEL condition inputs: the only names a condition may read.
var conditionInputs = []string{"subject", "actor", "action", "resource", "context"}

var (
	policyIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	reasonPattern   = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
)

// Bounds on what an approval obligation may ask for.
const (
	maxApprovers     = 8
	minApprovalValid = 60
	maxApprovalValid = 30 * 24 * 3600
)

// Published is one policy version as the store holds it: immutable once published.
type Published struct {
	Document    Document
	Version     int
	PublishedAt int64
	PublishedBy string
}

// Policy is one published policy compiled for evaluation. It is immutable and safe
// for concurrent use.
type Policy struct {
	id        string
	version   int
	priority  int
	target    compiledTarget
	combining string
	rules     []rule
}

// ID and Version name the policy as it was published.
func (p *Policy) ID() string   { return p.id }
func (p *Policy) Version() int { return p.version }

type compiledTarget struct {
	workerTypes, workers, operations, modes set
}

type rule struct {
	id          string
	effect      Effect // Permit or Deny; Indeterminate when decision is set
	reason      string
	match       compiledMatch
	condition   *expr.Compiled
	decision    *dmn.Standalone
	obligations []obligation
}

type compiledMatch struct {
	roles, groups, subjects, operations, workers, modes, processes set
	prefixes                                                       []string
}

type obligation struct {
	id, kind         string
	approverGroup    string
	approvals        int
	validFor         int64 // nanoseconds
	excludeRequester bool
	maxAge           int64 // nanoseconds
}

// set is a membership test; nil means "not stated", which matches everything.
type set map[string]struct{}

func setOf(items []string) set {
	if len(items) == 0 {
		return nil
	}
	s := make(set, len(items))
	for _, it := range items {
		s[it] = struct{}{}
	}
	return s
}

func (s set) has(v string) bool {
	if s == nil {
		return true
	}
	_, ok := s[v]
	return ok
}

func (s set) hasAny(vs []string) bool {
	if s == nil {
		return true
	}
	for _, v := range vs {
		if _, ok := s[v]; ok {
			return true
		}
	}
	return false
}

// CompilePolicy validates and compiles one policy. Everything that can be checked
// without a request is checked here — the structure, every FEEL condition, the DMN
// model — so nothing is parsed or compiled when a decision is asked for (invariant
// I5). It returns warnings for what is legal but unsafe to rely on.
func CompilePolicy(ctx context.Context, doc Document, version int) (*Policy, []string, error) {
	var warnings []string
	fail := func(format string, args ...any) (*Policy, []string, error) {
		return nil, nil, fmt.Errorf("policy %q: %s", doc.ID, fmt.Sprintf(format, args...))
	}
	if !policyIDPattern.MatchString(doc.ID) {
		return fail("the id must be 1–64 lowercase letters, digits, '.', '_' or '-', starting with a letter or digit")
	}
	if version < 1 {
		return fail("version %d: versions start at 1", version)
	}
	if doc.RuleCombining != FirstApplicable && doc.RuleCombining != DenyOverrides {
		return fail("ruleCombining must be %q or %q; it has no default because the two decide the same rules differently", FirstApplicable, DenyOverrides)
	}
	if len(doc.Rules) == 0 {
		return fail("it has no rules")
	}
	for _, m := range doc.Target.IdentityModes {
		if !knownMode(m) {
			return fail("target names identity mode %q, which does not exist", m)
		}
	}
	var model *dmnModel
	if doc.DMN != "" {
		if err := refuseClockInDMN(doc.DMN); err != nil {
			return fail("%v", err)
		}
		model = &dmnModel{src: []byte(doc.DMN), compiled: map[string]*dmn.Standalone{}}
	}
	p := &Policy{
		id: doc.ID, version: version, priority: doc.Priority, combining: doc.RuleCombining,
		target: compiledTarget{
			workerTypes: setOf(doc.Target.WorkerTypes), workers: setOf(doc.Target.Workers),
			operations: setOf(doc.Target.Operations), modes: setOf(doc.Target.IdentityModes),
		},
	}
	seen := map[string]bool{}
	for _, rd := range doc.Rules {
		if !policyIDPattern.MatchString(rd.ID) {
			return fail("rule id %q must be 1–64 lowercase letters, digits, '.', '_' or '-'", rd.ID)
		}
		if seen[rd.ID] {
			return fail("rule %q is declared twice", rd.ID)
		}
		seen[rd.ID] = true
		r := rule{id: rd.ID, reason: rd.ReasonCode}
		if rd.ReasonCode != "" && !reasonPattern.MatchString(rd.ReasonCode) {
			return fail("rule %q: reasonCode %q must be UPPER_SNAKE_CASE", rd.ID, rd.ReasonCode)
		}
		switch {
		case (rd.Effect == "") == (rd.Decision == ""):
			return fail("rule %q must state either an effect or a decision", rd.ID)
		case rd.Decision != "":
			if model == nil {
				return fail("rule %q names decision %q but the policy has no dmn model", rd.ID, rd.Decision)
			}
			d, err := model.decision(ctx, rd.Decision)
			if err != nil {
				return fail("rule %q: %v", rd.ID, err)
			}
			r.decision = d
		default:
			e, ok := ParseEffect(rd.Effect)
			if !ok || e == NotApplicable {
				return fail("rule %q: effect %q must be \"Permit\" or \"Deny\"", rd.ID, rd.Effect)
			}
			r.effect = e
		}
		for _, m := range rd.When.IdentityModes {
			if !knownMode(m) {
				return fail("rule %q names identity mode %q, which does not exist", rd.ID, m)
			}
		}
		r.match = compiledMatch{
			roles: setOf(rd.When.SubjectRolesAny), groups: setOf(rd.When.SubjectGroupsAny),
			subjects: setOf(rd.When.SubjectIDs), operations: setOf(rd.When.Operations),
			workers: setOf(rd.When.Workers), modes: setOf(rd.When.IdentityModes),
			processes: setOf(rd.When.ProcessIDs), prefixes: append([]string(nil), rd.When.ResourcePrefixes...),
		}
		for _, pfx := range rd.When.ResourcePrefixes {
			if endsInsideValue(pfx) {
				warnings = append(warnings, fmt.Sprintf(
					"rule %q: resource prefix %q ends inside a value, so it also matches every value that merely "+
						"begins with it; end it at a boundary (';' after a whole segment, or '/' in a path)", rd.ID, pfx))
			}
		}
		if r.effect == Deny && len(rd.When.ResourcePrefixes) > 0 {
			warnings = append(warnings, fmt.Sprintf(
				"rule %q denies by resource name; a provider that spells one resource several ways "+
					"(a SharePoint site by URL, host path or id) can be reached through a spelling this rule does not name. "+
					"Write resource rules as a permit-list and let everything else fall to the default deny", rd.ID))
		}
		if strings.TrimSpace(rd.Condition) != "" {
			if err := refuseClock(rd.Condition); err != nil {
				return fail("rule %q: condition: %v", rd.ID, err)
			}
			c, err := expr.Compile(rd.Condition, conditionInputs...)
			if err != nil {
				return fail("rule %q: condition may read only %s: %v", rd.ID, strings.Join(conditionInputs, ", "), err)
			}
			if err := expr.CheckCallsError(rd.Condition, conditionInputs...); err != nil {
				return fail("rule %q: condition: %v", rd.ID, err)
			}
			r.condition = c
		}
		if len(rd.Obligations) > 0 && r.effect != Permit {
			return fail("rule %q: obligations belong to a Permit — a refusal has nothing left to fulfil", rd.ID)
		}
		obIDs := map[string]bool{}
		for _, od := range rd.Obligations {
			ob, err := compileObligation(od)
			if err != nil {
				return fail("rule %q: %v", rd.ID, err)
			}
			if obIDs[ob.id] {
				return fail("rule %q: obligation %q is declared twice", rd.ID, ob.id)
			}
			obIDs[ob.id] = true
			r.obligations = append(r.obligations, ob)
		}
		p.rules = append(p.rules, r)
	}
	return p, warnings, nil
}

func compileObligation(od ObligationDoc) (obligation, error) {
	if !policyIDPattern.MatchString(od.ID) {
		return obligation{}, fmt.Errorf("obligation id %q must be 1–64 lowercase letters, digits, '.', '_' or '-'", od.ID)
	}
	ob := obligation{id: od.ID, kind: od.Type}
	switch od.Type {
	case ObligationApproval:
		if strings.TrimSpace(od.ApproverGroup) == "" {
			return obligation{}, fmt.Errorf("obligation %q: an approval needs an approverGroup", od.ID)
		}
		if od.Approvals < 1 || od.Approvals > maxApprovers {
			return obligation{}, fmt.Errorf("obligation %q: approvals must be 1–%d", od.ID, maxApprovers)
		}
		if od.ValidForSeconds < minApprovalValid || od.ValidForSeconds > maxApprovalValid {
			return obligation{}, fmt.Errorf("obligation %q: validForSeconds must be %d–%d", od.ID, minApprovalValid, maxApprovalValid)
		}
		ob.approverGroup, ob.approvals = od.ApproverGroup, od.Approvals
		ob.validFor, ob.excludeRequester = od.ValidForSeconds*1e9, od.ExcludeRequester
	case ObligationFreshAuthentication:
		if od.MaxAgeSeconds <= 0 {
			return obligation{}, fmt.Errorf("obligation %q: a fresh authentication needs maxAgeSeconds", od.ID)
		}
		ob.maxAge = od.MaxAgeSeconds * 1e9
	default:
		return obligation{}, fmt.Errorf("obligation %q: unknown type %q (want %q or %q)", od.ID, od.Type, ObligationApproval, ObligationFreshAuthentication)
	}
	return ob, nil
}

// dmnModel compiles a policy's DMN model once per decision a rule names.
type dmnModel struct {
	src      []byte
	compiled map[string]*dmn.Standalone
}

func (m *dmnModel) decision(ctx context.Context, id string) (*dmn.Standalone, error) {
	if d, ok := m.compiled[id]; ok {
		return d, nil
	}
	d, err := dmn.CompileStandalone(ctx, m.src, id)
	if err != nil {
		return nil, err
	}
	m.compiled[id] = d
	return d, nil
}

// Set is the compiled, published policy set. It is immutable: publishing builds a new
// one, and a reader holding the old one keeps deciding with it.
type Set struct {
	policies []*Policy
}

// Policies lists the set's policies in evaluation order.
func (s *Set) Policies() []*Policy {
	if s == nil {
		return nil
	}
	return s.policies
}

// CompileSet compiles every published policy into one set, ordered by priority and
// then id so the order of the input never changes an answer.
func CompileSet(ctx context.Context, published []Published) (*Set, error) {
	s := &Set{}
	ids := map[string]bool{}
	for _, pub := range published {
		if ids[pub.Document.ID] {
			return nil, fmt.Errorf("policy %q is in the set twice; a set holds one version of each policy", pub.Document.ID)
		}
		ids[pub.Document.ID] = true
		p, _, err := CompilePolicy(ctx, pub.Document, pub.Version)
		if err != nil {
			return nil, err
		}
		s.policies = append(s.policies, p)
	}
	sort.SliceStable(s.policies, func(i, j int) bool {
		if s.policies[i].priority != s.policies[j].priority {
			return s.policies[i].priority < s.policies[j].priority
		}
		return s.policies[i].id < s.policies[j].id
	})
	return s, nil
}

var errClock = errors.New("it reads the clock (now() or today()); a policy must be a function of its request — compare against context.time")

// refuseClock reports a call of now() or today() outside a string literal. FEEL reads
// the wall clock for both, which would make a decision depend on when it was asked
// rather than on the instant the request carries.
func refuseClock(src string) error {
	inString := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if inString {
			switch c {
			case '\\':
				i++
			case '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			continue
		}
		if !isIdentStart(c) || (i > 0 && isIdentPart(src[i-1])) {
			continue
		}
		j := i
		for j < len(src) && isIdentPart(src[j]) {
			j++
		}
		word := src[i:j]
		k := j
		for k < len(src) && (src[k] == ' ' || src[k] == '\t' || src[k] == '\n' || src[k] == '\r') {
			k++
		}
		if (word == "now" || word == "today") && k < len(src) && src[k] == '(' {
			return errClock
		}
		i = j - 1
	}
	return nil
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') }

// refuseClockInDMN applies refuseClock to every FEEL text in a DMN model: the
// expressions of a DMN document are the contents of its <text> elements.
func refuseClockInDMN(src string) error {
	dec := xml.NewDecoder(bytes.NewReader([]byte(src)))
	var inText bool
	var buf strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("dmn: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "text" {
				inText = true
				buf.Reset()
			}
		case xml.CharData:
			if inText {
				buf.Write(t)
			}
		case xml.EndElement:
			if t.Name.Local == "text" && inText {
				inText = false
				if err := refuseClock(buf.String()); err != nil {
					return fmt.Errorf("dmn: %w", err)
				}
			}
		}
	}
}

// endsInsideValue reports whether a resource prefix stops in the middle of a segment's
// value: it names a value (it has a '=') and does not end at a separator.
func endsInsideValue(prefix string) bool {
	if !strings.Contains(prefix, "=") {
		return false
	}
	last := prefix[len(prefix)-1]
	return last != ';' && last != '/'
}

package api

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// Who already holds a combination the catalogue forbids
// (ADR-0342).
//
// # Why this looks at the current catalogue and not at each grant's release
//
// The deliberate opposite of the expiry ceiling, which never reaches a right
// granted before it was declared. The two look similar and are not. An expiry is
// *part of what was granted* — it belongs to that grant, and applying one
// retroactively would invent a date nobody agreed. A conflict is a statement about
// what may **coexist now**, and a rule that did not apply to the estate the day it
// was written would be decoration.
//
// So declaring a rule surfaces its violations the same day, which is the only
// behaviour that makes declaring one worth doing.
//
// # And why nothing here acts
//
// Every other mechanism in this line of work acts on one (principal, item) pair,
// and a conflict is a pair of pairs. Asked which half is wrong, the honest answer
// is neither, and both together — the person needs one of them to do their job,
// and which one is a question about the job. An automatic remedy would have to
// choose, and choosing wrongly takes the needed right away and leaves the other.

// conflictFinding is one person holding one forbidden pair.
type conflictFinding struct {
	Principal string `json:"principal"`
	// A and B are the two items, sorted, so the same pair reads the same way
	// however the inventory walk happened to meet it. Neither is "the problem" and
	// the order says nothing about blame.
	A string `json:"a"`
	B string `json:"b"`
	// SinceA and SinceB are when each was granted. They are the only thing in the
	// finding that hints at a remedy — the newer one is usually the one somebody
	// meant to add — and it is a hint rather than an answer, which is why both are
	// here rather than a computed "the newer one".
	SinceA int64 `json:"sinceA,omitempty"`
	SinceB int64 `json:"sinceB,omitempty"`
}

// conflictReport is what one read answers.
type conflictReport struct {
	// Pairs is how many forbidden combinations the catalogue declares at all. It is
	// the denominator: "nobody holds a conflict" means something different when the
	// answer is none out of none.
	Pairs    int               `json:"pairs"`
	Counts   conflictCounts    `json:"counts"`
	Findings []conflictFinding `json:"findings"`
	Omitted  int               `json:"omitted,omitempty"`
	// Note explains an empty answer, which otherwise reads as a clean estate when it
	// usually means no catalogue declares an incompatibility.
	Note string `json:"note,omitempty"`
}

type conflictCounts struct {
	// Findings is pairs held, People how many people hold at least one. Both,
	// because one person with four conflicts and four people with one each are
	// very different mornings and the same number of findings.
	Findings int `json:"findings"`
	People   int `json:"people"`
}

// handleConflicts answers who holds a combination the catalogue forbids.
func (s *Server) handleConflicts(w http.ResponseWriter, r *http.Request) {
	var (
		excludes map[string][]string
		pairs    int
		ran      bool
		loadErr  error
	)
	s.do(func() {
		ran = true
		excludes, pairs, loadErr = currentExclusions(s)
	})
	if !ran {
		httpapi.Error(w, http.StatusServiceUnavailable, "conflicts: this server is shutting down")
		return
	}
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "conflicts: "+loadErr.Error())
		return
	}

	rep := conflictReport{Pairs: pairs, Findings: []conflictFinding{}}
	if len(excludes) == 0 {
		rep.Note = "no catalogue release declares an incompatibility, so nothing here can be " +
			"in conflict. A catalogue declares one with an edge of kind \"excludes\", and it " +
			"takes effect for everybody the day it is published — unlike a product's maximum " +
			"duration, which only ever reaches rights granted after it"
		httpapi.JSON(w, http.StatusOK, rep)
		return
	}

	// The inventory, whole, off the loop — the walk ADR-0334 added. "Who holds a
	// forbidden pair" is not answerable from the entitlement key: the principal
	// comes first, so one person's holdings are a scan, but the question is about
	// everybody.
	held := map[string][]model.EntitlementValue{}
	if err := s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		return rv.Entitlements(func(v *model.EntitlementValue) error {
			held[v.Principal] = append(held[v.Principal], *v)
			return nil
		})
	}); err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "conflicts: read the inventory: "+err.Error())
		return
	}

	for principal, rights := range held {
		rep.Findings = append(rep.Findings, conflictsFor(principal, rights, excludes)...)
	}
	sortConflicts(rep.Findings)

	people := map[string]bool{}
	for _, f := range rep.Findings {
		people[f.Principal] = true
	}
	rep.Counts = conflictCounts{Findings: len(rep.Findings), People: len(people)}

	if limit := int(s.budgets().ConflictReport); len(rep.Findings) > limit {
		rep.Omitted = len(rep.Findings) - limit
		rep.Findings = rep.Findings[:limit]
	}
	if len(rep.Findings) == 0 {
		rep.Note = "nobody holds a forbidden combination, against " + strconv.Itoa(pairs) +
			" " + plural(pairs, "declared incompatible pair", "declared incompatible pairs") +
			". This is what a healthy answer looks like"
	}
	httpapi.JSON(w, http.StatusOK, rep)
}

// currentExclusions is what the catalogue forbids **now**, merged across every
// catalogue's newest release.
//
// Merged rather than per-catalogue, because a person holds rights and not
// catalogues: two products offered by different catalogues are still two products
// one person can end up with, and a check confined to one catalogue would miss
// exactly the combination nobody thought to look for.
//
// It must run on the loop — it reads the catalogue store.
func currentExclusions(s *Server) (map[string][]string, int, error) {
	cats, err := s.catalogStore.Catalogs()
	if err != nil {
		return nil, 0, err
	}
	merged := map[string]map[string]bool{}
	for _, c := range cats {
		rels, err := s.catalogStore.ReleasesOf(c.ID)
		if err != nil {
			return nil, 0, err
		}
		if len(rels) == 0 {
			continue
		}
		// The newest, and only the newest. The store orders releases newest first,
		// and an older release's rules are what the catalogue used to say — reading
		// them too would enforce a rule somebody has since removed.
		for id, others := range rels[0].Excludes {
			if merged[id] == nil {
				merged[id] = map[string]bool{}
			}
			for _, other := range others {
				merged[id][other] = true
			}
		}
	}
	if len(merged) == 0 {
		return nil, 0, nil
	}
	out := make(map[string][]string, len(merged))
	seen := map[string]bool{}
	for id, others := range merged {
		list := make([]string, 0, len(others))
		for other := range others {
			list = append(list, other)
			// Counted once per pair, not once per direction: the release stores both
			// ways round on purpose, and a count that followed the storage would
			// report twice as many rules as anybody wrote.
			seen[pairKey(id, other)] = true
		}
		sort.Strings(list)
		out[id] = list
	}
	return out, len(seen), nil
}

// pairKey names a pair the same way from either side.
func pairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "\x00" + b
}

// conflictsFor finds the forbidden pairs one person holds.
//
// Each pair once. The exclusion map holds both directions — which is what makes a
// one-sided check impossible — so a naive walk meets every pair twice, and a report
// that listed a conflict under both its names would double every number an operator
// reads.
func conflictsFor(principal string, rights []model.EntitlementValue,
	excludes map[string][]string) []conflictFinding {

	since := make(map[string]int64, len(rights))
	for _, v := range rights {
		// The earliest, where a product is held more than once: the question a
		// finding's dates answer is "how long has this combination existed", and the
		// later grant of the same product did not start it.
		if at, ok := since[v.ItemID]; !ok || v.Since < at {
			since[v.ItemID] = v.Since
		}
	}

	var out []conflictFinding
	seen := map[string]bool{}
	for id := range since {
		for _, other := range excludes[id] {
			if _, also := since[other]; !also {
				continue
			}
			key := pairKey(id, other)
			if seen[key] {
				continue
			}
			seen[key] = true
			a, b := id, other
			if a > b {
				a, b = b, a
			}
			out = append(out, conflictFinding{
				Principal: principal, A: a, B: b, SinceA: since[a], SinceB: since[b],
			})
		}
	}
	return out
}

// sortConflicts puts the oldest combination first: what has been true longest is
// what nobody has looked at, and a list worked from the top should start there.
func sortConflicts(f []conflictFinding) {
	sort.SliceStable(f, func(i, j int) bool {
		oi, oj := older(f[i]), older(f[j])
		if oi != oj {
			return oi < oj
		}
		if f[i].Principal != f[j].Principal {
			return f[i].Principal < f[j].Principal
		}
		return f[i].A < f[j].A
	})
}

// older is when the combination began, which is the later of the two grants: a
// pair does not exist until both halves do.
func older(f conflictFinding) int64 {
	if f.SinceA > f.SinceB {
		return f.SinceA
	}
	return f.SinceB
}

package api

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// A number the console states is a counter or a walk, never the length of a page
// (ADR-0365).
//
// The mistake these guards look for is one the codebase made five times before
// anybody noticed it once:
//
//	a list is fetched with a page cap, the console counts its rows, and the count is
//	rendered as a fact about the population.
//
// It is worth checking mechanically for the same reason ADR-0266's rule is: the
// failure is invisible in every environment small enough to develop against. While
// the population fits inside the page the two numbers agree, so the defect passes
// review, passes the tests, and passes every manual check on a fresh engine — and
// then a busy installation asks the question the number exists to answer and gets a
// wrong answer to it. Worse than wrong: because every capped listing here is
// *ordered*, what falls off the page is a contiguous slice rather than a sample, so a
// whole class of subject goes missing together and the count reads zero. A floor an
// operator can work with. A zero is a claim that nothing is there.
//
// What these guards are, honestly: three narrow rules over the text of the console,
// each of which would have caught a real defect, and none of which proves anything.
// They do not follow data flow, so a count taken from a page two assignments away
// from the fetch still gets through — that is how the task inbox's folder badges came
// to be wrong, and no regular expression over this file set would have found it. They
// are a tripwire at the three places the mistake has actually been made, not a proof
// that it cannot be made again. The proof, where one is wanted, is a test against a
// population larger than the page — see api/operations_counts_test.go.

// cappedListings are the list endpoints whose response is a page rather than a
// population, with the cap that makes it one. The console may read any of them; what
// it may not do is treat what came back as the whole set.
//
// Each is named by the path prefix the browser writes, because that is what these
// guards can see. Sub-resources under a key (…/instances/{key}/variables) are not
// listings and are excluded by the matcher below.
var cappedListings = []struct {
	path   string
	cap    int
	signal string // the truncation signal the response carries beside its rows
}{
	{"/api/v1/incidents", maxTaskListMax, "X-Incidents-Truncated"},
	{"/api/v1/tasks", maxTaskListDefault, "X-Tasks-Truncated"},
	{"/api/v1/instances", maxInstanceListDefault, "X-Instances-Truncated"},
}

// completeLists are the names a console variable may carry while holding a list whose
// length *is* its population — so counting it is counting the thing, and [fmtCount]
// may have it.
//
// By variable name rather than by file and line, so that moving the code does not
// silently retire the exemption or, worse, leave it pointing at a line that now says
// something else. Each entry says how the completeness is known, because that is the
// next reader's only question and the answer is not guessable from the call site.
var completeLists = map[string]string{
	"flows": "the collaboration replay's message-flow timeline: handleCollaborationRuntime " +
		"builds it in one pass over the collaboration's own history and applies no cap, so " +
		"the list the browser holds is every message that crossed",
}

var (
	// fmtCountOfLength: the formatter that puts a number in front of a person, handed
	// the length of a list. Whatever that list is, the figure on screen is now the
	// size of it — which is the whole defect when the list is a page.
	fmtCountOfLength = regexp.MustCompile(`fmtCount\s*\(\s*([A-Za-z_$][\w$.]*)\.length\s*\)`)

	// rawGetOfListing: apiRaw exists to hand back the response headers; api() is there
	// for callers that only want the body. Taking the raw form on a capped listing and
	// then not binding `headers` means the caller asked for the one thing that says the
	// page is a page, and dropped it. That is exactly how the instance search came to
	// flag which instances were stuck from a bucket that stopped at 5 000 rows.
	rawGetOfListing = regexp.MustCompile(`(\{[^}]*\})\s*=\s*await\s+apiRaw\s*\(\s*"GET"\s*,\s*([^;]*)`)

	// listingRead: any read of a capped listing, however it is spelled. Excludes the
	// per-key sub-resources (…/instances/${key}/timeline), which are point reads.
	listingRead = regexp.MustCompile(`["` + "`" + `]/api/v1/(incidents|tasks|instances)(?:["` + "`" + `?]|\s*\+)`)

	// mutations never read a page.
	mutation = regexp.MustCompile(`"(POST|DELETE|PUT|PATCH)"`)

	// capAware is the vocabulary a reader uses when they have thought about the bound:
	// they read the truncation signal, they page with a cursor, they set a limit, or
	// they say in prose that what they hold is a page. Any of those is enough — this
	// rule asks for evidence of the thought, and cannot check the thought itself.
	capAware = regexp.MustCompile(`(?i)truncated|\bcapped?\b|\bpage\b|\bcursor\b|before=|\blimit\b|\bsample`)
)

// consoleModules returns the embedded browser modules, as lines.
//
// The console's own ES modules, and not the hosted pages under web/*.html. Those reach
// the API through bare fetch rather than through api()/apiRaw(), so two of the three
// rules below cannot see them by construction — and the shape that matters there is
// already refused outright by
// [TestHostedAppsNeverLookForOneInstanceInTheCappedListing], which does not ask a
// hosted page to explain its cap but forbids the read that has one. Checked when this
// file was written: no hosted page matches any of these patterns today.
func consoleModules(t *testing.T) map[string][]string {
	t.Helper()
	files, err := fs.Glob(webFS, "web/*.js")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no embedded console modules found; these guards would pass vacuously")
	}
	out := make(map[string][]string, len(files))
	for _, f := range files {
		body, err := fs.ReadFile(webFS, f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		out[f] = strings.Split(string(body), "\n")
	}
	return out
}

// TestDisplayedCountsAreNotListLengths is the rule at the point of display: the
// console's number formatter is not handed the length of a list.
//
// It is the narrowest of the three and the one that caught the most: the live
// diagram's incident pill, its per-element badges and the replay's header count were
// all `fmtCount(<something>.length)` over a hundred-row detail page, on a process
// holding thousands of parked tokens.
func TestDisplayedCountsAreNotListLengths(t *testing.T) {
	for file, lines := range consoleModules(t) {
		for i, line := range lines {
			for _, m := range fmtCountOfLength.FindAllStringSubmatch(line, -1) {
				// The bare name, so `state.tasks.length` and `tasks.length` are the same
				// question — an exemption is about the list, not about how it is reached.
				name := m[1]
				if dot := strings.LastIndex(name, "."); dot >= 0 {
					name = name[dot+1:]
				}
				if why, ok := completeLists[name]; ok {
					t.Logf("%s:%d: fmtCount(%s.length) allowed — %s", file, i+1, m[1], why)
					continue
				}
				t.Errorf("%s:%d formats the length of a list as a displayed number:\n  %s\n"+
					"If %q holds a page of a capped listing, that figure is the size of the page "+
					"rather than of what it is counting, and on an ordered listing it can be zero "+
					"while the thing it counts is not empty.\n"+
					"Take the number from the server — a total it computed, or a per-row count it "+
					"counted — rather than from the rows it could fit. If the list really is "+
					"complete, add its variable name to completeLists in this file with how that "+
					"is known.",
					file, i+1, strings.TrimSpace(line), m[1])
			}
		}
	}
}

// TestRawReadsOfACappedListingKeepTheirHeaders is the rule at the point of fetch:
// taking the raw response of a capped listing and dropping its headers.
//
// The instance search did exactly this. It read the whole incident list through
// apiRaw, bound only `data`, bucketed the rows by instance, and flagged a search hit
// whose key turned up in the bucket. Past 5 000 incidents the bucket is a page: two
// hundred running instances that were each parked behind an incident rendered as a
// plain "active", on the surface an operator opens to debug one. The response had
// been saying X-Incidents-Truncated: true the whole time.
func TestRawReadsOfACappedListingKeepTheirHeaders(t *testing.T) {
	for file, lines := range consoleModules(t) {
		for i, line := range lines {
			m := rawGetOfListing.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			target := m[2]
			listing, ok := matchCappedListing(target)
			if !ok {
				continue
			}
			if strings.Contains(m[1], "headers") {
				continue
			}
			t.Errorf("%s:%d takes the raw response of a capped listing and drops its headers:\n  %s\n"+
				"%s is capped at %d rows and says so in %s. apiRaw is the form that hands that "+
				"back; a caller that does not want it should use api() and say why the cap does "+
				"not matter here.",
				file, i+1, strings.TrimSpace(line), listing.path, listing.cap, listing.signal)
		}
	}
}

// TestReadsOfACappedListingNameTheCap is the loosest of the three, and the one to read
// as a prompt rather than as a proof: a read of a capped listing should have something
// near it that shows the bound was considered — the truncation signal, a cursor, an
// explicit limit, or prose saying what the page is.
//
// It is gameable, and that is understood: writing the word "page" nearby satisfies it.
// What it is for is the moment somebody adds the next reader of one of these
// endpoints. That is when the question is cheap to answer and the answer is worth
// writing down, and it is the moment the five defects behind this file all passed
// without anybody asking it.
func TestReadsOfACappedListingNameTheCap(t *testing.T) {
	const window = 12 // lines either side: the call and the comment that introduces it
	for file, lines := range consoleModules(t) {
		for i, line := range lines {
			if !listingRead.MatchString(line) || mutation.MatchString(line) {
				continue
			}
			if _, ok := matchCappedListing(line); !ok {
				continue
			}
			lo, hi := max(0, i-window), min(len(lines), i+window+1)
			if capAware.MatchString(strings.Join(lines[lo:hi], "\n")) {
				continue
			}
			t.Errorf("%s:%d reads a capped listing without anything nearby that names the cap:\n  %s\n"+
				"Say what this page is and what is done about what it cannot hold — read the "+
				"truncation header, page with the cursor, set an explicit limit, or write down "+
				"that a sample is all this needs. Any of those satisfies this rule; the point is "+
				"that the next reader can tell which one was meant.",
				file, i+1, strings.TrimSpace(line))
		}
	}
}

// matchCappedListing reports which capped listing a fragment of console source reads,
// if any. A path with a key segment after the collection (…/instances/${key}/…) is a
// point read, not a listing, and is not one of these.
func matchCappedListing(fragment string) (listing struct {
	path   string
	cap    int
	signal string
}, ok bool) {
	for _, l := range cappedListings {
		idx := strings.Index(fragment, l.path)
		if idx < 0 {
			continue
		}
		rest := fragment[idx+len(l.path):]
		if strings.HasPrefix(rest, "/") {
			continue // a sub-resource under one key
		}
		return l, true
	}
	return listing, false
}

// TestThePageCountGuardsStillBite. A guard that has stopped matching anything is
// worse than no guard: it is a passing test that reads as coverage.
//
// The cases below are the real ones. The lines marked as defects are what the console
// actually said before ADR-0365; the lines marked as
// sound are shapes that live in it now and must keep passing, because a rule that
// fires on them is a rule somebody will delete rather than satisfy.
func TestThePageCountGuardsStillBite(t *testing.T) {
	t.Run("a displayed count off a list length", func(t *testing.T) {
		for _, c := range []struct {
			line   string
			caught bool
			what   string
		}{
			{`incidentEl.textContent = fmtCount(incidents.length);`, true,
				"the live diagram's incident pill, over a 100-row detail page"},
			{`const many = elIncidents.length > 1 ? " " + fmtCount(elIncidents.length) : "";`, true,
				"the per-element incident badge, the number that read 50 on a task holding 5 452"},
			{`root.querySelector("#m-inc-n").textContent = fmtCount(incidents.length);`, true,
				"the replay header's count, capped with its instance's detail page"},
			{`incidentEl.textContent = fmtCount(incidentTotal);`, false,
				"the same pill, taking the server's own total"},
			{`countEl.textContent = fmtCount(rt.instances);`, false,
				"a count the server computed, not a length"},
			{`if (list.length === 1) return "1 incident";`, false,
				"a length used to decide a plural, which is not a figure on screen"},
		} {
			got := fmtCountOfLength.MatchString(c.line)
			if got != c.caught {
				t.Errorf("caught=%v, want %v for %s:\n  %s", got, c.caught, c.what, c.line)
			}
		}
	})

	t.Run("a raw read that drops its headers", func(t *testing.T) {
		for _, c := range []struct {
			line   string
			caught bool
			what   string
		}{
			{`const { data } = await apiRaw("GET", "/api/v1/incidents");`, true,
				"the instance search's incident bucket, which never read the truncation header"},
			{`const { data } = await apiRaw("GET", "/api/v1/tasks");`, true,
				"the same mistake on the task listing"},
			{`const { data, headers } = await apiRaw("GET", "/api/v1/tasks");`, false,
				"the shape the inbox uses: the header comes back with the rows"},
			{`const { data, headers } = await apiRaw("GET", "/api/v1/incidents" + scopeQuery());`, false,
				"a scoped read that still keeps its header"},
		} {
			m := rawGetOfListing.FindStringSubmatch(c.line)
			caught := false
			if m != nil {
				if _, ok := matchCappedListing(m[2]); ok && !strings.Contains(m[1], "headers") {
					caught = true
				}
			}
			if caught != c.caught {
				t.Errorf("caught=%v, want %v for %s:\n  %s", caught, c.caught, c.what, c.line)
			}
		}
	})

	t.Run("which paths are listings", func(t *testing.T) {
		for _, c := range []struct {
			fragment string
			listing  bool
		}{
			{`"/api/v1/tasks"`, true},
			{`"/api/v1/tasks?folder=" + id`, true},
			{`"/api/v1/incidents" + scopeQuery()`, true},
			{`"/api/v1/instances?process=" + key`, true},
			// A key after the collection is a point read of one thing, not a page.
			{"`/api/v1/instances/${key}/timeline`", false},
			{"`/api/v1/tasks/${k}/claim`", false},
			// A different endpoint that merely starts the same way.
			{`"/api/v1/incidents/summary"`, false},
		} {
			if _, ok := matchCappedListing(c.fragment); ok != c.listing {
				t.Errorf("matchCappedListing(%q) = %v, want %v", c.fragment, ok, c.listing)
			}
		}
	})

	t.Run("the guards see the console at all", func(t *testing.T) {
		mods := consoleModules(t)
		if len(mods) < 10 {
			t.Fatalf("only %d console modules found; the guards above would barely look at anything", len(mods))
		}
		var fmtCounts int
		for _, lines := range mods {
			for _, line := range lines {
				if strings.Contains(line, "fmtCount(") {
					fmtCounts++
				}
			}
		}
		if fmtCounts < 10 {
			t.Errorf("only %d fmtCount call sites; either the console stopped using it or the "+
				"read is broken, and either way the first guard is watching nothing", fmtCounts)
		}
	})
}

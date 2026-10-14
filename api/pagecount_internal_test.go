package api

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// A number the console states is a counter or a walk, never the length of a page
// (ADR-0365) — and a capped listing answers in a shape that says so
// (ADR-0378).
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
// What these guards are, honestly: one structural check, three narrow rules over the
// text of the console, and one over the published Postman collection.
//
// The structural one is [TestACappedListingAnswersWithAPage], and it is the only one
// here that proves anything: a capped listing answers with an object that states its
// own total and whether the cap bit, so `response.length` is `undefined` rather than
// the page size. That does not make the mistake impossible — a caller can still count
// `page.items` — but it moves the wrong number out of arm's reach, which is a
// different and better kind of guard than noticing it afterwards.
//
// The text rules are the afterwards. Each would have caught a real defect and none of
// them proves anything. The Postman one ([TestThePostmanCollectionReadsItems]) is there
// because that collection is published for people to copy from and nothing else in this
// repository runs it: a wrong shape in it teaches the wrong shape, silently, outside. They do not follow data flow, so a count taken from a
// page two assignments away from the fetch still gets through — that is how the task
// inbox's folder badges came to be wrong, and no regular expression over this file set
// would have found it. They are a tripwire at the places the mistake has actually been
// made. The proof, where one is wanted, is a test against a population larger than the
// page — see api/operations_counts_test.go.

// cappedListings are the list endpoints whose response is a page rather than a
// population, with the cap that makes it one. The console may read any of them; what
// it may not do is treat what came back as the whole set.
//
// Each is named by the path prefix the browser writes, because that is what these
// guards can see. Sub-resources under a key (…/instances/{key}/variables) are not
// listings and are excluded by the matcher below.
var cappedListings = []struct {
	path string
	cap  int
}{
	{"/api/v1/incidents", maxTaskListMax},
	{"/api/v1/tasks", maxTaskListDefault},
	{"/api/v1/instances", maxInstanceListDefault},
	{"/api/v1/approvals", maxFolderScan},
	{"/api/v1/audit", defaultAuditLimit},
	// Listed after the collection it sits under, because matchCappedListing takes the
	// first entry whose path is a prefix and treats anything under a key as a point read.
	{"/api/v1/instances/search", maxInstanceSearchResults},
}

// truncatedField is the field every one of them states its bound in. It is a constant
// rather than a column in the table above, because the table would then be a place to
// record a listing that says it differently — and one shape for all of them is the
// whole point of the record this file guards.
const truncatedField = "truncated"

// listingPaths is the capped listings' paths as a regexp alternation, longest first so
// /api/v1/instances/search is tried before /api/v1/instances.
func listingPaths() string {
	paths := make([]string, 0, len(cappedListings))
	for _, l := range cappedListings {
		paths = append(paths, regexp.QuoteMeta(l.path))
	}
	sort.Slice(paths, func(i, j int) bool { return len(paths[i]) > len(paths[j]) })
	return strings.Join(paths, "|")
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

	// getOfListing / rowUse: a capped listing's response used as if it were its rows.
	//
	// It used to be one: the body *was* a JSON array, so `.length` on it was the page
	// length and read as a population, and `.map` on it iterated a page while looking
	// like it iterated the set. The body is an object now, so both of those are loud —
	// `undefined` and a TypeError — rather than quietly short. This rule is what stops
	// the shape being written back in the next time somebody ports a caller from memory.
	getOfListing = regexp.MustCompile(`await\s+api(?:Raw|Call)?\s*\(\s*['"` + "`" + `]?GET['"` + "`" + `]?\s*,\s*([^;]*)`)
	rowUse       = regexp.MustCompile(`(?:\)|\.data)\s*\.\s*(length|map|filter|forEach|find|some|every|reduce|slice|sort|at)\b`)

	// listingRead: any read of a capped listing, however it is spelled. Excludes the
	// per-key sub-resources (…/instances/${key}/timeline), which are point reads.
	//
	// Built from cappedListings rather than written out, because a hand-written copy is
	// a second place to remember: /api/v1/audit sat in the table while this pattern
	// still named three endpoints, so the console's audit view read a capped listing
	// with no rule looking at it — and did read it as a bare array.
	listingRead = regexp.MustCompile(`["` + "`" + `](` + listingPaths() + `)(?:["` + "`" + `?]|\s*\+)`)

	// mutations never read a page.
	mutation = regexp.MustCompile(`"(POST|DELETE|PUT|PATCH)"`)

	// capAware is the vocabulary a reader uses when they have thought about the bound:
	// they read the response's `truncated`, they page with its cursor, they set a limit,
	// or they say in prose that what they hold is a page. Any of those is enough — this
	// rule asks for evidence of the thought, and cannot check the thought itself.
	capAware = regexp.MustCompile(`(?i)truncated|\bcapped?\b|\bpage\b|\bcursor\b|before=|\blimit\b|\bsample`)
)

// consoleModules returns the embedded browser modules, as lines.
//
// The console's own ES modules. The hosted pages under web/*.html are read separately,
// by [hostedPages], because they are a different kind of caller: each drives one process
// instance, none of them uses [fmtCount], and their fetch wrapper is spelled differently
// per page. Two of the three text rules therefore do not apply to them — but
// [TestReadsOfACappedListingGoThroughItems] does, and has to, because four hosted pages
// were still reading a listing as a bare array after the envelope landed and no rule
// here looked at them.
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

// hostedPages returns the embedded single-purpose apps under web/*.html, as lines.
//
// They are not the console: each drives one process instance, none of them formats a
// count for a person, and each carries its own small fetch wrapper. What they share
// with the console is that they read these listings, and after the envelope landed four
// of them were still filtering the response as if it were an array —
// `(await api("GET", "/instances?process=" + k)).filter(…)` — which throws rather than
// lying, on a page an outside customer opens.
func hostedPages(t *testing.T) map[string][]string {
	t.Helper()
	files, err := fs.Glob(webFS, "web/*.html")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no embedded hosted pages found; the rule below would pass vacuously")
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

// TestReadsOfACappedListingGoThroughItems is the rule at the point of fetch: the
// response of a capped listing is a page, so it is not iterated or measured directly.
//
// The instance search did the old form of this. It read the whole incident list, which
// was then a bare array, bucketed the rows by instance, and flagged a search hit whose
// key turned up in the bucket. Past 5 000 incidents the bucket is a page: two hundred
// running instances that were each parked behind an incident rendered as a plain
// "active", on the surface an operator opens to debug one.
//
// That exact line would now throw instead of lying, which is the point of the envelope.
// This rule keeps it from being written at all, because "it throws in production" is a
// worse place to find out than here.
func TestReadsOfACappedListingGoThroughItems(t *testing.T) {
	sources := consoleModules(t)
	for file, lines := range hostedPages(t) {
		sources[file] = lines
	}
	for file, lines := range sources {
		for i, line := range lines {
			m := getOfListing.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			listing, ok := matchCappedListing(m[1])
			if !ok {
				continue
			}
			use := rowUse.FindStringSubmatchIndex(m[1])
			if use == nil || goesThroughItems(m[1], use[0]) {
				continue
			}
			t.Errorf("%s:%d uses a capped listing's response as if it were its rows (.%s):\n  %s\n"+
				"%s is capped at %d rows and answers {items, total, totalExact, truncated, "+
				"nextCursor}. The rows are in .items, and how many there really are is in .total "+
				"— reach for the one the number is about.",
				file, i+1, m[1][use[2]:use[3]], strings.TrimSpace(line), listing.path, listing.cap)
		}
	}
}

// TestReadsOfACappedListingNameTheCap is the loosest of the three, and the one to read
// as a prompt rather than as a proof: a read of a capped listing should have something
// near it that shows the bound was considered — the truncation signal, a cursor, an
// explicit limit, or prose saying what the page is.
//
// Loose is not the same as idle. The first run after its path pattern was derived from
// cappedListings instead of written out beside it reported the live panel's search box,
// which had been labelling its picker "Search results (200)" off the row count — the
// original defect, on the control an operator uses to find one instance among many.
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
				"response's `truncated`, page with its `nextCursor`, set an explicit limit, or "+
				"write down that a sample is all this needs. Any of those satisfies this rule; "+
				"the point is that the next reader can tell which one was meant.",
				file, i+1, strings.TrimSpace(line))
		}
	}
}

// TestACappedListingAnswersWithAPage is the structural rule, and the only one in this
// file that holds regardless of how the caller is written: a capped listing's response
// is an object that states what it is, not a bare array that cannot.
//
// The three text rules above look for the mistake after it has been made. This one
// takes the wrong number out of reach: on `[…]`, `response.length` is the page size and
// reads exactly like a population, which is how five call sites came to be wrong in the
// same way. On `{items, total, totalExact, truncated}` it is `undefined`, and the
// number a caller actually wants is a field away and correct.
//
// Checked against a live server rather than against the source: it asks the endpoint
// what it actually answers, so no handler can satisfy it by being written to look
// right. What it cannot do is notice a capped listing nobody added to cappedListings —
// that table is still maintained by hand, and it is the one place a new listing has to
// be registered for any of these four rules to see it.
func TestACappedListingAnswersWithAPage(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	for _, l := range cappedListings {
		t.Run(l.path, func(t *testing.T) {
			code, body := x.do(http.MethodGet, l.path, "")
			if code != http.StatusOK {
				t.Fatalf("GET %s = %d (%s)", l.path, code, body)
			}
			for _, complaint := range pageShapeComplaints(body) {
				t.Errorf("GET %s %s\n  %s\n"+
					"It is capped at %d rows, so what came back is a page. Answer with httpapi.Page: "+
					"httpapi.Rows when a counter or a walk knows the real total, httpapi.PageOf when "+
					"the page is all there is to go on.",
					l.path, complaint, strings.TrimSpace(string(body)), l.cap)
			}
		})
	}
}

// pageShapeComplaints says what is wrong with a capped listing's body, or nothing. It
// is separate from the test above so that [TestThePageCountGuardsStillBite] can hand it
// the shapes this rule exists to refuse — a guard nobody has watched fail is a guard
// nobody knows still works.
func pageShapeComplaints(body []byte) []string {
	if strings.HasPrefix(strings.TrimSpace(string(body)), "[") {
		return []string{"answers with a bare array, which cannot say it is a page and whose .length reads as the population:"}
	}
	var page map[string]json.RawMessage
	if err := json.Unmarshal(body, &page); err != nil {
		return []string{"did not answer with a JSON object:"}
	}
	var out []string
	for _, field := range []string{"items", "total", "totalExact", truncatedField} {
		if _, ok := page[field]; !ok {
			out = append(out, "answers without "+strconv.Quote(field)+
				", so a caller cannot tell a full set from a page:")
		}
	}
	var items []json.RawMessage
	switch err := json.Unmarshal(page["items"], &items); {
	case err != nil:
		out = append(out, "has an items that is not an array:")
	case items == nil:
		out = append(out, "has items null rather than [], so a caller that iterates it breaks on an empty engine:")
	}
	return out
}

// goesThroughItems reports that the rows were unwrapped before being used. The shape
// the hosted pages write — `(((await api(…))||{}).items||[]).find(…)` — puts a closing
// paren immediately before the array method, which is the same two characters the
// mistake makes, so the rule has to look at what came between rather than at what is
// adjacent.
func goesThroughItems(fragment string, useAt int) bool {
	at := strings.Index(fragment, ".items")
	return at >= 0 && at < useAt
}

// TestThePostmanCollectionReadsItems is the same rule as the console's, over the one
// consumer nothing else in this repository runs.
//
// postman/Atlas.postman_collection.json is published for people to import and copy
// from: its test scripts are what somebody pastes into their own script when they
// write against Atlas for the first time. Before the envelope they read
// `pm.response.json()` straight as an array, which is the shape this record removed —
// and unlike every other caller here, no test would have said so. This is that test.
//
// It reads the same cappedListings table the rules above do, because a guard with its
// own copy of the list it guards drifts from it, and the drift is silent in exactly the
// direction that matters.
func TestThePostmanCollectionReadsItems(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "postman", "Atlas.postman_collection.json"))
	if err != nil {
		t.Fatalf("read the published collection: %v", err)
	}
	var doc struct {
		Item []json.RawMessage `json:"item"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode the collection: %v", err)
	}
	checked := 0
	var walk func(items []json.RawMessage, path string)
	walk = func(items []json.RawMessage, path string) {
		for _, it := range items {
			var node struct {
				Name  string            `json:"name"`
				Item  []json.RawMessage `json:"item"`
				Event []struct {
					Script struct {
						Exec []string `json:"exec"`
					} `json:"script"`
				} `json:"event"`
				Request struct {
					Method string `json:"method"`
					URL    struct {
						Raw string `json:"raw"`
					} `json:"url"`
				} `json:"request"`
			}
			if err := json.Unmarshal(it, &node); err != nil {
				t.Fatalf("decode collection item under %q: %v", path, err)
			}
			here := path + "/" + node.Name
			if len(node.Item) > 0 {
				walk(node.Item, here)
				continue
			}
			if node.Request.Method != http.MethodGet {
				continue
			}
			// The collection writes {{baseUrl}} before the path, so the quote the console
			// rules key on is not there; match on the path itself.
			listing, ok := matchCappedListing(node.Request.URL.Raw + `"`)
			if !ok {
				continue
			}
			var script []string
			for _, e := range node.Event {
				script = append(script, e.Script.Exec...)
			}
			// A script that never reads the body cannot read it wrongly. Only the ones
			// that call pm.response.json() are making a claim about the shape.
			body := strings.Join(script, "\n")
			if !strings.Contains(body, "pm.response.json()") {
				continue
			}
			checked++
			if why, bad := usesTheBodyAsRows(body); bad {
				t.Errorf("%s reads %s and %s:\n  %s\n"+
					"That listing is capped at %d rows and answers {items, total, totalExact, "+
					"truncated, nextCursor}. This script is what somebody copies into their own; "+
					"reading the response as an array teaches the shape this collection is "+
					"supposed to demonstrate the end of.",
					here, listing.path, why, strings.Join(script, "\n  "), listing.cap)
			}
		}
	}
	walk(doc.Item, "")
	if checked == 0 {
		t.Fatal("no capped-listing request in the collection carries a test script; this guard " +
			"is watching nothing, which is either a gutted collection or a broken walk")
	}
}

// usesTheBodyAsRows reports that a Postman test script treats the whole response as its
// rows, and says how.
//
// The first version of this rule asked only that the script mention "items" somewhere,
// and a comment naming the envelope satisfied it — so the check passed on a script that
// then did `const tasks = pm.response.json();`. Looking for the word was looking at the
// wrong thing: what matters is whether the *value* is subscripted or iterated, which is
// what this reads.
func usesTheBodyAsRows(script string) (why string, bad bool) {
	const arrayUse = `(?:\[|\.\s*(?:length|map|filter|forEach|find|some|every|reduce|slice|sort|at)\b)`
	if regexp.MustCompile(`pm\.response\.json\(\)\s*` + arrayUse).MatchString(script) {
		return "subscripts or iterates the response itself", true
	}
	for _, m := range regexp.MustCompile(
		`(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*pm\.response\.json\(\)`,
	).FindAllStringSubmatch(script, -1) {
		name := regexp.QuoteMeta(m[1])
		// Unwrapped first is the correct shape, however it is spelled — `page.items`,
		// `(page && page.items) || []`.
		if regexp.MustCompile(`\b` + name + `\s*(?:&&\s*` + name + `\s*)?\.\s*items\b`).MatchString(script) {
			continue
		}
		// The optional `(name || [])` is the defensive spelling this codebase writes; it
		// puts a paren between the name and the method, which a naive pattern misses.
		if regexp.MustCompile(`\(?\s*\b` + name + `\b(?:\s*\|\|\s*\[\s*\])?\s*\)?\s*` + arrayUse).MatchString(script) {
			return "binds the response to " + m[1] + " and then uses it as an array", true
		}
	}
	return "", false
}

// matchCappedListing reports which capped listing a fragment of console source reads,
// if any. A path with a key segment after the collection (…/instances/${key}/…) is a
// point read, not a listing, and is not one of these.
func matchCappedListing(fragment string) (listing struct {
	path string
	cap  int
}, ok bool) {
	for _, l := range cappedListings {
		for _, spelling := range []string{l.path, strings.TrimPrefix(l.path, "/api/v1")} {
			idx := strings.Index(fragment, spelling)
			if idx < 0 {
				continue
			}
			// The hosted pages prepend their own base, so they write "/instances?…".
			// That bare form is only a listing where it begins a string literal —
			// otherwise "#/data/instances?class=…", a client-side route, would match it.
			if spelling != l.path && (idx == 0 || !strings.ContainsAny(fragment[idx-1:idx], "\"'"+"`")) {
				continue
			}
			if strings.HasPrefix(fragment[idx+len(spelling):], "/") {
				continue // a sub-resource under one key
			}
			return l, true
		}
	}
	return listing, false
}

// TestThePageCountGuardsStillBite. A guard that has stopped matching anything is
// worse than no guard: it is a passing test that reads as coverage.
//
// The cases below are the real ones. The lines marked as defects are what the console
// actually said before
// ADR-0378; the lines marked as sound
// are shapes that live in it now and must keep passing, because a rule that
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

	t.Run("a listing response used as its own rows", func(t *testing.T) {
		for _, c := range []struct {
			line   string
			caught bool
			what   string
		}{
			{`const keys = (await api("GET", "/api/v1/incidents")).map(r => r.processInstanceKey);`, true,
				"the instance search's incident bucket, built straight off the page"},
			{`const n = (await api("GET", "/api/v1/tasks?folder=" + id)).length;`, true,
				"a folder badge counted from the page it could fit"},
			{`const stuck = (await apiRaw("GET", "/api/v1/incidents")).data.filter(isStuck);`, true,
				"the same read through apiRaw, which is no safer"},
			{`const page = await api("GET", "/api/v1/tasks?limit=" + PAGE);`, false,
				"the shape the inbox uses: the envelope is bound, then .items is read from it"},
			{`const { items, total } = await api("GET", "/api/v1/incidents" + scopeQuery());`, false,
				"a scoped read that destructures the envelope by name"},
			{`const rows = (await api("GET", "/api/v1/instances?process=" + k)).items.map(toRow);`, false,
				"mapping the rows, which is what .items is for"},
			{"return (((await api(\"GET\",`/instances/search?q=${key}`))||{}).items||[]).find(i=>i.key===key);", false,
				"a hosted page's defensive unwrap: a paren sits next to .find, but .items came first"},
			{`const t = (await api("GET", ` + "`" + `/api/v1/instances/${key}/timeline` + "`" + `)).map(toEvent);`, false,
				"a point read under one key, which is not a page"},
		} {
			caught := false
			if m := getOfListing.FindStringSubmatch(c.line); m != nil {
				if _, ok := matchCappedListing(m[1]); ok {
					if use := rowUse.FindStringIndex(m[1]); use != nil && !goesThroughItems(m[1], use[0]) {
						caught = true
					}
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

	t.Run("a listing body that is not a page", func(t *testing.T) {
		for _, c := range []struct {
			body   string
			caught bool
			what   string
		}{
			{`[{"key":1},{"key":2}]`, true, "the shape every one of these endpoints used to have"},
			{`[]`, true, "an empty page, still unable to say it is one"},
			{`{"incidents":[]}`, true, "the incidents listing's old wrapper, which named the rows but not the bound"},
			{`{"items":[],"total":0,"totalExact":true}`, true, "an envelope that forgot to say whether the cap bit"},
			{`{"items":null,"total":0,"totalExact":true,"truncated":false}`, true, "a nil slice marshalled straight through"},
			{`{"items":[],"total":0,"totalExact":true,"truncated":false}`, false, "an empty page that states its own bound"},
			{`{"items":[{"key":1}],"total":9001,"totalExact":true,"truncated":true,"nextCursor":"7"}`, false,
				"one row of a large population, with the total from a counter"},
		} {
			if got := pageShapeComplaints([]byte(c.body)); (len(got) > 0) != c.caught {
				t.Errorf("caught=%v (%v), want %v for %s:\n  %s", len(got) > 0, got, c.caught, c.what, c.body)
			}
		}
	})

	t.Run("a Postman script that reads the body as rows", func(t *testing.T) {
		for _, c := range []struct {
			script string
			caught bool
			what   string
		}{
			{"const tasks = pm.response.json();\npm.expect(tasks.length).to.be.above(0);", true,
				"the collection's own Golden Path before this record"},
			{"const arr = pm.response.json();\nconst a = (arr || []).find(i => i.state === 'active');", true,
				"the same, one step further along"},
			{"pm.expect(pm.response.json().map(p => String(p.key))).to.include(k);", true,
				"iterating the response without binding it first"},
			{"// answers {items, total, totalExact, truncated}\nconst t = pm.response.json();\nt[0].key;", true,
				"a comment naming the envelope, which is what defeated the first version of this rule"},
			{"const page = pm.response.json();\nconst t = (page && page.items) || [];\nt[0].key;", false,
				"the shape the collection uses now"},
			{"const page = pm.response.json();\npm.expect(page.items).to.be.an('array');", false,
				"asserting the envelope itself"},
			{"const j = pm.response.json();\npm.collectionVariables.set('key', j.key);", false,
				"a single object, which is not a listing and has no rows to miscount"},
		} {
			if _, bad := usesTheBodyAsRows(c.script); bad != c.caught {
				t.Errorf("caught=%v, want %v for %s:\n  %s", bad, c.caught, c.what, c.script)
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

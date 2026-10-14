package examples

import (
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The handbook says how many examples there are, in prose, in both languages and in
// more than one place — and a number in prose is the one claim on the page that no
// other test reads. The chapter said "thirty" while it carried thirty-eight cards,
// and nothing failed: every card was there, only the sentence about them was wrong.
//
// So a count the page states about the examples is written as
//
//	<span data-count="KEY">N</span>
//
// and this test holds every such span to the set it counts. `go test ./examples
// -update`, the command that already regenerates the catalog after an example is
// added, rewrites the spans too — adding an example stays one command, and the
// sentence cannot fall behind the cards again.
//
// The keys, and what each one counts:
//
//	examples          every example: one card per catalogSources entry
//	examples-runnable the cards badged "läuft sofort" — no worker, no credential,
//	                  no internet
//
// A span under any other key fails: a key this test does not know is a count
// nobody checks, which is the state the span exists to end.
var countSpan = regexp.MustCompile(`<span data-count="([a-z-]+)">(\d+)</span>`)

// runnableBadge is the badge a card wears when it runs with nothing configured.
// The German variant is the one looked for; the English one sits beside it on
// every card, so counting both would count every card twice.
const runnableBadge = `<span class="tag ok" data-l="de">läuft sofort</span>`

func TestHandbookCountsMatchTheExamples(t *testing.T) {
	raw, err := os.ReadFile(handbookPath)
	if err != nil {
		t.Fatalf("read %s: %v", handbookPath, err)
	}
	page := string(raw)
	want := map[string]int{
		"examples":          len(catalogSources),
		"examples-runnable": runnableCards(exampleChapter(t, page)),
	}

	stale := 0
	fixed := countSpan.ReplaceAllStringFunc(page, func(span string) string {
		m := countSpan.FindStringSubmatch(span)
		key, stated := m[1], m[2]
		n, known := want[key]
		if !known {
			t.Errorf("%s states a count under the key %q, which this test does not know (it knows %s) — "+
				"a count nobody checks is the drift this test exists to end", handbookPath, key, keysOf(want))
			return span
		}
		if stated == strconv.Itoa(n) {
			return span
		}
		stale++
		if !*update {
			t.Errorf("%s says %s for %q, but there are %d. The examples are the source: run "+
				"`go test ./examples -update` and commit the rewritten page.", handbookPath, stated, key, n)
		}
		return `<span data-count="` + key + `">` + strconv.Itoa(n) + `</span>`
	})
	if stale == 0 || !*update {
		return
	}
	if err := os.WriteFile(handbookPath, []byte(fixed), 0o644); err != nil {
		t.Fatalf("write %s: %v", handbookPath, err)
	}
	t.Logf("rewrote %d count(s) in %s", stale, handbookPath)
}

// runnableCards counts the example cards badged as running with nothing configured.
// It reads the cards rather than the explanation above them: that explanation shows
// the same badge once as a legend, and a count that included it would be one too
// many for as long as nobody looked.
func runnableCards(chapter string) int {
	n := 0
	parts := strings.Split(chapter, cardOpen)
	for _, card := range parts[1:] { // parts[0] is the chapter text before the first card
		if strings.Contains(card, runnableBadge) {
			n++
		}
	}
	return n
}

func keysOf(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

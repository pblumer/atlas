package api

import (
	"strings"
	"testing"
)

// The catalogue screens render timestamps that come from the catalogue tree, and
// that tree keeps time in nanoseconds: catalog.Lifecycle says so in as many words,
// and the service's clock is time.Now().UnixNano().
//
// catalog-admin.js read them as seconds and multiplied by a thousand. The result is
// past the millisecond range a JavaScript Date can hold, so it did not render a
// wrong date — it rendered "Invalid Date", on every release in the table, on the
// last-changed column of the catalogue list, and on the line saying what the shop
// is currently serving.
//
// The guard is about the unit rather than the function: the product carries both,
// because api/releases.go's own publishedAt is seconds, and the two are one
// misreading apart.

// TestTheCatalogueScreensReadNanosecondTimestamps.
func TestTheCatalogueScreensReadNanosecondTimestamps(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	if strings.Contains(src, "* 1000)") {
		t.Error("catalog-admin.js still turns a timestamp into milliseconds by multiplying: " +
			"the catalogue's timestamps are nanoseconds, and that renders as Invalid Date")
	}
	if !strings.Contains(src, "/ 1e6") {
		t.Error("catalog-admin.js no longer divides a timestamp down from nanoseconds; " +
			"if the unit changed, this guard and the catalogue service's clock disagree")
	}
	// The three the screen actually renders. Named one by one because each came from
	// a different payload, and a helper fixed for one of them says nothing about the
	// other two.
	for _, field := range []string{"fmtNano(c.updatedAt)", "fmtNano(r.createdAt)", "fmtNano(diff.releasedAt)"} {
		if !strings.Contains(src, field) {
			t.Errorf("%s is not rendered through the nanosecond formatter", field)
		}
	}
}

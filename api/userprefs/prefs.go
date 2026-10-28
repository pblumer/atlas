// Package userprefs stores how one person reads the console: how a number is
// written, which instant a timestamp means, which words the translated screens
// use (ADR-0459), and whether the console is drawn light or dark (ADR-0507).
//
// It is per user rather than per browser because of the time zone. A language is
// how somebody reads at one desk, and ADR-0267 kept it in the browser for that
// reason; a time zone is where somebody *is*, and a person who answers tasks on a
// laptop and checks a stuck instance on a tablet has one of those and two of
// these. A preference they must set again per device is one they will have set
// wrong somewhere, and a timestamp read in the wrong zone is an operational
// error rather than an ugly one.
//
// The server stores and validates; it does not format. Formatting happens in the
// browser, which is why validation here is strict: an unknown zone stored would
// come back as an exception from `Intl.DateTimeFormat` and break every timestamp
// on the screen instead of this one field.
package userprefs

import (
	"fmt"
	"strings"
	"time"

	// The zone database travels with the binary. Atlas ships as one binary and is
	// run in containers built from scratch, where there is no /usr/share/zoneinfo
	// at all: without this, the same record would be accepted on a developer's
	// laptop and refused in production, which is the worst of the two. The cost is
	// about 450 KB of binary, paid once.
	_ "time/tzdata"
)

// Regions are the number and date formats on offer. It is a fixed list rather
// than a locale picker, and that is the decision: each entry's output is a table
// in the console's own formatter (api/web/numfmt.js), so the preview on the
// settings screen promises exactly what the badge will show. A locale handed to
// `toLocaleString` would render whatever the browser's data says this year, which
// is neither previewable nor testable.
//
// "international" is the default and is the unset value, not a fifth row: the
// console formats that way today, so an instance where nobody opens the screen
// keeps the behaviour it has.
var Regions = []string{"international", "ch", "de", "us"}

// Languages are the locales the console's catalogue actually holds (ADR-0267).
// A language without a catalogue would render every translated screen as its own
// message keys, so the list is the catalogue's and is checked, not trusted. The
// api package replaces it at start-up with the message files the embedded console
// carries (ADR-0497); this default is what the
// package's own tests and a console-less build see.
var Languages = []string{"de", "en"}

// Appearances are how the console may be drawn (ADR-0507): light, dark, or
// following the operating system's scheme. "" is the unset value and draws today's
// light console; "light" is the same picture chosen on purpose, which matters once
// the default changes. The list is the stylesheet's — app.css answers exactly these.
var Appearances = []string{"light", "dark", "system"}

// Prefs is one person's answer to the four questions. Each field's zero value is
// "not chosen", which is why none of them is a pointer and why "international"
// and "" both mean the default: a person who clears the screen gets the console
// they had before it existed.
type Prefs struct {
	// UserID is the authenticated principal's id. It is the store's key and is
	// never read from a request body.
	UserID string `json:"userId"`
	// Region is how a number and a date are written. One of Regions, or "".
	Region string `json:"region"`
	// TimeZone is which instant a timestamp means: an IANA name, or "" for the
	// device's own zone.
	TimeZone string `json:"timeZone"`
	// Language is which words the translated screens use. One of Languages, or "".
	Language string `json:"language"`
	// Appearance is whether the console is drawn light or dark. One of
	// Appearances, or "". It is here rather than in the browser for the reason
	// the time zone is: it belongs to the person, and `system` already covers a
	// person who wants each device to follow its own scheme.
	Appearance string `json:"appearance"`
	// UpdatedAt is when this record was last written, in unix nanos. It is here so
	// a support question ("since when has this been set?") has an answer.
	UpdatedAt int64 `json:"updatedAt,omitempty"`
}

// Validate reports why a record may not be stored, or nil.
//
// Every refusal names the field, because this validates a form somebody filled in
// and "invalid request" would send them looking at all four.
func (p Prefs) Validate() error {
	if p.Region != "" && !in(Regions, p.Region) {
		return fmt.Errorf("unknown region %q — one of %s, or empty for the default",
			p.Region, strings.Join(Regions, ", "))
	}
	if p.Language != "" && !in(Languages, p.Language) {
		return fmt.Errorf("unknown language %q — one of %s, or empty for the default",
			p.Language, strings.Join(Languages, ", "))
	}
	if p.Appearance != "" && !in(Appearances, p.Appearance) {
		return fmt.Errorf("unknown appearance %q — one of %s, or empty for the default",
			p.Appearance, strings.Join(Appearances, ", "))
	}
	if p.TimeZone != "" {
		// LoadLocation is the check rather than a pattern: the question is not
		// whether the string looks like a zone but whether the database — the same
		// one the browser consults — has it. It also refuses a path of its own
		// accord, so a traversal attempt never reaches the store's key.
		if _, err := time.LoadLocation(p.TimeZone); err != nil {
			return fmt.Errorf("unknown time zone %q — an IANA name such as Europe/Zurich, or empty for the device's own", p.TimeZone)
		}
	}
	return nil
}

func in(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

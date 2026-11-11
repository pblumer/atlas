package calendar

import (
	"fmt"
	"strings"
	"time"
)

// The iCalendar (RFC 5545) the CalDAV backend reads and writes is the narrow part an
// availability needs: the start, end, duration, transparency and status of each
// VEVENT, its UID, and — for an event this backend made — the lines its confirmation
// replaces. Everything else is carried through untouched or not read at all.

// icalLine is one unfolded content line: its name, its parameters and its value.
type icalLine struct {
	name   string
	params map[string]string
	value  string
	raw    string
}

// unfold joins the continuation lines of an iCalendar text (a line starting with a space
// or a tab continues the one before) and splits it into content lines.
func unfold(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) && len(out) > 0 {
			out[len(out)-1] += l[1:]
			continue
		}
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func parseLine(raw string) icalLine {
	// The value begins after the first colon that is not inside a quoted parameter.
	quoted := false
	split := -1
	for i, r := range raw {
		if r == '"' {
			quoted = !quoted
		}
		if r == ':' && !quoted {
			split = i
			break
		}
	}
	if split < 0 {
		return icalLine{name: strings.ToUpper(raw), raw: raw}
	}
	head, value := raw[:split], raw[split+1:]
	parts := strings.Split(head, ";")
	l := icalLine{name: strings.ToUpper(parts[0]), params: map[string]string{}, value: value, raw: raw}
	for _, p := range parts[1:] {
		k, v, _ := strings.Cut(p, "=")
		l.params[strings.ToUpper(k)] = strings.Trim(v, `"`)
	}
	return l
}

// icalEvent is one VEVENT as availability reads it.
type icalEvent struct {
	uid        string
	start, end time.Time
	busy       bool
	cancelled  bool
}

// icalTime reads a DATE-TIME or DATE value. A value ending in Z is UTC; one with a
// TZID is in that zone, which must be an IANA name; a floating one, and an all-day
// DATE, is in zone — the Worker's.
func icalTime(l icalLine, zone *time.Location) (t time.Time, allDay bool, err error) {
	loc := zone
	if tzid := l.params["TZID"]; tzid != "" {
		if loc, err = time.LoadLocation(tzid); err != nil {
			return time.Time{}, false, fmt.Errorf("the time zone %q is not an IANA name this worker knows", clip(tzid))
		}
	}
	v := strings.TrimSpace(l.value)
	switch {
	case l.params["VALUE"] == "DATE" || len(v) == 8:
		t, err = time.ParseInLocation("20060102", v, zone)
		return t, true, err
	case strings.HasSuffix(v, "Z"):
		t, err = time.Parse("20060102T150405Z", v)
		return t, false, err
	}
	t, err = time.ParseInLocation("20060102T150405", v, loc)
	return t, false, err
}

// icalDuration reads the DURATION forms an event uses: P1D, PT1H30M, P1W.
func icalDuration(v string) (time.Duration, error) {
	v = strings.TrimSpace(v)
	neg := strings.HasPrefix(v, "-")
	v = strings.TrimLeft(v, "+-")
	if !strings.HasPrefix(v, "P") {
		return 0, fmt.Errorf("%q is not a duration", clip(v))
	}
	var d time.Duration
	num := 0
	seen := false
	inTime := false
	for _, r := range v[1:] {
		switch {
		case r >= '0' && r <= '9':
			num = num*10 + int(r-'0')
			seen = true
			continue
		case r == 'T':
			inTime = true
			continue
		}
		if !seen {
			return 0, fmt.Errorf("%q is not a duration", clip(v))
		}
		unit := map[rune]time.Duration{'W': 7 * 24 * time.Hour, 'D': 24 * time.Hour}
		if inTime {
			unit = map[rune]time.Duration{'H': time.Hour, 'M': time.Minute, 'S': time.Second}
		}
		u, ok := unit[r]
		if !ok {
			return 0, fmt.Errorf("%q is not a duration", clip(v))
		}
		d += time.Duration(num) * u
		num, seen = 0, false
	}
	if neg {
		d = -d
	}
	return d, nil
}

// parseICalEvents reads every VEVENT of a calendar object. An event with no end lasts
// its duration, or — a DATE start — the whole day, or — a DATE-TIME start — no time at
// all (RFC 5545 §3.6.1). A recurring event the server did not expand is refused: its
// first occurrence alone would leave every later one free.
func parseICalEvents(text string, zone *time.Location) ([]icalEvent, error) {
	var out []icalEvent
	var cur *icalEvent
	var dur string
	var hasEnd, allDay bool
	depth := 0
	for _, raw := range unfold(text) {
		l := parseLine(raw)
		switch {
		case l.name == "BEGIN" && strings.EqualFold(l.value, "VEVENT"):
			cur, dur, hasEnd, allDay = &icalEvent{busy: true}, "", false, false
			depth = 0
			continue
		case cur == nil:
			continue
		case l.name == "BEGIN":
			depth++ // a VALARM or another sub-component: its lines are not the event's
			continue
		case l.name == "END" && depth > 0:
			depth--
			continue
		case depth > 0:
			continue
		case l.name == "END" && strings.EqualFold(l.value, "VEVENT"):
			if cur.start.IsZero() {
				return nil, fmt.Errorf("the server answered an event without a start")
			}
			switch {
			case hasEnd:
			case dur != "":
				d, err := icalDuration(dur)
				if err != nil {
					return nil, err
				}
				cur.end = cur.start.Add(d)
			case allDay:
				cur.end = cur.start.AddDate(0, 0, 1)
			default:
				cur.end = cur.start
			}
			out = append(out, *cur)
			cur = nil
			continue
		}
		var err error
		switch l.name {
		case "UID":
			cur.uid = l.value
		case "DTSTART":
			cur.start, allDay, err = icalTime(l, zone)
		case "DTEND":
			cur.end, _, err = icalTime(l, zone)
			hasEnd = true
		case "DURATION":
			dur = l.value
		case "TRANSP":
			if strings.EqualFold(strings.TrimSpace(l.value), "TRANSPARENT") {
				cur.busy = false
			}
		case "STATUS":
			if strings.EqualFold(strings.TrimSpace(l.value), "CANCELLED") {
				cur.busy, cur.cancelled = false, true
			}
		case "RRULE", "RDATE":
			return nil, fmt.Errorf("the server answered a recurring appointment without expanding it; it does not support the expand of a calendar-query, which this Worker needs")
		}
		if err != nil {
			return nil, fmt.Errorf("the server answered an event whose %s cannot be read: %v", strings.ToLower(l.name), err)
		}
	}
	return out, nil
}

// icalText escapes a TEXT value (RFC 5545 §3.3.11).
func icalText(s string) string {
	r := strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`, "\r", `\n`)
	return r.Replace(s)
}

// foldLine folds a content line at 75 octets, never inside a UTF-8 sequence.
func foldLine(line string) string {
	var b strings.Builder
	n := 0
	for _, r := range line {
		w := len(string(r))
		if n+w > 75 {
			b.WriteString("\r\n ")
			n = 1
		}
		b.WriteRune(r)
		n += w
	}
	return b.String()
}

// icalPlaceholder is the calendar object reserve-slot writes: one opaque, confirmed
// VEVENT with the create key in its UID.
func icalPlaceholder(uid, subject string, start, end, stamp time.Time) string {
	const f = "20060102T150405Z"
	lines := []string{
		"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//atlas//calendar worker//EN", "CALSCALE:GREGORIAN",
		"BEGIN:VEVENT",
		"UID:" + uid,
		"DTSTAMP:" + stamp.UTC().Format(f),
		"DTSTART:" + start.UTC().Format(f),
		"DTEND:" + end.UTC().Format(f),
		"SUMMARY:" + icalText(subject),
		"TRANSP:OPAQUE",
		"STATUS:CONFIRMED",
		"END:VEVENT", "END:VCALENDAR",
	}
	for i, l := range lines {
		lines[i] = foldLine(l)
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}

// icalConfirm rewrites a calendar object with the details a confirmation sets: the
// SUMMARY, DESCRIPTION and ATTENDEE lines of its VEVENT are replaced, every other line
// — an alarm a person added, a property this worker does not know — is kept as it was.
func icalConfirm(text string, d Details) string {
	var out []string
	inEvent, depth := false, 0
	for _, raw := range unfold(text) {
		l := parseLine(raw)
		switch {
		case l.name == "BEGIN" && strings.EqualFold(l.value, "VEVENT"):
			inEvent, depth = true, 0
		case inEvent && l.name == "BEGIN":
			depth++
		case inEvent && l.name == "END" && depth > 0:
			depth--
		case inEvent && depth == 0 && l.name == "END" && strings.EqualFold(l.value, "VEVENT"):
			if d.Subject != "" {
				out = append(out, foldLine("SUMMARY:"+icalText(d.Subject)))
			}
			if d.Body != "" {
				out = append(out, foldLine("DESCRIPTION:"+icalText(d.Body)))
			}
			for _, a := range d.Attendees {
				attendee := "ATTENDEE;ROLE=REQ-PARTICIPANT;PARTSTAT=NEEDS-ACTION;RSVP=TRUE"
				if a.Name != "" {
					attendee += `;CN="` + strings.ReplaceAll(a.Name, `"`, "'") + `"`
				}
				out = append(out, foldLine(attendee+":mailto:"+a.Email))
			}
			inEvent = false
		case inEvent && depth == 0:
			switch l.name {
			case "SUMMARY":
				if d.Subject != "" {
					continue
				}
			case "DESCRIPTION":
				if d.Body != "" {
					continue
				}
			case "ATTENDEE":
				if len(d.Attendees) > 0 {
					continue
				}
			}
		}
		out = append(out, foldLine(raw))
	}
	return strings.Join(out, "\r\n") + "\r\n"
}

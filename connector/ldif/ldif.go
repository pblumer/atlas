package ldif

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf8"
)

// parseLDIF reads RFC 2849 content records.
//
// Three things about the format are load-bearing and easy to get wrong:
//
//   - **Continuation lines.** A line beginning with a single space continues the
//     previous one, with the space removed. Producers wrap at 76 characters, so a
//     parser that reads line-by-line splits long DNs and base64 values in half.
//   - **Base64 values.** "attr:: value" means the value is base64. It is how a value
//     with a newline, a leading space, or non-ASCII text is carried, so ignoring the
//     second colon silently corrupts exactly the values that needed the encoding.
//   - **Records are blank-line separated**, and an attribute may repeat within one —
//     that is how a multi-valued attribute is written, so a later value must append
//     rather than replace.
func parseLDIF(data []byte) ([]Entry, error) {
	lines := unfold(string(data))
	var (
		out     []Entry
		current *Entry
	)
	flush := func() {
		if current != nil {
			out = append(out, *current)
			current = nil
		}
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		name, value, err := splitLDIFLine(line)
		if err != nil {
			return nil, err
		}
		switch {
		case strings.EqualFold(name, "version"):
			// "version: 1" heads the file and is not part of any entry.
			continue
		case strings.EqualFold(name, "dn"):
			flush()
			current = &Entry{DN: value, Attributes: map[string][]string{}}
		case current == nil:
			return nil, fmt.Errorf("ldif: attribute %q appears before any dn", name)
		default:
			current.Attributes[name] = append(current.Attributes[name], value)
		}
	}
	flush()
	if len(out) == 0 {
		return nil, fmt.Errorf("ldif: the file holds no entries")
	}
	return out, nil
}

// unfold joins RFC 2849 continuation lines — a line starting with one space belongs
// to the line before it — and normalises CRLF.
func unfold(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimPrefix(s, "\ufeff")
	raw := strings.Split(s, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		if strings.HasPrefix(line, " ") && len(out) > 0 {
			out[len(out)-1] += line[1:]
			continue
		}
		out = append(out, line)
	}
	return out
}

// splitLDIFLine reads one "attr: value" or "attr:: base64" line.
func splitLDIFLine(line string) (name, value string, err error) {
	name, rest, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", fmt.Errorf("ldif: line %q has no attribute separator", line)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", fmt.Errorf("ldif: line %q names no attribute", line)
	}
	switch {
	case strings.HasPrefix(rest, "<"):
		// "attr:< url" fetches the value from a URL. This connector reads a file it
		// was handed, not the network, and quietly producing an empty attribute would
		// hide the difference.
		return "", "", fmt.Errorf("ldif: attribute %q reads its value from a URL, which this connector does not fetch", name)
	case strings.HasPrefix(rest, ":"):
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rest[1:]))
		if err != nil {
			return "", "", fmt.Errorf("ldif: attribute %q is marked base64 but does not decode: %w", name, err)
		}
		return name, string(raw), nil
	default:
		return name, strings.TrimSpace(rest), nil
	}
}

// renderLDIF writes entries as RFC 2849 content records.
func renderLDIF(entries []Entry) string {
	var b strings.Builder
	b.WriteString("version: 1\n")
	for _, e := range entries {
		b.WriteByte('\n')
		writeLDIFLine(&b, "dn", e.DN)
		for _, name := range attrNames(e) {
			for _, v := range e.Attributes[name] {
				writeLDIFLine(&b, name, v)
			}
		}
	}
	return b.String()
}

// writeLDIFLine writes one attribute, choosing base64 when the plain form would not
// survive a round trip.
func writeLDIFLine(b *strings.Builder, name, value string) {
	if needsBase64(value) {
		b.WriteString(name)
		b.WriteString(":: ")
		b.WriteString(base64.StdEncoding.EncodeToString([]byte(value)))
		b.WriteByte('\n')
		return
	}
	b.WriteString(name)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteByte('\n')
}

// needsBase64 reports whether a value must be encoded to survive the format.
//
// RFC 2849 requires it for anything that is not "safe": a leading space or colon or
// less-than (which would be read as syntax), a trailing space (which a reader would
// trim away), a newline (which would look like a new attribute), and any non-ASCII
// byte. Getting this wrong is the quiet kind of bug — the file still parses, it just
// no longer says what it said.
func needsBase64(v string) bool {
	if v == "" {
		return false
	}
	if strings.HasPrefix(v, " ") || strings.HasPrefix(v, ":") || strings.HasPrefix(v, "<") {
		return true
	}
	if strings.HasSuffix(v, " ") {
		return true
	}
	if strings.ContainsAny(v, "\n\r\x00") {
		return true
	}
	return !isASCII(v)
}

func isASCII(s string) bool {
	// Invalid UTF-8 is *not* plain ASCII: emitting those bytes raw would produce a
	// file a reader cannot decode, so the caller must encode them.
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}

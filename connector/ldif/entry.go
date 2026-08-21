package ldif

import (
	"fmt"
	"sort"
	"strings"
)

// The file formats this connector reads and writes.
const (
	// FormatLDIF is RFC 2849's LDAP Data Interchange Format — MIM's *LDIF* agent.
	FormatLDIF = "ldif"
	// FormatDSML is Directory Services Markup Language v1 — MIM's *DSML* agent.
	FormatDSML = "dsml"
)

// The two directions a file task runs in.
const (
	OperationRead  = "read"
	OperationWrite = "write"
)

// Formats lists the formats, sorted, for the messages that say what was expected.
func Formats() []string { return []string{FormatDSML, FormatLDIF} }

// KnownFormat reports whether name is a format this connector handles. Unlike the
// text-file connector there is no default: a file is LDIF or it is DSML, and guessing
// from the bytes is how a malformed file becomes a plausible-looking empty result.
func KnownFormat(name string) bool { return name == FormatLDIF || name == FormatDSML }

// Entry is one directory entry: its distinguished name and its multi-valued
// attributes. It is deliberately the shape [connector/ldap]'s search and
// [connector/ad]'s DirSync return, so a process handles a file and a live directory
// the same way.
type Entry struct {
	DN         string
	Attributes map[string][]string
}

// Parse reads a file of directory entries.
func Parse(format string, data []byte) ([]Entry, error) {
	switch format {
	case FormatLDIF:
		return parseLDIF(data)
	case FormatDSML:
		return parseDSML(data)
	default:
		return nil, fmt.Errorf("unknown format %q (want %s)", format, strings.Join(Formats(), " or "))
	}
}

// Render writes entries out.
func Render(format string, entries []Entry) (string, error) {
	if len(entries) == 0 {
		return "", fmt.Errorf("there are no entries to write")
	}
	for i, e := range entries {
		if strings.TrimSpace(e.DN) == "" {
			return "", fmt.Errorf("entry %d has no dn; a directory entry is addressed by its name", i)
		}
	}
	switch format {
	case FormatLDIF:
		return renderLDIF(entries), nil
	case FormatDSML:
		return renderDSML(entries), nil
	default:
		return "", fmt.Errorf("unknown format %q (want %s)", format, strings.Join(Formats(), " or "))
	}
}

// EntriesToJSON turns entries into the JSON-ready shape a process variable holds —
// the same one the directory connectors write, so downstream handling is shared.
func EntriesToJSON(entries []Entry) any {
	out := make([]any, 0, len(entries))
	for _, e := range entries {
		attrs := make(map[string]any, len(e.Attributes))
		for name, vals := range e.Attributes {
			attrs[name] = vals
		}
		out = append(out, map[string]any{"dn": e.DN, "attributes": attrs})
	}
	return out
}

// attrNames returns an entry's attribute names in a stable order, so a file written
// twice from the same entries is byte-for-byte the same file.
func attrNames(e Entry) []string {
	names := make([]string, 0, len(e.Attributes))
	for n := range e.Attributes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

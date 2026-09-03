package ldif

import (
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"html"
	"strings"
)

// DSML v1 is XML, so it is read with encoding/xml against a shape that ignores
// namespace prefixes. A file in the wild is <dsml:...> or <ds:...> or unprefixed
// depending on who wrote it, and Go matches on local names when the struct tag names
// no namespace — which is exactly the leniency this format needs.

// dsmlDocument is the subset of DSML v1 this worker reads.
type dsmlDocument struct {
	Entries []dsmlEntry `xml:"directory-entries>entry"`
}

type dsmlEntry struct {
	DN string `xml:"dn,attr"`
	// ObjectClass is DSML's dedicated element for the object classes; every other
	// attribute is an <attr>. Folding it back into a normal attribute is what makes
	// the result the same shape a directory read produces.
	ObjectClass dsmlObjectClass `xml:"objectclass"`
	Attributes  []dsmlAttr      `xml:"attr"`
}

type dsmlObjectClass struct {
	Values []string `xml:"oc-value"`
}

type dsmlAttr struct {
	Name   string      `xml:"name,attr"`
	Values []dsmlValue `xml:"value"`
}

type dsmlValue struct {
	Encoding string `xml:"encoding,attr"`
	Text     string `xml:",chardata"`
}

// parseDSML reads a DSML v1 document into entries.
func parseDSML(data []byte) ([]Entry, error) {
	var doc dsmlDocument
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("dsml: %w", err)
	}
	if len(doc.Entries) == 0 {
		return nil, fmt.Errorf("dsml: the document holds no directory entries")
	}
	out := make([]Entry, 0, len(doc.Entries))
	for _, e := range doc.Entries {
		if strings.TrimSpace(e.DN) == "" {
			return nil, fmt.Errorf("dsml: an entry has no dn")
		}
		attrs := map[string][]string{}
		if len(e.ObjectClass.Values) > 0 {
			attrs["objectClass"] = append(attrs["objectClass"], e.ObjectClass.Values...)
		}
		for _, a := range e.Attributes {
			name := strings.TrimSpace(a.Name)
			if name == "" {
				return nil, fmt.Errorf("dsml: entry %q has an attribute with no name", e.DN)
			}
			for _, v := range a.Values {
				text := v.Text
				// DSML carries a binary value base64-encoded, marked on the value
				// rather than on the attribute as LDIF does.
				if strings.EqualFold(strings.TrimSpace(v.Encoding), "base64") {
					raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
					if err != nil {
						return nil, fmt.Errorf("dsml: a base64 value of %q in entry %q does not decode: %w", name, e.DN, err)
					}
					text = string(raw)
				}
				attrs[name] = append(attrs[name], text)
			}
		}
		out = append(out, Entry{DN: e.DN, Attributes: attrs})
	}
	return out, nil
}

// renderDSML writes entries as a DSML v1 document.
//
// It is written out directly rather than through encoding/xml's marshaller because
// the output has to carry the namespace prefix readers expect, and because objectClass
// takes DSML's dedicated element rather than a generic <attr> — neither of which a
// struct-tag round trip expresses without more scaffolding than the writing is worth.
func renderDSML(entries []Entry) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<dsml:dsml xmlns:dsml="http://www.dsml.org/DSML">` + "\n")
	b.WriteString("  <dsml:directory-entries>\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "    <dsml:entry dn=%q>\n", html.EscapeString(e.DN))
		if classes := e.Attributes["objectClass"]; len(classes) > 0 {
			b.WriteString("      <dsml:objectclass>\n")
			for _, c := range classes {
				fmt.Fprintf(&b, "        <dsml:oc-value>%s</dsml:oc-value>\n", html.EscapeString(c))
			}
			b.WriteString("      </dsml:objectclass>\n")
		}
		for _, name := range attrNames(e) {
			if name == "objectClass" {
				continue // already written as DSML's own element
			}
			fmt.Fprintf(&b, "      <dsml:attr name=%q>\n", html.EscapeString(name))
			for _, v := range e.Attributes[name] {
				if needsBase64(v) {
					fmt.Fprintf(&b, "        <dsml:value encoding=\"base64\">%s</dsml:value>\n",
						base64.StdEncoding.EncodeToString([]byte(v)))
					continue
				}
				fmt.Fprintf(&b, "        <dsml:value>%s</dsml:value>\n", html.EscapeString(v))
			}
			b.WriteString("      </dsml:attr>\n")
		}
		b.WriteString("    </dsml:entry>\n")
	}
	b.WriteString("  </dsml:directory-entries>\n")
	b.WriteString("</dsml:dsml>\n")
	return b.String()
}

package mimimport

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The MIMWAL activity library keeps an activity's actual work in serialised .NET
// collections hung off the element as WF property elements: an UpdateResources
// carries UpdatesTable and QueriesTable, a GenerateUniqueValue carries
// ValueExpressions and LdapQueriesTable. That is where the named queries and the
// assignments live — the substance of the step — and in the raw XOML it
// is thousands of characters of Hashtable entries with the assembly-qualified
// type of every cell repeated on it.
//
// This file renders those collections as a small table so a reviewer can read
// the step without reading the markup. The markup itself stays in
// atlas:mimSource; nothing here replaces it.
//
// # What is decoded, and what is not
//
// The *structure* is mechanical and safe: a Hashtable keyed "row:column" plus a
// Count entry is a table, an ArrayList is an ordered list. The *meaning of a
// column* is not. MIMWAL's own editor labels the updates grid Target | Value |
// Allow Null, but the data does not bear that out: across the one real workflow
// this was checked against, column 1 holds a literal in nine rows — nothing can
// be assigned to a literal — and column 0 holds a query result in six, which
// cannot be written to either. With no reference that settles it, naming the
// columns would state something unverified about every imported activity, so the
// columns are rendered by position and left for a reviewer to interpret.

// tableKey matches a Hashtable key of the form "<row>:<column>"; countKey is the
// row count MIMWAL writes into the same table.
var tableKey = regexp.MustCompile(`^(\d+):(\d+)$`)

const countKey = "Count"

// mimTables renders every serialised collection an activity hangs off itself,
// in document order, or "" when it carries none.
func mimTables(n xnode) string {
	var b strings.Builder
	for _, prop := range n.Kids {
		// WF writes a property as <Type.Property>; anything else is a child
		// activity, not this activity's data.
		name, isProp := propertyName(prop.local())
		if !isProp {
			continue
		}
		for _, coll := range prop.Kids {
			body := renderCollection(coll)
			if body == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(name)
			b.WriteString(body)
		}
	}
	return b.String()
}

// propertyName splits a WF property element's local name (Type.Property) into
// the property part, reporting whether the element is a property at all.
func propertyName(local string) (string, bool) {
	i := strings.LastIndexByte(local, '.')
	if i < 0 || i == len(local)-1 {
		return "", false
	}
	return local[i+1:], true
}

// renderCollection renders one serialised .NET collection, or "" if the element
// is not one this package understands.
func renderCollection(coll xnode) string {
	switch strings.ToLower(coll.local()) {
	case "hashtable":
		return renderHashtable(coll)
	case "arraylist":
		return renderList(coll)
	default:
		return ""
	}
}

// renderList renders an ArrayList: its entries in order, one per line.
func renderList(coll xnode) string {
	var b strings.Builder
	n := 0
	for _, item := range coll.Kids {
		fmt.Fprintf(&b, "\n  [%d] %s", n, oneLine(item.Text))
		n++
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(" (%s)%s", plural(n, "entry", "entries"), b.String())
}

// renderHashtable renders a Hashtable keyed "row:column" as that table, rows in
// numeric order and cells in column order. Entries whose key is not a cell
// reference — the Count MIMWAL writes alongside, or anything unexpected — are
// listed after the table rather than dropped.
func renderHashtable(coll xnode) string {
	cells := map[int]map[int]string{}
	var extra []string
	count, hasCount := -1, false
	for _, item := range coll.Kids {
		key := entryKey(item)
		val := oneLine(item.Text)
		if strings.EqualFold(key, countKey) {
			// MIMWAL writes the row count alongside the cells. In every table of
			// the workflow this was checked against it equalled the number of
			// decoded rows, so it is a check rather than content: reported only
			// when it disagrees, which is the case that means a row went missing.
			if n, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
				count, hasCount = n, true
				continue
			}
		}
		m := tableKey.FindStringSubmatch(key)
		if m == nil {
			if key != "" {
				extra = append(extra, key+" = "+val)
			}
			continue
		}
		row, _ := strconv.Atoi(m[1])
		col, _ := strconv.Atoi(m[2])
		if cells[row] == nil {
			cells[row] = map[int]string{}
		}
		cells[row][col] = val
	}
	if len(cells) == 0 && len(extra) == 0 && !hasCount {
		return ""
	}
	if hasCount && count != len(cells) {
		extra = append(extra, fmt.Sprintf("%s = %d, but %s decoded — a row is missing, see atlas:mimSource",
			countKey, count, plural(len(cells), "row", "rows")))
	}

	rows := make([]int, 0, len(cells))
	for r := range cells {
		rows = append(rows, r)
	}
	sort.Ints(rows)

	var b strings.Builder
	fmt.Fprintf(&b, " (%s)", plural(len(rows), "row", "rows"))
	for _, r := range rows {
		cols := make([]int, 0, len(cells[r]))
		for c := range cells[r] {
			cols = append(cols, c)
		}
		sort.Ints(cols)
		vals := make([]string, 0, len(cols))
		for _, c := range cols {
			vals = append(vals, cells[r][c])
		}
		fmt.Fprintf(&b, "\n  [%d] %s", r, strings.Join(vals, " | "))
	}
	for _, e := range extra {
		fmt.Fprintf(&b, "\n  %s", e)
	}
	return b.String()
}

// entryKey returns a collection entry's key: the text of its <x:Key> child, or
// of that child's own single element when the key is itself a typed value —
// <x:Key><String>0:1</String></x:Key> is how MIMWAL writes one.
func entryKey(item xnode) string {
	for _, k := range item.Kids {
		if !strings.EqualFold(k.local(), "Key") {
			continue
		}
		for _, inner := range k.Kids {
			if s := strings.TrimSpace(inner.Text); s != "" {
				return s
			}
		}
		return strings.TrimSpace(k.Text)
	}
	return ""
}

// oneLine collapses a cell's whitespace so a table row stays one line: a MIM
// expression may be written across several in the source.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

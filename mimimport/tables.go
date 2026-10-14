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
// This file decodes those collections into [mimCollection] and renders them
// three ways, all of which are additions to the preserved markup rather than
// replacements for it:
//
//   - as a small table on the activity's documentation, so a reviewer can read
//     the step without reading the markup (renderCollections);
//   - as <atlas:mimCollection> extension elements on the node, so a tool can
//     address one row without re-parsing XOML (emitCollections, emit.go);
//   - as one Report item per row, so a migration worked off the report sees
//     every read and write rather than one "preserved" node (convert.go).
//
// The markup itself stays in atlas:mimSource; nothing here replaces it.
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
// columns are rendered by position and left for a reviewer to interpret. That
// restraint is what keeps the structured form honest: <atlas:mimCell column="1">
// says where a cell sat, never what it does.

// tableKey matches a Hashtable key of the form "<row>:<column>"; countKey is the
// row count MIMWAL writes into the same table.
var tableKey = regexp.MustCompile(`^(\d+):(\d+)$`)

const countKey = "Count"

// Collection kinds. A Hashtable keyed "row:column" is a table; an ArrayList is
// an ordered list, which this package models as one single-cell row per entry so
// both shapes have one representation.
const (
	kindTable = "table"
	kindList  = "list"
)

// mimCell is one cell of a decoded collection: where it sat, and what it held.
// text is the cell as MIM wrote it, with only outer whitespace trimmed — a MIM
// expression can carry a string literal whose spacing is part of its value, so
// the collapsing that keeps the documentation table on one line happens at
// render time and never in the decoded value.
type mimCell struct {
	column int
	text   string
}

// mimRow is one row of a table (MIM's own row number) or one entry of a list
// (its position), holding its cells in column order.
type mimRow struct {
	index int
	cells []mimCell
}

// mimCollection is one serialised .NET collection an activity hangs off itself.
type mimCollection struct {
	property string // the WF property it hangs off: UpdatesTable, ValueExpressions, …
	kind     string // kindTable or kindList
	rows     []mimRow
	// notes hold what was decoded but is not a cell: an entry whose key is
	// neither a cell reference nor the Count, and the Count that disagrees with
	// the decoded rows — the case that means a row went missing.
	notes []string
	// counted records that the collection carried a Count entry, which is what
	// tells an empty table apart from a property holding nothing at all.
	counted bool
}

// mimCollections decodes every serialised collection an activity hangs off
// itself, in document order. A property this package does not understand is
// skipped rather than reported as an empty table: it stays in atlas:mimSource,
// unsummarised, which is the honest answer for markup nothing here can read.
func mimCollections(n xnode) []mimCollection {
	var out []mimCollection
	for _, prop := range n.Kids {
		// WF writes a property as <Type.Property>; anything else is a child
		// activity, not this activity's data.
		name, isProp := propertyName(prop.local())
		if !isProp {
			continue
		}
		for _, coll := range prop.Kids {
			c, ok := decodeCollection(coll)
			if !ok || c.isEmpty() {
				continue
			}
			c.property = name
			out = append(out, c)
		}
	}
	return out
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

// decodeCollection decodes one serialised .NET collection, reporting whether the
// element is one this package understands.
func decodeCollection(coll xnode) (mimCollection, bool) {
	switch strings.ToLower(coll.local()) {
	case "hashtable":
		return decodeHashtable(coll), true
	case "arraylist":
		return decodeList(coll), true
	default:
		return mimCollection{}, false
	}
}

// decodeList decodes an ArrayList: its entries in order, one single-cell row
// each, so a list and a table are walked the same way downstream.
func decodeList(coll xnode) mimCollection {
	c := mimCollection{kind: kindList}
	for _, item := range coll.Kids {
		c.rows = append(c.rows, mimRow{
			index: len(c.rows),
			cells: []mimCell{{column: 0, text: strings.TrimSpace(item.Text)}},
		})
	}
	return c
}

// decodeHashtable decodes a Hashtable keyed "row:column" as that table, rows in
// numeric order and cells in column order. Entries whose key is not a cell
// reference — the Count MIMWAL writes alongside, or anything unexpected — become
// notes rather than being dropped.
func decodeHashtable(coll xnode) mimCollection {
	c := mimCollection{kind: kindTable}
	cells := map[int]map[int]string{}
	count := -1
	for _, item := range coll.Kids {
		key := entryKey(item)
		val := strings.TrimSpace(item.Text)
		if strings.EqualFold(key, countKey) {
			// MIMWAL writes the row count alongside the cells. In every table of
			// the workflow this was checked against it equalled the number of
			// decoded rows, so it is a check rather than content: reported only
			// when it disagrees, which is the case that means a row went missing.
			if n, err := strconv.Atoi(val); err == nil {
				count, c.counted = n, true
				continue
			}
		}
		m := tableKey.FindStringSubmatch(key)
		if m == nil {
			if key != "" {
				c.notes = append(c.notes, key+" = "+oneLine(val))
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

	rows := make([]int, 0, len(cells))
	for r := range cells {
		rows = append(rows, r)
	}
	sort.Ints(rows)
	for _, r := range rows {
		cols := make([]int, 0, len(cells[r]))
		for col := range cells[r] {
			cols = append(cols, col)
		}
		sort.Ints(cols)
		row := mimRow{index: r}
		for _, col := range cols {
			row.cells = append(row.cells, mimCell{column: col, text: cells[r][col]})
		}
		c.rows = append(c.rows, row)
	}

	if c.counted && count != len(c.rows) {
		c.notes = append(c.notes, fmt.Sprintf("%s = %d, but %s decoded — a row is missing, see atlas:mimSource",
			countKey, count, plural(len(c.rows), "row", "rows")))
	}
	return c
}

// isEmpty reports whether a decoded collection says nothing at all. An empty
// table that carried a Count is not empty: the activity states it has no rows,
// which is different from a property holding nothing this package can read.
func (c mimCollection) isEmpty() bool {
	if len(c.rows) > 0 || len(c.notes) > 0 {
		return false
	}
	return !(c.kind == kindTable && c.counted)
}

// units returns the singular and plural word for what this collection holds, so
// a table is counted in rows and a list in entries wherever either is reported.
func (c mimCollection) units() (one, many string) {
	if c.kind == kindList {
		return "entry", "entries"
	}
	return "row", "rows"
}

// label names one row for a reader: "UpdatesTable row 0 of 2". It is what a
// Report item and a reviewer's checklist are keyed by.
func (c mimCollection) label(r mimRow) string {
	one, _ := c.units()
	return fmt.Sprintf("%s %s %d of %d", c.property, one, r.index, len(c.rows))
}

// cellsOneLine joins a row's cells for a one-line rendering — the documentation
// table and a Report item — collapsing each cell's own whitespace so a MIM
// expression written across several lines stays on one.
func cellsOneLine(r mimRow) string {
	out := make([]string, 0, len(r.cells))
	for _, cell := range r.cells {
		out = append(out, oneLine(cell.text))
	}
	return strings.Join(out, " | ")
}

// renderCollections renders decoded collections as the small table that goes on
// the activity's documentation: the property, how many rows it holds, and the
// rows by position.
func renderCollections(cols []mimCollection) string {
	var b strings.Builder
	for _, c := range cols {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		one, many := c.units()
		fmt.Fprintf(&b, "%s (%s)", c.property, plural(len(c.rows), one, many))
		for _, r := range c.rows {
			fmt.Fprintf(&b, "\n  [%d] %s", r.index, cellsOneLine(r))
		}
		for _, n := range c.notes {
			fmt.Fprintf(&b, "\n  %s", n)
		}
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

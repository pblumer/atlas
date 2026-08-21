package csvimport

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"sort"
	"strings"
)

// The text-table formats this connector reads and writes (ADR-0139, amended).
//
// All three describe the same thing — a table of records in a text file — and differ
// only in how a record is delimited and how a field is found inside it. So they share
// the [Column] layout, the type coercion, and everything downstream: what a process
// gets back is a list of row objects whichever format the file arrived in.
//
// They are formats of one connector rather than three connectors for the same
// reason: unlike the SQL products (ADR-0170), nothing here can be pointed at the
// wrong one silently. A fixed-width layout applied to an LDIF file does not quietly
// produce plausible rows; it fails on the first record, loudly, at the moment the
// file is read.
const (
	// FormatCSV is a delimited text file — MIM's *Delimited text file* agent.
	FormatCSV = "csv"
	// FormatFixedWidth is a positional file whose columns are found by character
	// width — MIM's *Fixed-Width text file* agent, and how a mainframe HR extract
	// usually arrives.
	FormatFixedWidth = "fixed-width"
	// FormatAVP is an attribute-value pair file: "name: value" lines, one record per
	// blank-line-separated block — MIM's *Attribute-Value Pair text file* agent.
	FormatAVP = "avp"
)

// Formats lists the readable/writable formats, sorted, for the messages that have to
// say what was expected.
func Formats() []string { return []string{FormatAVP, FormatCSV, FormatFixedWidth} }

// KnownFormat reports whether name is a format this connector handles. The empty
// string is CSV, which is what every model authored before formats existed.
func KnownFormat(name string) bool {
	switch name {
	case "", FormatCSV, FormatFixedWidth, FormatAVP:
		return true
	default:
		return false
	}
}

// Parse reads data in the configured format into row objects.
func Parse(cfg Config, data []byte) ([]map[string]any, error) {
	switch cfg.Format {
	case "", FormatCSV:
		return ParseRows(cfg, data)
	case FormatFixedWidth:
		return parseFixedWidth(cfg, data)
	case FormatAVP:
		return parseAVP(cfg, data)
	default:
		return nil, fmt.Errorf("unknown format %q (want %s)", cfg.Format, strings.Join(Formats(), ", "))
	}
}

// Render writes rows out in the configured format. It is the inverse of [Parse] and
// exists because an identity feed is as often produced as consumed: MIM's file agents
// both import and export, and a process that has assembled a set of accounts needs
// somewhere to put them.
func Render(cfg Config, rows []map[string]any) (string, error) {
	if cfg.Format != FormatCSV && cfg.Format != "" && cfg.Format != FormatFixedWidth && cfg.Format != FormatAVP {
		return "", fmt.Errorf("unknown format %q (want %s)", cfg.Format, strings.Join(Formats(), ", "))
	}
	cols := renderColumns(cfg, rows)
	if len(cols) == 0 {
		return "", fmt.Errorf("nothing to write: no columns are configured and the rows carry no fields")
	}
	switch cfg.Format {
	case FormatFixedWidth:
		return renderFixedWidth(cfg, cols, rows)
	case FormatAVP:
		return renderAVP(cols, rows), nil
	default:
		return renderCSV(cfg, cols, rows)
	}
}

// renderColumns decides the output's field order: the configured layout when there is
// one, and otherwise every field name the rows carry, sorted — so a file written from
// unconfigured rows is at least stable between runs rather than following Go's map
// iteration.
func renderColumns(cfg Config, rows []map[string]any) []Column {
	if len(cfg.Columns) > 0 {
		return cfg.Columns
	}
	seen := map[string]struct{}{}
	var names []string
	for _, r := range rows {
		for name := range r {
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
	sort.Strings(names)
	cols := make([]Column, 0, len(names))
	for _, n := range names {
		cols = append(cols, Column{Name: n})
	}
	return cols
}

// parseFixedWidth splits each line by the configured column widths. A line shorter
// than the layout yields empty cells for the columns past its end rather than an
// error: a ragged export is dirty data, which this connector passes through to be
// validated downstream, exactly as a ragged CSV row is.
func parseFixedWidth(cfg Config, data []byte) ([]map[string]any, error) {
	if len(cfg.Columns) == 0 {
		return nil, fmt.Errorf("a fixed-width file must declare its columns and their widths")
	}
	for _, c := range cfg.Columns {
		if c.Width <= 0 {
			return nil, fmt.Errorf("fixed-width column %q needs a positive width", c.Name)
		}
	}
	lines := textLines(data)
	if len(lines) == 0 {
		return nil, fmt.Errorf("file is empty")
	}
	if cfg.hasHeader() {
		lines = lines[1:] // the header is positional too, so it carries no information
	}
	rows := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		// Runes, not bytes: a fixed-width column is a count of characters, and an
		// umlaut in a name would otherwise shift every column after it.
		r := []rune(line)
		at := 0
		row := make(map[string]any, len(cfg.Columns))
		for _, col := range cfg.Columns {
			cell := ""
			if at < len(r) {
				end := at + col.Width
				if end > len(r) {
					end = len(r)
				}
				cell = string(r[at:end])
			}
			row[col.Name] = coerceCell(strings.TrimSpace(cell), col.Type)
			at += col.Width
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// parseAVP reads "name: value" lines, one record per blank-line-separated block.
//
// With a column layout the named fields are picked out and anything else in the file
// is ignored; without one every key becomes a field, which is the "just turn this
// into JSON" case the CSV format's header row covers.
func parseAVP(cfg Config, data []byte) ([]map[string]any, error) {
	var (
		rows    []map[string]any
		current map[string]string
	)
	flush := func() {
		if len(current) > 0 {
			rows = append(rows, avpRow(cfg, current))
			current = nil
		}
	}
	for _, line := range textLines(data) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			flush()
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			continue // a comment
		}
		name, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			return nil, fmt.Errorf("attribute-value line %q has no separator", trimmed)
		}
		if current == nil {
			current = map[string]string{}
		}
		// A repeated key keeps the first value, matching the CSV header rule: a row
		// object holds one value per field, and picking the last would make the
		// result depend on how far down the file a duplicate happened to appear.
		key := strings.TrimSpace(name)
		if _, dup := current[key]; !dup {
			current[key] = strings.TrimSpace(value)
		}
	}
	flush()
	if len(rows) == 0 {
		return nil, fmt.Errorf("file holds no attribute-value records")
	}
	return rows, nil
}

// avpRow turns one record's key/value pairs into a row object.
func avpRow(cfg Config, rec map[string]string) map[string]any {
	if len(cfg.Columns) == 0 {
		row := make(map[string]any, len(rec))
		for k, v := range rec {
			row[k] = v
		}
		return row
	}
	row := make(map[string]any, len(cfg.Columns))
	for _, col := range cfg.Columns {
		key := strings.TrimSpace(col.Header)
		if key == "" {
			key = col.Name
		}
		row[col.Name] = coerceCell(rec[key], col.Type)
	}
	return row
}

// renderCSV writes the rows as a delimited file, with a header row unless the layout
// says the file has none.
func renderCSV(cfg Config, cols []Column, rows []map[string]any) (string, error) {
	comma, err := delimiterRune(cfg.Delimiter)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	w.Comma = comma
	if cfg.hasHeader() {
		if err := w.Write(headerOf(cols)); err != nil {
			return "", fmt.Errorf("write header: %w", err)
		}
	}
	for _, row := range rows {
		rec := make([]string, len(cols))
		for i, col := range cols {
			rec[i] = cellText(row[col.Name])
		}
		if err := w.Write(rec); err != nil {
			return "", fmt.Errorf("write row: %w", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", fmt.Errorf("write CSV: %w", err)
	}
	return b.String(), nil
}

// renderFixedWidth pads each cell to its column's width.
//
// A value longer than its column is **truncated**, which is the one place this
// connector loses data on purpose: a fixed-width file has no way to express a wider
// field, so the alternatives are a corrupt file whose columns no longer line up, or
// refusing to write a record because a display name is long. Truncation is what every
// producer of these files does, and it is at least visible in the output.
func renderFixedWidth(cfg Config, cols []Column, rows []map[string]any) (string, error) {
	for _, c := range cols {
		if c.Width <= 0 {
			return "", fmt.Errorf("fixed-width column %q needs a positive width", c.Name)
		}
	}
	var b strings.Builder
	if cfg.hasHeader() {
		for _, col := range cols {
			b.WriteString(padTo(col.Name, col.Width))
		}
		b.WriteByte('\n')
	}
	for _, row := range rows {
		for _, col := range cols {
			b.WriteString(padTo(cellText(row[col.Name]), col.Width))
		}
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// padTo renders one fixed-width cell: padded with spaces, or cut to the width.
func padTo(s string, width int) string {
	r := []rune(s)
	if len(r) > width {
		return string(r[:width])
	}
	return string(r) + strings.Repeat(" ", width-len(r))
}

// renderAVP writes each row as "name: value" lines, records separated by a blank
// line. A field the row does not carry is omitted rather than written empty, because
// in this format an absent line and an empty value are different statements.
func renderAVP(cols []Column, rows []map[string]any) string {
	var b strings.Builder
	for i, row := range rows {
		if i > 0 {
			b.WriteByte('\n')
		}
		for _, col := range cols {
			v, ok := row[col.Name]
			if !ok {
				continue
			}
			b.WriteString(col.Name)
			b.WriteString(": ")
			b.WriteString(cellText(v))
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// headerOf returns the columns' output names.
func headerOf(cols []Column) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Name
	}
	return out
}

// cellText renders a row value as the text a file holds. A number keeps the form it
// arrived in rather than acquiring a float's exponent, and a missing field is empty.
func cellText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", t), "0"), ".")
	default:
		return fmt.Sprint(t)
	}
}

// textLines splits a file into lines, tolerating CRLF and a trailing newline, and
// strips a UTF-8 BOM as the CSV path does.
func textLines(data []byte) []string {
	data = bytes.TrimPrefix(data, []byte("\ufeff"))
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

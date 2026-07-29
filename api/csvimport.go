package api

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// csvColumn maps one CSV column into a field of the produced row object. The
// source column is located by Header (when the file has a header row) or by the
// 0-based Index (when it does not); the cell is coerced to Type. This is the
// "predefined column layout" a Quality Manager's upload is checked against
// (ADR-0084).
type csvColumn struct {
	Name   string `json:"name"`             // field name in the row object (required, unique)
	Header string `json:"header,omitempty"` // CSV header to read; defaults to Name when the file has a header row
	Index  *int   `json:"index,omitempty"`  // 0-based source column, used when the file has no header row
	Type   string `json:"type,omitempty"`   // "string" (default), "number", "integer", "boolean"
}

// csvConfig is the predefined column layout an uploaded CSV is parsed against.
type csvConfig struct {
	Columns   []csvColumn `json:"columns"`
	Delimiter string      `json:"delimiter,omitempty"` // single character; defaults to ","
	HasHeader *bool       `json:"hasHeader,omitempty"` // defaults to true (a header row is present)
}

// hasHeader reports whether the file carries a header row (the default).
func (c csvConfig) hasHeader() bool { return c.HasHeader == nil || *c.HasHeader }

// parseCSVRows parses CSV data against the predefined column layout into a list
// of row objects ({fieldName: value}). It is a pure, side-effect-free transform
// that runs in the API/side-effect phase, never on the processor hot path
// (ADR-0084, invariant 1/5).
//
// Type coercion is deliberately lenient: a cell that will not coerce to its
// declared type is kept as its raw string, so dirty records flow through to be
// validated and corrected rather than being rejected at ingestion — that is the
// point of the feature. A *structural* mismatch (a configured header absent from
// the file, an unparseable CSV, an invalid layout) is an error.
func parseCSVRows(cfg csvConfig, data []byte) ([]map[string]any, error) {
	if err := validateCSVConfig(cfg); err != nil {
		return nil, err
	}
	comma, err := delimiterRune(cfg.Delimiter)
	if err != nil {
		return nil, err
	}

	// Excel and many exporters prepend a UTF-8 BOM; left in place it corrupts the
	// first header cell (and thus every header lookup), so strip it up front.
	data = bytes.TrimPrefix(data, []byte("\ufeff"))
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("CSV is empty")
	}

	rdr := csv.NewReader(bytes.NewReader(data))
	rdr.Comma = comma
	rdr.FieldsPerRecord = -1 // dirty data may have ragged rows; index defensively
	rdr.LazyQuotes = true
	records, err := rdr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse CSV: %v", err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("CSV is empty")
	}

	// Resolve each configured column to a source position in a record.
	srcIndex := make([]int, len(cfg.Columns))
	dataStart := 0
	if cfg.hasHeader() {
		header := records[0]
		dataStart = 1
		pos := make(map[string]int, len(header))
		for i, h := range header {
			key := strings.TrimSpace(h)
			if _, seen := pos[key]; !seen { // first occurrence wins
				pos[key] = i
			}
		}
		for c, col := range cfg.Columns {
			want := strings.TrimSpace(col.Header)
			if want == "" {
				want = strings.TrimSpace(col.Name)
			}
			i, ok := pos[want]
			if !ok {
				return nil, fmt.Errorf("column %q (header %q) not found in CSV header", col.Name, want)
			}
			srcIndex[c] = i
		}
	} else {
		for c, col := range cfg.Columns {
			srcIndex[c] = *col.Index
		}
	}

	rows := make([]map[string]any, 0, len(records)-dataStart)
	for _, rec := range records[dataStart:] {
		row := make(map[string]any, len(cfg.Columns))
		for c, col := range cfg.Columns {
			cell := ""
			if i := srcIndex[c]; i >= 0 && i < len(rec) {
				cell = rec[i]
			}
			row[col.Name] = coerceCell(cell, col.Type)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// validateCSVConfig rejects a layout that could never map a file: no columns,
// blank/duplicate names, a headerless column without an index, or an unknown
// type. Type is checked here (not lazily per cell) so a bad layout fails even on
// a file that happens to contain no rows.
func validateCSVConfig(cfg csvConfig) error {
	if len(cfg.Columns) == 0 {
		return fmt.Errorf("column layout must declare at least one column")
	}
	seen := make(map[string]struct{}, len(cfg.Columns))
	for _, col := range cfg.Columns {
		name := strings.TrimSpace(col.Name)
		if name == "" {
			return fmt.Errorf("column name is required")
		}
		if _, dup := seen[name]; dup {
			return fmt.Errorf("duplicate column name %q", name)
		}
		seen[name] = struct{}{}
		switch col.Type {
		case "", "string", "number", "integer", "boolean":
		default:
			return fmt.Errorf("column %q: unsupported column type %q", name, col.Type)
		}
		if !cfg.hasHeader() {
			if col.Index == nil {
				return fmt.Errorf("column %q needs an index when the file has no header row", name)
			}
			if *col.Index < 0 {
				return fmt.Errorf("column %q: index must be >= 0", name)
			}
		}
	}
	return nil
}

// delimiterRune resolves the configured delimiter to a single rune, defaulting
// to a comma. A multi-character delimiter is a configuration error.
func delimiterRune(d string) (rune, error) {
	if d == "" {
		return ',', nil
	}
	r := []rune(d)
	if len(r) != 1 {
		return 0, fmt.Errorf("delimiter must be a single character, got %q", d)
	}
	return r[0], nil
}

// coerceCell converts a raw CSV cell to its declared type. Numeric cells become
// json.Number so their exact decimal text survives into FEEL (ADR-0037); a
// boolean parses the common truthy/falsy tokens (incl. German ja/nein,
// wahr/falsch). Anything that will not coerce falls back to the raw string.
func coerceCell(raw, typ string) any {
	switch typ {
	case "", "string":
		return raw
	case "number":
		t := strings.TrimSpace(raw)
		if _, err := strconv.ParseFloat(t, 64); err == nil {
			return json.Number(t)
		}
		return raw
	case "integer":
		t := strings.TrimSpace(raw)
		if _, err := strconv.ParseInt(t, 10, 64); err == nil {
			return json.Number(t)
		}
		return raw
	case "boolean":
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "true", "1", "yes", "ja", "wahr", "x":
			return true
		case "false", "0", "no", "nein", "falsch":
			return false
		default:
			return raw
		}
	default:
		return raw // validateCSVConfig already rejected unknown types
	}
}

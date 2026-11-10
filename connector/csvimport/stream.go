package csvimport

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
)

// Record is one record of a file read by [EachRow]: its line in the file, the cells
// exactly as written, and the row object the layout makes of them.
type Record struct {
	// Line is the record's 1-based line in the file, the number a person finds it by
	// in the editor they opened the file in. A quoted cell spanning lines gives the
	// line the record starts on.
	Line  int
	Cells []string
	Row   map[string]any
	// Header is the file's header row, the same for every record; nil for a file
	// without one.
	Header []string
}

// EachRow reads a CSV file one record at a time and calls fn with each, against the
// same layout [ParseRows] applies — so a file validated record by record and a file
// turned into rows cannot disagree about which cell is which field. It holds one
// record at a time rather than the whole file's rows: a file of a hundred thousand
// records is read as one, not held as a hundred thousand maps (ADR-0548).
//
// It returns the file's header row (nil for a file without one) so a caller writing a
// result can repeat it. An error from fn stops the read and is returned as it is. The
// layout is checked as ParseRows checks it, and a file with no records at all is an
// error, as it is there.
func EachRow(cfg Config, r io.Reader, fn func(Record) error) ([]string, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	comma, err := delimiterRune(cfg.Delimiter)
	if err != nil {
		return nil, err
	}
	br := bufio.NewReader(r)
	// Excel and many exporters prepend a UTF-8 BOM; left in place it corrupts the first
	// header cell, and with it every header lookup.
	if bom, _ := br.Peek(3); bytes.Equal(bom, []byte{0xEF, 0xBB, 0xBF}) {
		_, _ = br.Discard(3)
	}
	rdr := csv.NewReader(br)
	rdr.Comma = comma
	rdr.FieldsPerRecord = -1 // dirty data may have ragged rows; index defensively
	rdr.LazyQuotes = true
	rdr.ReuseRecord = true

	var header []string
	if cfg.hasHeader() {
		first, err := rdr.Read()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("CSV is empty")
		}
		if err != nil {
			return nil, fmt.Errorf("parse CSV: %v", err)
		}
		header = append([]string(nil), first...) // the next Read reuses first
	}
	cols, srcIndex, err := resolveColumns(cfg, header)
	if err != nil {
		return header, err
	}
	seen := false
	for {
		rec, err := rdr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return header, fmt.Errorf("parse CSV: %v", err)
		}
		seen = true
		line, _ := rdr.FieldPos(0)
		if err := fn(Record{Line: line, Cells: rec, Row: rowOf(cols, srcIndex, rec), Header: header}); err != nil {
			return header, err
		}
	}
	if !seen && header == nil {
		return nil, fmt.Errorf("CSV is empty")
	}
	return header, nil
}

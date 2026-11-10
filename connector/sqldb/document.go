package sqldb

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/pblumer/atlas/connector/csvdoc"
	"github.com/pblumer/atlas/connector/nettimeout"
)

// A query written as a document (docs/planning/ad-search-as-document.md, S3).
//
// A query normally writes its rows into the result variable, which a variable's budget
// bounds at a few thousand. Checking a whole table is a different question with a
// different size, so the rows go into a CSV document instead, one row per row of the
// result, its columns the statement's in their order. The writer is the directories'
// (connector/csvdoc), so a business rule task over a file reads a table the way it
// reads a directory.

// DocumentStore is where a query written as a document stores its file.
type DocumentStore = csvdoc.Store

// RunWithDocuments is [Run] with a document store, for a query written as a document.
// Every other operation, and a query into a variable, is exactly [Run].
func RunWithDocuments(ctx context.Context, j Job, reg *Registry, docs DocumentStore) (map[string]any, error) {
	if !j.AsDocument {
		return Run(ctx, j, reg)
	}
	if j.Operation != "query" {
		return nil, fmt.Errorf("sqldb: only a query writes its result as a document, not %s", j.Operation)
	}
	if docs == nil {
		return nil, fmt.Errorf("sqldb: this query writes its result as a document, and where it runs there is no document store to write it to")
	}
	client, args, err := prepare(j, reg)
	if err != nil {
		return nil, err
	}
	var w *csvdoc.Writer
	var row []string
	err = client.QueryEach(ctx, j.Statement, args, func(cols []string, cells []any) error {
		if w == nil {
			w = csvdoc.NewWriter(cols, csvdoc.Bounds{
				What: "sqldb: the query", Unit: "row", Units: "rows",
				Limit: csvdoc.Limit(j.DocumentLimit, docs), Max: j.MaxRows,
			})
		}
		if cells == nil {
			return nil // the header alone: a result with no rows
		}
		row = row[:0]
		for _, c := range cells {
			row = append(row, cellText(c))
		}
		if err := w.Write(row); err != nil {
			return err
		}
		return ctx.Err()
	})
	if err != nil {
		return nil, err
	}
	name := j.DocumentName
	if name == "" {
		name = "query.csv"
	}
	ref, err := w.Store(ctx, docs, name)
	if err != nil {
		return nil, fmt.Errorf("sqldb: %w", err)
	}
	return result(j, csvdoc.Summary(w.Rows(), ref)), nil
}

// cellText is a value as a CSV cell. NULL is an empty cell, as a missing attribute is
// in a directory's document: a CSV has no other way to say it, so a rule that must tell
// NULL from an empty string needs the statement to say so (COALESCE, a flag column).
func cellText(v any) string {
	switch t := normalize(v).(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprint(t)
		}
		return string(b)
	}
}

// QueryEach runs a row-returning statement and hands each row to fn as it is read,
// with the result's columns in their order, so a large result is never held whole.
// fn is called once with nil cells before the first row, so a result with no rows
// still knows its columns. An error from fn stops the read and is returned as it is.
func (c *Client) QueryEach(ctx context.Context, stmt string, args []any, fn func(cols []string, cells []any) error) error {
	ctx, cancel := context.WithTimeout(ctx, nettimeout.Default)
	defer cancel()

	rows, err := c.db.QueryContext(ctx, stmt, args...)
	if err != nil {
		return fmt.Errorf("sqldb: query: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return fmt.Errorf("sqldb: columns: %w", err)
	}
	if err := fn(cols, nil); err != nil {
		return err
	}
	cells := make([]any, len(cols))
	into := make([]any, len(cols))
	for i := range cells {
		into[i] = &cells[i]
	}
	for rows.Next() {
		if err := rows.Scan(into...); err != nil {
			return fmt.Errorf("sqldb: scan: %w", err)
		}
		if err := fn(cols, cells); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sqldb: reading rows: %w", err)
	}
	return nil
}

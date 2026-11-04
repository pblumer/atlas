package api

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// The instance listing and the instance search take ?values=preview, which cuts every
// long variable value to its beginning.
//
// The Operations overview polls a page of instances every 1.5 seconds, up to fifty of
// each half, and every row carried every variable whole. A process that keeps an
// uploaded CSV file in one string (a variable may hold up to limits.Variable, 1 MiB by
// default) made each poll carry that file once per listed instance, to a card that
// shows one line of it. With the parameter, the poll carries the line and what the
// rest weighs; the whole value is one point read away (GET
// /api/v1/instances/search?q=<key>) when somebody opens it.
//
// It is opt-in, and the default stays the whole value: other readers of the listing —
// scripts, the MCP tools, the instances page — were written against whole values, and
// a value quietly cut is a wrong answer that looks right.
//
// What it saves is transport, encoding and the browser's work. The server still reads
// each value whole from the store to cut it, so the scan costs what it did.

// variablePreviewBytes is how much of a long value a preview keeps. Enough for the
// first lines of a text in the single-instance view, a fraction of a large value.
const variablePreviewBytes = 512

// parseValuesMode reads ?values=: "" or "full" is the whole value, "preview" the cut
// one. Anything else is refused rather than ignored, so a caller who misspelt it does
// not believe it is reading previews while receiving whole files.
func parseValuesMode(raw string) (bool, error) {
	switch strings.TrimSpace(raw) {
	case "", "full":
		return false, nil
	case "preview":
		return true, nil
	default:
		return false, errors.New(`invalid values (want "full" or "preview")`)
	}
}

// previewVariableView cuts a value longer than variablePreviewBytes to at most that
// many bytes, on a character boundary, and records what the whole value is.
func previewVariableView(v variableView) variableView {
	if len(v.Value) <= variablePreviewBytes {
		return v
	}
	whole := v.Value
	cut := variablePreviewBytes
	for cut > 0 && !utf8.RuneStart(whole[cut]) {
		cut--
	}
	v.Value = whole[:cut]
	v.Truncated = true
	v.Size = len(whole)
	if v.Kind == "json" {
		v.Shape, v.Count = jsonShape(whole)
	} else {
		v.Lines = strings.Count(whole, "\n") + 1
	}
	return v
}

// previewInstanceRows applies previewVariableView to every variable of every row. An
// archived row is left whole: it is the exported event log's answer for an instance
// this server no longer holds, so there is no point read to fetch the rest from.
func previewInstanceRows(rows []instanceResp) {
	for i := range rows {
		if rows[i].Archived {
			continue
		}
		for j := range rows[i].Variables {
			rows[i].Variables[j] = previewVariableView(rows[i].Variables[j])
		}
	}
}

// jsonShape says whether a JSON text is an array or an object and how many elements or
// members it holds at the top level, so a preview of a cut structure can still say
// "15 items". It counts the separators at depth one in a single pass, skipping strings,
// rather than decoding the value: the preview exists because values can be large. A
// scalar or empty text has no shape.
func jsonShape(text string) (string, int) {
	s := strings.TrimSpace(text)
	if s == "" {
		return "", 0
	}
	var shape string
	switch s[0] {
	case '[':
		shape = "array"
	case '{':
		shape = "object"
	default:
		return "", 0
	}
	depth, count := 0, 0
	inString, escaped, seen := false, false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
			if depth == 1 {
				seen = true
			}
		case '[', '{':
			if depth == 1 {
				seen = true
			}
			depth++
		case ']', '}':
			depth--
		case ',':
			if depth == 1 {
				count++
			}
		case ' ', '\t', '\n', '\r':
		default:
			if depth == 1 {
				seen = true
			}
		}
	}
	if seen {
		count++
	}
	return shape, count
}

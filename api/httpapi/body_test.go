package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestABodyIsReadUpToItsLimit pins the three functions every handler reads a body
// through: a body within the limit is read whole, one past it is an error that
// BodyTooLarge recognises rather than a silent prefix, and an empty body is still
// io.EOF, which the routes with an optional body rely on.
func TestABodyIsReadUpToItsLimit(t *testing.T) {
	post := func(body string) *http.Request {
		return httptest.NewRequest("POST", "/x", strings.NewReader(body))
	}

	var dst struct {
		Name string `json:"name"`
	}
	if err := DecodeJSON(httptest.NewRecorder(), post(`{"name":"a"}`), 64, &dst); err != nil || dst.Name != "a" {
		t.Errorf("a body within the limit: %v, decoded %+v", err, dst)
	}
	if err := DecodeJSON(httptest.NewRecorder(), post(""), 64, &dst); err != io.EOF {
		t.Errorf("an empty body = %v, want io.EOF", err)
	}
	long := `{"name":"` + strings.Repeat("a", 100) + `"}`
	if err := DecodeJSON(httptest.NewRecorder(), post(long), 64, &dst); !BodyTooLarge(err) {
		t.Errorf("a body past the limit = %v, want it recognised as too large", err)
	}
	if err := DecodeJSON(httptest.NewRecorder(), post(`{"name":`), 64, &dst); err == nil || BodyTooLarge(err) {
		t.Errorf("a malformed body = %v, want a decoding error that is not about size", err)
	}

	if got, err := ReadBody(httptest.NewRecorder(), post("12345"), 5); err != nil || string(got) != "12345" {
		t.Errorf("a body of exactly the limit: %q, %v", got, err)
	}
	if got, err := ReadBody(httptest.NewRecorder(), post("123456"), 5); !BodyTooLarge(err) {
		t.Errorf("a body one byte past the limit: %q, %v; want it refused, not cut", got, err)
	}
	if BodyTooLarge(nil) || BodyTooLarge(io.ErrUnexpectedEOF) {
		t.Error("an error that is not about size was recognised as too large")
	}
}

// TestBodyErrorSaysTooLargeOnce: the status and the sentence for a body past its
// budget are the same on every route, and anything else keeps the handler's words.
func TestBodyErrorSaysTooLargeOnce(t *testing.T) {
	_, tooLarge := ReadBody(httptest.NewRecorder(), httptest.NewRequest("POST", "/x", strings.NewReader("123456")), 5)

	rec := httptest.NewRecorder()
	BodyError(rec, tooLarge, "malformed JSON body")
	if rec.Code != http.StatusRequestEntityTooLarge || strings.Contains(rec.Body.String(), "malformed") {
		t.Errorf("a body past its budget = %d %s, want 413 in its own words", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	BodyError(rec, io.ErrUnexpectedEOF, "malformed JSON body")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "malformed JSON body") {
		t.Errorf("a body that could not be decoded = %d %s, want 400 with the handler's message", rec.Code, rec.Body)
	}
}

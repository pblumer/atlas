// Package soap integrates a SOAP / Web Services (WSDL) endpoint as a service-task
// connector: a BPMN SOAP connector task invokes an operation — reading identities
// (import) or provisioning accounts (outbound) — against a model-authored web-service
// endpoint through the job path (ADR-0165), the same seam the rest package uses for a
// generic HTTP call (ADR-0067). It inherits the job protocol's durability and
// non-blocking properties (ADR-0007):
//
//   - A SOAP connector task creates a job carrying the reserved
//     [compiler.SoapJobType]. The processor never performs the outbound call itself,
//     so it stays allocation-free (invariant I1) and free of any HTTP/XML dependency.
//   - The in-process [Handler] — a job worker — pulls those jobs, wraps the authored
//     body in a SOAP envelope and calls the web service off the processor goroutine and
//     after fsync (invariant I2, never inside applyToState / I4), parses the response
//     into the task's result variable, and completes the job, which drives the token
//     onward.
//
// Like REST, a SOAP task authors its endpoint, operation, and request body in the
// model; credentials are never authored there — authentication (basic/bearer/apiKey)
// names a server-side secret the worker resolves at runtime (ADR-0041/0067), so a
// credential never appears in a BPMN file. The connector is deliberately generic — a
// WSDL-based client for legacy SOAP services: it does not bind a WSDL at deploy time
// but wraps the model-authored operation payload in a SOAP 1.1 or 1.2 envelope,
// carries the SOAPAction, and turns a SOAP Fault (faultcode/faultstring, or the 1.2
// Code/Reason) into the job-failure message so a parked incident is legible.
//
// Delivery is at-least-once (a crash between "the service accepted the call" and "job
// completed" replays the request); a provisioning operation should therefore be
// idempotent on the service side, or correlate on a business key carried in the body,
// exactly as any other connector's retried call must (ADR-0007/0149).
package soap

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/connector/nettimeout"
)

// Request is one SOAP call a connector task makes. Endpoint is the resolved service
// URL; Operation is the operation name (used in diagnostics); Action is the SOAPAction
// (a header for 1.1, a Content-Type parameter for 1.2); Version is "1.1" or "1.2";
// Body is the XML payload placed inside the envelope's <soap:Body> (the operation's
// request element); Headers are set on the request (including any Authorization/api-key
// header the worker resolved from a secret).
type Request struct {
	Endpoint  string
	Operation string
	Action    string
	Version   string
	Body      string
	Headers   map[string]string
}

// Response is a SOAP call's outcome. Status is the HTTP status code; Body is the
// parsed content of the SOAP <Body> element — a map of its child elements (repeated
// elements become arrays, leaf elements their trimmed text), or nil for an empty body.
type Response struct {
	Status int
	Body   any
}

// Client calls a SOAP web service. It is an interface so the worker is testable
// without a live server.
type Client interface {
	Do(ctx context.Context, r Request) (Response, error)
}

// HTTPClient calls a real SOAP web service over HTTP. It POSTs the SOAP envelope with
// the version-appropriate Content-Type and SOAPAction, and parses the response
// envelope's Body. A SOAP Fault or a non-2xx status without a parseable body is
// returned as an error carrying the fault detail so the job stays pending and is
// retried (at-least-once).
type HTTPClient struct {
	http *http.Client
}

// NewHTTPClient builds a SOAP HTTP client bounded by the shared connector call budget
// (nettimeout.HTTPClient), like the REST connector: the worker runs on the run-loop
// goroutine, so an unbounded call would stall the whole engine (ADR-0149).
func NewHTTPClient() *HTTPClient {
	return &HTTPClient{http: nettimeout.HTTPClient()}
}

// SOAP envelope namespaces: 1.1 (SOAP/1.1 note) and 1.2 (W3C Rec).
const (
	soap11NS = "http://schemas.xmlsoap.org/soap/envelope/"
	soap12NS = "http://www.w3.org/2003/05/soap-envelope"
)

func (c *HTTPClient) Do(ctx context.Context, r Request) (Response, error) {
	envelope := buildEnvelope(r.Version, r.Body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.Endpoint, strings.NewReader(envelope))
	if err != nil {
		return Response{}, fmt.Errorf("soap: build request: %w", err)
	}
	// Model-resolved headers first (an auth header), then the transport headers the
	// SOAP binding requires — Content-Type and, for 1.1, SOAPAction — which a task must
	// not override.
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	applyContentType(req, r.Version, r.Action)
	resp, err := c.http.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("soap: call %s %s: %w", r.Operation, r.Endpoint, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("soap: read response from %s: %w", r.Endpoint, err)
	}
	body, fault, perr := parseResponse(raw)
	if perr != nil {
		// A non-2xx with an unparseable body is a transport-level error (a proxy page,
		// an HTML 500); surface the status and a snippet rather than the XML parse error.
		if resp.StatusCode/100 != 2 {
			return Response{}, fmt.Errorf("soap: %s %s returned HTTP %d: %s", r.Operation, r.Endpoint, resp.StatusCode, snippet(raw))
		}
		return Response{}, fmt.Errorf("soap: parse response from %s: %w", r.Endpoint, perr)
	}
	if fault != "" {
		// A SOAP fault rides on HTTP 500 (1.1) or 200 (1.2); either way the operation
		// failed, so the job fails and retries (at-least-once) toward an incident.
		return Response{}, fmt.Errorf("soap: %s %s returned a SOAP fault: %s", r.Operation, r.Endpoint, fault)
	}
	if resp.StatusCode/100 != 2 {
		return Response{}, fmt.Errorf("soap: %s %s returned HTTP %d", r.Operation, r.Endpoint, resp.StatusCode)
	}
	return Response{Status: resp.StatusCode, Body: body}, nil
}

// buildEnvelope wraps the model-authored payload in a SOAP envelope for the given
// version. The payload carries its own namespaces (it is the operation's request
// element authored in the model), so the envelope only supplies the SOAP namespace.
func buildEnvelope(version, payload string) string {
	ns := soap11NS
	if version == "1.2" {
		ns = soap12NS
	}
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<soapenv:Envelope xmlns:soapenv="` + ns + `">` +
		`<soapenv:Body>` + payload + `</soapenv:Body>` +
		`</soapenv:Envelope>`
}

// applyContentType sets the transport headers a SOAP binding requires. SOAP 1.1 sends
// text/xml with a separate (quoted) SOAPAction header — required even when empty; SOAP
// 1.2 sends application/soap+xml and carries the action as a Content-Type parameter.
func applyContentType(req *http.Request, version, action string) {
	if version == "1.2" {
		ct := "application/soap+xml; charset=utf-8"
		if action != "" {
			ct += `; action="` + action + `"`
		}
		req.Header.Set("Content-Type", ct)
		return
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", `"`+action+`"`)
}

// parseResponse parses a SOAP response envelope: it walks to the <Body> element that is
// a direct child of <Envelope>, decodes its content into a generic value, and reports a
// SOAP Fault (1.1 or 1.2) as a human-readable string so a failed job's incident names
// the service's own reason. It returns (bodyContent, "", nil) for a normal response,
// (nil, faultMessage, nil) for a fault, and a non-nil error only when the payload is
// not a parseable SOAP envelope.
func parseResponse(raw []byte) (body any, fault string, err error) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var stack []string
	for {
		tok, terr := dec.Token()
		if terr == io.EOF {
			break
		}
		if terr != nil {
			return nil, "", terr
		}
		switch t := tok.(type) {
		case xml.StartElement:
			// The SOAP Body is a direct child of the Envelope; matching it at that depth
			// avoids mistaking a payload element that happens to be named "Body".
			if t.Name.Local == "Body" && len(stack) == 1 && stack[0] == "Envelope" {
				return decodeBody(dec, t)
			}
			stack = append(stack, t.Name.Local)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return nil, "", fmt.Errorf("soap: response is not a SOAP envelope (no <Body>)")
}

// decodeBody decodes the children of the SOAP <Body> element into a map keyed by each
// child's local element name (repeated names become arrays). A child named "Fault" is
// not data: it is decoded and turned into a fault message instead.
func decodeBody(dec *xml.Decoder, body xml.StartElement) (any, string, error) {
	result := map[string]any{}
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, "", err // io.EOF here means the Body was never closed (malformed)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			val, err := decodeElement(dec, t)
			if err != nil {
				return nil, "", err
			}
			if t.Name.Local == "Fault" {
				return nil, faultMessage(val), nil
			}
			addChild(result, t.Name.Local, val)
		case xml.EndElement:
			if t.Name.Local == body.Name.Local {
				return result, "", nil
			}
		}
	}
}

// decodeElement decodes one XML element (whose start token has already been consumed)
// into a generic value: an element with child elements becomes a map keyed by local
// name (repeated children become arrays), a leaf element becomes its trimmed text.
// Namespaces/prefixes are reduced to local names and attributes are not mapped — legacy
// SOAP responses carry their data in child elements, which is what an import reads back
// (ADR-0165); attribute mapping is a documented follow-up.
func decodeElement(dec *xml.Decoder, start xml.StartElement) (any, error) {
	children := map[string]any{}
	var text strings.Builder
	hasChild := false
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			hasChild = true
			val, err := decodeElement(dec, t)
			if err != nil {
				return nil, err
			}
			addChild(children, t.Name.Local, val)
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			if hasChild {
				return children, nil
			}
			return strings.TrimSpace(text.String()), nil
		}
	}
}

// addChild adds a decoded child value under its element name, promoting a name that
// appears more than once to an array so a repeated element (a list of entries) round-
// trips as a JSON array rather than silently keeping only the last one.
func addChild(m map[string]any, name string, val any) {
	existing, ok := m[name]
	if !ok {
		m[name] = val
		return
	}
	if arr, ok := existing.([]any); ok {
		m[name] = append(arr, val)
		return
	}
	m[name] = []any{existing, val}
}

// faultMessage builds a human-readable message from a decoded SOAP Fault element,
// handling both 1.1 (faultcode/faultstring) and 1.2 (Code/Value, Reason/Text). It never
// returns the empty string, so the worker always recognizes a fault as a failure even
// when the service left the detail blank.
func faultMessage(val any) string {
	m, ok := val.(map[string]any)
	if !ok {
		if s := asString(val); s != "" {
			return s
		}
		return "unknown fault"
	}
	code := asString(m["faultcode"]) // SOAP 1.1
	reason := asString(m["faultstring"])
	if code == "" { // SOAP 1.2
		code = asString(nested(m, "Code", "Value"))
	}
	if reason == "" {
		reason = asString(nested(m, "Reason", "Text"))
	}
	switch {
	case code != "" && reason != "":
		return code + ": " + reason
	case reason != "":
		return reason
	case code != "":
		return code
	default:
		return "unknown fault"
	}
}

// asString returns v as a trimmed string when it is one, else "".
func asString(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// nested walks a chain of map keys, returning nil as soon as a level is absent or not a
// map — the shape of a SOAP 1.2 fault's Code/Value and Reason/Text.
func nested(m map[string]any, keys ...string) any {
	var cur any = m
	for _, k := range keys {
		cm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = cm[k]
	}
	return cur
}

// snippet trims a raw response body to a short, single-line excerpt for an error
// message about an unparseable (non-SOAP) response.
func snippet(raw []byte) string {
	s := strings.Join(strings.Fields(string(raw)), " ")
	const max = 200
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

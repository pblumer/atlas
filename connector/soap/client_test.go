package soap

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHTTPClientCallsReferenceService is the integration slice: the client POSTs a SOAP
// 1.1 envelope to a reference web service (an httptest server), which checks the
// transport headers and the wrapped payload and answers with a response envelope; the
// client parses the Body back into a nested structure.
func TestHTTPClientCallsReferenceService(t *testing.T) {
	var gotCT, gotAction, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		gotAction = r.Header.Get("SOAPAction")
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.Header().Set("Content-Type", "text/xml")
		io.WriteString(w, `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/">
		  <soapenv:Body>
		    <GetUserResponse xmlns="urn:x"><user><id>7</id><name>ada</name></user></GetUserResponse>
		  </soapenv:Body>
		</soapenv:Envelope>`)
	}))
	defer srv.Close()

	resp, err := NewHTTPClient().Do(context.Background(), Request{
		Endpoint:  srv.URL,
		Operation: "GetUser",
		Action:    "urn:GetUser",
		Version:   "1.1",
		Body:      `<tns:GetUser xmlns:tns="urn:x"><id>7</id></tns:GetUser>`,
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if !strings.HasPrefix(gotCT, "text/xml") {
		t.Errorf("Content-Type = %q, want text/xml for SOAP 1.1", gotCT)
	}
	if gotAction != `"urn:GetUser"` {
		t.Errorf("SOAPAction = %q, want the quoted action", gotAction)
	}
	if !strings.Contains(gotBody, "<soapenv:Body>") || !strings.Contains(gotBody, "<tns:GetUser") {
		t.Errorf("request envelope = %q, want the payload wrapped in a SOAP Body", gotBody)
	}
	body, ok := resp.Body.(map[string]any)
	if !ok {
		t.Fatalf("response body = %#v, want a map", resp.Body)
	}
	user := nested(body, "GetUserResponse", "user")
	um, ok := user.(map[string]any)
	if !ok || um["id"] != "7" || um["name"] != "ada" {
		t.Errorf("decoded user = %#v, want {id:7, name:ada}", user)
	}
}

// TestHTTPClient12ContentType proves SOAP 1.2 carries the action inside the
// application/soap+xml Content-Type and sends no separate SOAPAction header.
func TestHTTPClient12ContentType(t *testing.T) {
	var gotCT, gotAction string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		gotAction = r.Header.Get("SOAPAction")
		io.WriteString(w, `<e:Envelope xmlns:e="http://www.w3.org/2003/05/soap-envelope"><e:Body><Ok/></e:Body></e:Envelope>`)
	}))
	defer srv.Close()

	if _, err := NewHTTPClient().Do(context.Background(), Request{
		Endpoint: srv.URL, Operation: "Ping", Action: "urn:Ping", Version: "1.2", Body: "<Ping/>",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if !strings.Contains(gotCT, "application/soap+xml") || !strings.Contains(gotCT, `action="urn:Ping"`) {
		t.Errorf("Content-Type = %q, want application/soap+xml with the action parameter", gotCT)
	}
	if gotAction != "" {
		t.Errorf("SOAPAction header = %q, want none for SOAP 1.2", gotAction)
	}
}

// TestHTTPClientSoap11Fault proves a SOAP 1.1 fault (HTTP 500 + Fault body) fails the
// call with the faultstring in the message, not an opaque status.
func TestHTTPClientSoap11Fault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/">
		  <soapenv:Body><soapenv:Fault>
		    <faultcode>soapenv:Client</faultcode><faultstring>Invalid user id</faultstring>
		  </soapenv:Fault></soapenv:Body></soapenv:Envelope>`)
	}))
	defer srv.Close()

	_, err := NewHTTPClient().Do(context.Background(), Request{Endpoint: srv.URL, Operation: "GetUser", Version: "1.1", Body: "<x/>"})
	if err == nil || !strings.Contains(err.Error(), "Invalid user id") {
		t.Fatalf("Do error = %v, want a SOAP fault carrying the faultstring", err)
	}
	if !strings.Contains(err.Error(), "soapenv:Client") {
		t.Errorf("Do error = %v, want the faultcode too", err)
	}
}

// TestHTTPClientSoap12Fault proves a SOAP 1.2 fault (Code/Value + Reason/Text, on HTTP
// 200) is recognized and reported.
func TestHTTPClientSoap12Fault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `<e:Envelope xmlns:e="http://www.w3.org/2003/05/soap-envelope"><e:Body><e:Fault>
		  <e:Code><e:Value>e:Sender</e:Value></e:Code>
		  <e:Reason><e:Text xml:lang="en">bad request</e:Text></e:Reason>
		</e:Fault></e:Body></e:Envelope>`)
	}))
	defer srv.Close()

	_, err := NewHTTPClient().Do(context.Background(), Request{Endpoint: srv.URL, Operation: "Op", Version: "1.2", Body: "<x/>"})
	if err == nil || !strings.Contains(err.Error(), "bad request") || !strings.Contains(err.Error(), "e:Sender") {
		t.Fatalf("Do error = %v, want the 1.2 Reason and Code", err)
	}
}

// TestHTTPClientHTTPErrorNoEnvelope proves a non-2xx response that is not a SOAP
// envelope (a plain proxy error) surfaces the status and a snippet, not an XML parse
// error.
func TestHTTPClientHTTPErrorNoEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "502 upstream down")
	}))
	defer srv.Close()

	_, err := NewHTTPClient().Do(context.Background(), Request{Endpoint: srv.URL, Operation: "Op", Version: "1.1", Body: "<x/>"})
	if err == nil || !strings.Contains(err.Error(), "502") || !strings.Contains(err.Error(), "upstream down") {
		t.Fatalf("Do error = %v, want the HTTP status and body snippet", err)
	}
}

// TestHTTPClientNon2xxValidEnvelope proves a non-2xx status carrying a valid, fault-free
// envelope still fails the call — the operation did not succeed even though the body
// parsed.
func TestHTTPClientNon2xxValidEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><Down/></soapenv:Body></soapenv:Envelope>`)
	}))
	defer srv.Close()

	_, err := NewHTTPClient().Do(context.Background(), Request{Endpoint: srv.URL, Operation: "Op", Version: "1.1", Body: "<x/>"})
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("Do error = %v, want the non-2xx status even with a valid envelope", err)
	}
}

// TestHTTPClientUnparseableSuccess proves a 2xx response whose body is not a SOAP
// envelope fails the call rather than returning a bogus result.
func TestHTTPClientUnparseableSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "not xml at all")
	}))
	defer srv.Close()

	if _, err := NewHTTPClient().Do(context.Background(), Request{Endpoint: srv.URL, Operation: "Op", Version: "1.1", Body: "<x/>"}); err == nil {
		t.Fatal("Do error = nil, want a parse failure for a non-envelope success body")
	}
}

// TestHTTPClientTransportError proves a dial/transport failure (an unroutable endpoint)
// is returned as an error so the job retries.
func TestHTTPClientTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now

	if _, err := NewHTTPClient().Do(context.Background(), Request{Endpoint: url, Operation: "Op", Version: "1.1", Body: "<x/>"}); err == nil {
		t.Fatal("Do error = nil, want a transport error against a closed server")
	}
}

// TestHTTPClientInvalidEndpoint proves a malformed endpoint (e.g. a FEEL expression that
// built garbage) fails the call at request construction rather than panicking.
func TestHTTPClientInvalidEndpoint(t *testing.T) {
	if _, err := NewHTTPClient().Do(context.Background(), Request{Endpoint: "http://%zz", Operation: "Op", Version: "1.1", Body: "<x/>"}); err == nil {
		t.Fatal("Do error = nil for a malformed endpoint, want a build-request error")
	}
}

// TestParseResponseRepeatedElements proves a repeated child element decodes to an array,
// so a list of imported entries round-trips rather than keeping only the last.
func TestParseResponseRepeatedElements(t *testing.T) {
	raw := []byte(`<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/">
	  <soapenv:Body><ListResponse><entry>a</entry><entry>b</entry></ListResponse></soapenv:Body>
	</soapenv:Envelope>`)
	body, fault, err := parseResponse(raw)
	if err != nil || fault != "" {
		t.Fatalf("parseResponse: body=%v fault=%q err=%v", body, fault, err)
	}
	list := nested(body.(map[string]any), "ListResponse", "entry")
	arr, ok := list.([]any)
	if !ok || len(arr) != 2 || arr[0] != "a" || arr[1] != "b" {
		t.Errorf("entries = %#v, want the array [a b]", list)
	}
}

// TestParseResponseNoBody proves a well-formed XML document that is not a SOAP envelope
// is reported as unparseable rather than silently returning nothing.
func TestParseResponseNoBody(t *testing.T) {
	if _, _, err := parseResponse([]byte(`<html><body>hi</body></html>`)); err == nil {
		t.Error("parseResponse of a non-envelope document = nil error, want a failure")
	}
}

// TestFaultMessageFallback proves an empty Fault still yields a non-empty message, so the
// worker always treats a fault as a failure.
func TestFaultMessageFallback(t *testing.T) {
	if got := faultMessage(map[string]any{}); got == "" {
		t.Error("faultMessage of an empty fault = empty, want a non-empty fallback")
	}
	if got := faultMessage("boom"); got != "boom" {
		t.Errorf("faultMessage of a leaf fault = %q, want boom", got)
	}
}

// TestBuildEnvelopeNamespaces proves each version wraps the payload with its own SOAP
// namespace.
func TestBuildEnvelopeNamespaces(t *testing.T) {
	if e := buildEnvelope("1.1", "<x/>"); !strings.Contains(e, soap11NS) || !strings.Contains(e, "<x/>") {
		t.Errorf("1.1 envelope = %q, want the 1.1 namespace and payload", e)
	}
	if e := buildEnvelope("1.2", "<x/>"); !strings.Contains(e, soap12NS) {
		t.Errorf("1.2 envelope = %q, want the 1.2 namespace", e)
	}
}

package entra

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Reading a person's photo (ADR-draft-directory-photo).
//
// Every other operation this worker performs returns JSON. A photo does not, and
// the three things that follow from that are what these hold: the bytes come back
// as bytes, a person without one is an answer rather than a failure, and an
// over-large body is refused rather than cut short.

// aJPEGBody is bytes that are genuinely a JPEG — the magic matters, because what
// receives this checks the content and not the header.
var aJPEGBody = "\xff\xd8\xff" + strings.Repeat("p", 40)

// fakeGraph stands in for Graph on one route and remembers what was asked of it.
type fakeGraph struct {
	client *GraphClient
	// accept is the Accept header of the last request, which is where a binary
	// request becomes observable from the far side.
	accept string
}

func photoServer(t *testing.T, status int, contentType, body string) *fakeGraph {
	t.Helper()
	g := &fakeGraph{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/photo/$value") {
			t.Errorf("asked for %q, want a photo route", r.URL.Path)
		}
		g.accept = r.Header.Get("Accept")
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	g.client = NewGraphClient(staticToken{tok: "t"}, srv.URL, http.DefaultClient)
	return g
}

func photoJob() Job {
	return Job{Connector: "contoso", Operation: "get-user-photo", UserID: "u1", ResultVariable: "foto"}
}

// TestAPhotoArrivesAsSomethingAProcessVariableCanHold.
//
// Base64 and a content type, because a process variable is FEEL and FEEL has no
// bytes — and because nothing downstream can store an image it cannot name.
func TestAPhotoArrivesAsSomethingAProcessVariableCanHold(t *testing.T) {
	g := photoServer(t, http.StatusOK, "image/jpeg", aJPEGBody)
	out, err := Run(context.Background(), photoJob(), regWith("contoso", g.client))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, ok := out["foto"].(map[string]any)
	if !ok {
		t.Fatalf("result = %#v, want an object naming the picture", out["foto"])
	}
	if got["contentType"] != "image/jpeg" {
		t.Errorf("contentType = %v", got["contentType"])
	}
	data, _ := got["data"].(string)
	raw, decErr := base64.StdEncoding.DecodeString(data)
	if decErr != nil {
		t.Fatalf("the data is not base64: %v", decErr)
	}
	if string(raw) != aJPEGBody {
		t.Errorf("the bytes came back changed: %q", raw)
	}
	// A request that asks for bytes must not ask for JSON, or a proxy or a gateway
	// is entitled to answer with something else entirely.
	if g.accept != "*/*" {
		t.Errorf("Accept = %q, want */* on a binary read", g.accept)
	}
	// The parameters Graph may append are dropped, or the type stored beside the
	// image would be one no browser was told about.
	g2 := photoServer(t, http.StatusOK, "image/jpeg; charset=binary", aJPEGBody)
	out2, err := Run(context.Background(), photoJob(), regWith("contoso", g2.client))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if m, _ := out2["foto"].(map[string]any); m["contentType"] != "image/jpeg" {
		t.Errorf("contentType = %v, want the bare media type", m["contentType"])
	}
}

// TestAPersonWithoutAPhotoIsAnAnswer.
//
// The one place in this worker where a non-2xx is not a failure, and it is
// deliberate: Graph answers 404 both for a person who has no photo and for an id
// that is not anybody's. A tenant where most people have none would otherwise
// produce an incident per person — an outcome far worse than the wrong answer this
// trades for, which is that a mistyped id reads as "no photo".
//
// The result is nil rather than an empty object, so a model asks whether there is
// a picture instead of comparing an empty string.
func TestAPersonWithoutAPhotoIsAnAnswer(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		body   string
	}{
		{"no photo", http.StatusNotFound, `{"error":{"code":"ImageNotFound","message":"no photo"}}`},
		{"a 2xx carrying nothing", http.StatusOK, ""},
	} {
		out, err := Run(context.Background(), photoJob(), regWith("contoso", photoServer(t, c.status, "", c.body).client))
		if err != nil {
			t.Fatalf("%s: Run: %v", c.name, err)
		}
		if out["foto"] != nil {
			t.Errorf("%s: result = %#v, want nothing", c.name, out["foto"])
		}
	}
}

// TestAFailedPhotoReadIsStillAFailure.
//
// The absence above must stay confined to 404. A tenant that refuses the
// permission answers 403, and reading that as "this person has no photo" would
// mirror an empty directory over everybody's face in silence.
func TestAFailedPhotoReadIsStillAFailure(t *testing.T) {
	g := photoServer(t, http.StatusForbidden,
		"application/json", `{"error":{"code":"Authorization_RequestDenied","message":"Insufficient privileges"}}`)
	_, err := Run(context.Background(), photoJob(), regWith("contoso", g.client))
	if err == nil {
		t.Fatal("a refused photo read succeeded")
	}
	if !strings.Contains(err.Error(), "Authorization_RequestDenied") {
		t.Errorf("err = %v, want Graph's own code in it", err)
	}
}

// TestAPhotoPastTheLimitIsRefusedRatherThanCutShort.
//
// Half a JPEG is not a smaller JPEG. The magic is at the front, so a truncated one
// passes every format check there is and lands as a broken image nobody can
// explain — which is why this refuses instead of taking what fits.
func TestAPhotoPastTheLimitIsRefusedRatherThanCutShort(t *testing.T) {
	big := "\xff\xd8\xff" + strings.Repeat("x", 64)
	g := photoServer(t, http.StatusOK, "image/jpeg", big)
	_, err := g.client.Call(context.Background(), Request{
		Method: "GET", Path: "/users/u1/photo/$value", Binary: true, MaxBytes: 16,
	})
	if err == nil {
		t.Fatal("a body past the limit was accepted")
	}
	if !strings.Contains(err.Error(), "16") {
		t.Errorf("err = %v, want the limit named", err)
	}
	// And the same body under a limit that fits comes back whole.
	res, err := g.client.Call(context.Background(), Request{
		Method: "GET", Path: "/users/u1/photo/$value", Binary: true, MaxBytes: int64(len(big)),
	})
	if err != nil {
		t.Fatalf("a body exactly at the limit was refused: %v", err)
	}
	if b, _ := res.(Binary); string(b.Data) != big {
		t.Errorf("the bytes came back changed: %q", b.Data)
	}
}

// TestOnlyABinaryRequestReadsBytes.
//
// The absence rule and the byte path both hang on one flag, and the day either
// leaks into the JSON path is the day a failed directory read looks like an empty
// one. So the same 404 on an ordinary request is still a failure.
func TestOnlyABinaryRequestReadsBytes(t *testing.T) {
	g := photoServer(t, http.StatusNotFound, "application/json", `{"error":{"code":"ResourceNotFound","message":"gone"}}`)
	if _, err := g.client.Call(context.Background(), Request{Method: "GET", Path: "/users/u1/photo/$value"}); err == nil {
		t.Error("a 404 on a JSON request was read as an empty result")
	}
	if g.accept != "application/json" {
		t.Errorf("Accept = %q on an ordinary request, want application/json", g.accept)
	}
}

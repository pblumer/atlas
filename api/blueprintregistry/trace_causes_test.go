package blueprintregistry

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"net"
	"reflect"
	"testing"
)

// The cause a traced load reports is what an operator acts on, so each failure a
// station can meet has to land on its own cause, not on the catch-all.

func TestALookupFailureNamesItsCause(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{context.DeadlineExceeded, CauseTimeout},
		{&net.DNSError{Err: "no such host", Name: "registry.example", IsNotFound: true}, CauseDNSNotFound},
		{&net.DNSError{Err: "server misbehaving", Name: "registry.example"}, CauseDNSFailed},
	}
	for _, c := range cases {
		if got := causeOfLookup(c.err); got != c.want {
			t.Errorf("causeOfLookup(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestADialFailureNamesItsCause(t *testing.T) {
	if got := causeOfDial(nil); got != CauseConnectFailed {
		t.Errorf("a dial that failed without an error = %q, want %q", got, CauseConnectFailed)
	}
	if got := causeOfDial(fmt.Errorf("dial: %w", context.DeadlineExceeded)); got != CauseTimeout {
		t.Errorf("a dial past its bound = %q, want %q", got, CauseTimeout)
	}
	if got := causeOfDial(errors.New("something else")); got != CauseConnectFailed {
		t.Errorf("an unclassified dial failure = %q, want %q", got, CauseConnectFailed)
	}
}

// A certificate nobody expected names its issuer: that is how a TLS inspection in the
// path shows itself.
func TestAHandshakeFailureNamesItsCauseAndIssuer(t *testing.T) {
	proxy := &x509.Certificate{Issuer: pkix.Name{CommonName: "Inspect CA", Organization: []string{"Firewall Ltd"}}}
	cases := []struct {
		name       string
		err        error
		cause      string
		wantIssuer string
	}{
		{"unknown authority", x509.UnknownAuthorityError{Cert: proxy}, CauseUnknownAuthority, "Inspect CA (Firewall Ltd)"},
		{"hostname", x509.HostnameError{Certificate: proxy, Host: "registry.example"}, CauseHostnameMismatch, "Inspect CA (Firewall Ltd)"},
		{"expired", x509.CertificateInvalidError{Cert: proxy, Reason: x509.Expired}, CauseCertificateExpired, "Inspect CA (Firewall Ltd)"},
		{"not tls", tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}, CauseNotTLS, ""},
		{"timeout", fmt.Errorf("handshake: %w", context.DeadlineExceeded), CauseTimeout, ""},
		{"other", errors.New("remote error: tls: internal error"), CauseTLSFailed, ""},
	}
	for _, c := range cases {
		step := tlsFailure(c.err)
		if step.Stage != StageTLS || step.Cause != c.cause || step.Issuer != c.wantIssuer {
			t.Errorf("%s: stage %q cause %q issuer %q, want %q %q %q", c.name, step.Stage, step.Cause, step.Issuer, StageTLS, c.cause, c.wantIssuer)
		}
	}
}

func TestTheStationsComeInTheOrderALoadPassesThem(t *testing.T) {
	want := []string{StageResolve, StageConnect, StageTLS, StageIndex, StageSignature}
	if got := Stations(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Stations() = %v, want %v", got, want)
	}
}

// A StopError reads as its cause, and unwraps to it, so a caller testing for the cause
// keeps working.
func TestAStopErrorReadsAsItsCause(t *testing.T) {
	cause := errors.New("connection refused")
	err := error(&StopError{Step: Step{Stage: StageConnect, Cause: CauseRefused}, Err: cause})
	if err.Error() != cause.Error() || !errors.Is(err, cause) {
		t.Fatalf("StopError reads %q and unwraps to the cause: %v", err.Error(), errors.Is(err, cause))
	}
	if step, ok := Stopped(err); !ok || step.Cause != CauseRefused {
		t.Fatalf("Stopped = %+v %v, want the connect station", step, ok)
	}
}

func TestAClientNamesTheHostItWasConfiguredFor(t *testing.T) {
	var none *PublicClient
	if got := none.Host(); got != "" {
		t.Errorf("a nil client names %q, want nothing", got)
	}
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewPublicClient("https://registry.example/index.json", pub)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Host(); got != "registry.example" {
		t.Errorf("Host() = %q, want registry.example", got)
	}
}

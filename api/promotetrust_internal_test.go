package api

import (
	"context"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPushBundleVerifiesAgainstTheOperatorsRoots covers the client half of
// ADR-0191. validateTargetURL demands https of a peer (ADR-0129) and this server
// can now serve it — but an on-prem pair usually gets its certificate from an
// internal CA, which the host's roots do not know. Without a way to name that CA,
// turning the listener on would move the failure from a clear refusal at
// configuration time to a verification error at promotion time, which is later and
// further from the cause.
//
// What this must never become is a way around verification: the first assertion is
// there so the second one means something.
func TestPushBundleVerifiesAgainstTheOperatorsRoots(t *testing.T) {
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"applicationId":"app-1","imported":true}`))
	}))
	defer peer.Close()
	tgt := deploymentTarget{ID: "t1", Name: "peer", BaseURL: peer.URL}

	// The host's roots have never heard of this certificate — which is exactly what
	// an internally issued one looks like — so the push does not go through.
	refused := (&Server{}).pushBundle(context.Background(), tgt, "", []byte(`{}`))
	if refused.Error == "" {
		t.Fatal("a certificate signed by nothing the host trusts was accepted")
	}
	if !strings.Contains(refused.Error, "unreachable") {
		t.Errorf("error %q does not read as a connection that did not happen", refused.Error)
	}

	// Naming the CA is what makes the same push work, and nothing else changes.
	pool := x509.NewCertPool()
	pool.AddCert(peer.Certificate())
	s := &Server{}
	WithTargetTLSRoots(pool)(s)

	got := s.pushBundle(context.Background(), tgt, "", []byte(`{}`))
	if got.Error != "" {
		t.Fatalf("push to a peer whose CA was named: %s", got.Error)
	}
	if !got.OK || got.RemoteApplicationID != "app-1" {
		t.Errorf("push result = %+v, want the peer's application id", got)
	}
}

// TestFillRemoteStatusVerifiesAgainstTheOperatorsRoots: reading a target's status
// back is the same conversation with the same peer, so it trusts the same roots.
// Leaving it on the default client would make a target that deploys fine show as
// unreachable in the Console.
func TestFillRemoteStatusVerifiesAgainstTheOperatorsRoots(t *testing.T) {
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"deployments":[]}`))
	}))
	defer peer.Close()
	tgt := deploymentTarget{ID: "t1", Name: "peer", BaseURL: peer.URL}

	var st targetStatus
	(&Server{}).fillRemoteStatus(context.Background(), &st, tgt, "app-1", "")
	if st.Error == "" {
		t.Fatal("a certificate signed by nothing the host trusts was accepted")
	}

	pool := x509.NewCertPool()
	pool.AddCert(peer.Certificate())
	s := &Server{}
	WithTargetTLSRoots(pool)(s)

	st = targetStatus{}
	s.fillRemoteStatus(context.Background(), &st, tgt, "app-1", "")
	if st.Error != "" {
		t.Errorf("status of a peer whose CA was named: %s", st.Error)
	}
}

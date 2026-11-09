package blueprintregistry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http/httptrace"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The stations a traced index load passes, in order. Each is reported once: as
// passed, or as the one it stopped at.
const (
	StageResolve   = "resolve"   // the registry's name, resolved and vetted as public
	StageConnect   = "connect"   // a TCP connection to one vetted address on 443
	StageTLS       = "tls"       // the TLS handshake and the certificate's verification
	StageIndex     = "index"     // index.json requested and read within its bound
	StageSignature = "signature" // index.json.sig read, and a pinned key made it
)

// StagePackage is the station after the index that only a release's download
// passes: its package read and checked against the size and SHA-256 the signed
// index states. It is not one of the Stations of an index load.
const StagePackage = "package"

// The causes a station can stop at. The vocabulary is fixed, so a caller may be
// shown which one without being shown an error text that names the network.
const (
	CauseTimeout              = "timeout"               // no answer within the bound
	CauseDNSNotFound          = "dns_not_found"         // the resolver does not know the name
	CauseDNSFailed            = "dns_failed"            // the resolver could not be asked or failed
	CauseDNSEmpty             = "dns_empty"             // an empty or oversized answer
	CauseNonpublicAddress     = "nonpublic_address"     // the name resolves to an internal address
	CauseRefused              = "refused"               // the connection was actively refused
	CauseUnreachable          = "unreachable"           // no route to the address
	CauseConnectFailed        = "connect_failed"        // any other connection failure
	CauseUnknownAuthority     = "unknown_authority"     // a certificate this server does not trust
	CauseHostnameMismatch     = "hostname_mismatch"     // a certificate for another name
	CauseCertificateExpired   = "certificate_expired"   // a certificate outside its validity
	CauseNotTLS               = "not_tls"               // something on 443 that does not speak TLS
	CauseTLSFailed            = "tls_failed"            // any other handshake failure
	CauseRedirect             = "redirect"              // the registry answered with a redirect
	CauseHTTPStatus           = "http_status"           // an HTTP status other than 200
	CauseTooLarge             = "too_large"             // more bytes than the bound allows
	CauseReadFailed           = "read_failed"           // the answer broke off
	CauseSignatureUnavailable = "signature_unavailable" // index.json.sig could not be read
	CauseNoPinnedKey          = "no_pinned_key"         // a signature no pinned key made
	CauseIndexInvalid         = "index_invalid"         // signed, but not a valid v1 index
	CausePackageMismatch      = "package_mismatch"      // package bytes other than the signed index states
)

// Step is one station of a traced index load. Millis is the time since the
// previous station, so the stations add up to the whole load. The facts are
// those the station established: what the name resolved to, which address
// answered, the TLS version and who issued the certificate, the HTTP status
// and size, the fingerprint of the key that made the signature.
type Step struct {
	Stage      string   `json:"stage"`
	OK         bool     `json:"ok"`
	Millis     int64    `json:"ms"`
	Cause      string   `json:"cause,omitempty"`
	Host       string   `json:"host,omitempty"`
	Addresses  []string `json:"addresses,omitempty"`
	Address    string   `json:"address,omitempty"`
	Tried      int      `json:"tried,omitempty"`
	TLSVersion string   `json:"tlsVersion,omitempty"`
	Issuer     string   `json:"issuer,omitempty"`
	Location   string   `json:"location,omitempty"`
	Status     int      `json:"status,omitempty"`
	Bytes      int      `json:"bytes,omitempty"`
	Key        string   `json:"key,omitempty"`
	// Err is the error the station stopped at, for the operator's log. It is not
	// part of the Step a caller is sent.
	Err error `json:"-"`
}

// StopError is a registry load or download that stopped at a station. Step names
// the station and its cause from the fixed vocabulary, with the facts the station
// established; it is what a traced load reports last. Err is the error itself:
// StopError reads and unwraps as Err, so a caller that tests for a cause keeps
// working.
type StopError struct {
	Step Step
	Err  error
}

func (e *StopError) Error() string { return e.Err.Error() }
func (e *StopError) Unwrap() error { return e.Err }

// Stopped returns the station a registry load or download stopped at, when its
// error names one. The Step's Err is for the operator's log, never for a caller.
func Stopped(err error) (Step, bool) {
	var stop *StopError
	if errors.As(err, &stop) {
		return stop.Step, true
	}
	return Step{}, false
}

// tracer collects the stations of one load and keeps the one it stopped at. Every
// load has one, traced or not, so an untraced load still says where it stopped;
// only a traced load reports the stations as it passes them. Every method is also
// safe on a nil tracer.
type tracer struct {
	mu       sync.Mutex
	report   func(Step)
	mark     time.Time
	stopped  bool
	stop     Step
	closed   bool
	location string
}

// newTracer starts a load's tracer. report receives each station as it is passed
// or where it stops; nil reports nothing and only keeps the stop.
func newTracer(report func(Step)) *tracer {
	if report == nil {
		report = func(Step) {}
	}
	return &tracer{report: report, mark: time.Now()}
}

type tracerKey struct{}

func withTracer(ctx context.Context, tr *tracer) context.Context {
	if tr == nil {
		return ctx
	}
	return context.WithValue(ctx, tracerKey{}, tr)
}

func tracerFrom(ctx context.Context) *tracer {
	tr, _ := ctx.Value(tracerKey{}).(*tracer)
	return tr
}

func (tr *tracer) emit(step Step, ok bool) {
	if tr == nil {
		return
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.closed || tr.stopped {
		return
	}
	now := time.Now()
	step.OK, step.Millis, tr.mark = ok, now.Sub(tr.mark).Milliseconds(), now
	if !ok {
		tr.stopped, tr.stop = true, step
	}
	tr.report(step)
}

// wrap names the station the load stopped at in its error, when it stopped at one.
func (tr *tracer) wrap(err error) error {
	if tr == nil || err == nil {
		return err
	}
	tr.mu.Lock()
	stopped, stop := tr.stopped, tr.stop
	tr.mu.Unlock()
	if !stopped {
		return err
	}
	return &StopError{Step: stop, Err: err}
}

func (tr *tracer) pass(step Step) { tr.emit(step, true) }
func (tr *tracer) fail(step Step) { tr.emit(step, false) }

func (tr *tracer) close() {
	if tr == nil {
		return
	}
	tr.mu.Lock()
	tr.closed = true
	tr.mu.Unlock()
}

func (tr *tracer) redirected(host string) {
	if tr == nil {
		return
	}
	tr.mu.Lock()
	tr.location = host
	tr.mu.Unlock()
}

// clientTrace reports the TLS handshake; resolving and connecting are reported
// by dialPublic, which does both itself.
func (tr *tracer) clientTrace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		TLSHandshakeDone: func(state tls.ConnectionState, err error) {
			if err != nil {
				tr.fail(tlsFailure(err))
				return
			}
			step := Step{Stage: StageTLS, TLSVersion: tls.VersionName(state.Version)}
			if len(state.PeerCertificates) > 0 {
				step.Issuer = issuerName(state.PeerCertificates[0])
			}
			tr.pass(step)
		},
	}
}

func (tr *tracer) passIndex(size int) {
	tr.pass(Step{Stage: StageIndex, Status: 200, Bytes: size})
}

// failRequest reports why the request of a station — the index, or a release's
// package — failed, unless a station before it already said so: a failed
// connection or handshake is also the request's error.
func (tr *tracer) failRequest(stage string, err error) {
	if tr == nil {
		return
	}
	tr.mu.Lock()
	location := tr.location
	tr.mu.Unlock()
	step := Step{Stage: stage, Err: err}
	var status statusError
	switch {
	case location != "":
		step.Cause, step.Location = CauseRedirect, location
	case errors.As(err, &status):
		step.Cause, step.Status = CauseHTTPStatus, status.code
	case strings.Contains(err.Error(), "exceeds configured byte limit") && stage == StagePackage:
		// A package is read up to the size the signed index states: more bytes
		// than that are bytes the index did not sign.
		step.Cause = CausePackageMismatch
	case strings.Contains(err.Error(), "exceeds configured byte limit"):
		step.Cause = CauseTooLarge
	case isTimeout(err):
		step.Cause = CauseTimeout
	default:
		step.Cause = CauseReadFailed
	}
	tr.fail(step)
}

// statusError is an answer with a status other than 200.
type statusError struct{ code int }

func (e statusError) Error() string { return "registry returned HTTP " + strconv.Itoa(e.code) }

func httpStatusOf(err error) int {
	var status statusError
	if errors.As(err, &status) {
		return status.code
	}
	return 0
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout())
}

func causeOfLookup(err error) string {
	var dns *net.DNSError
	switch {
	case isTimeout(err):
		return CauseTimeout
	case errors.As(err, &dns) && dns.IsNotFound:
		return CauseDNSNotFound
	default:
		return CauseDNSFailed
	}
}

func causeOfDial(err error) string {
	switch {
	case err == nil:
		return CauseConnectFailed
	case isTimeout(err):
		return CauseTimeout
	case isRefused(err):
		return CauseRefused
	case isUnreachable(err):
		return CauseUnreachable
	default:
		return CauseConnectFailed
	}
}

// tlsFailure names why a handshake failed and, where a certificate was shown,
// who issued it: an issuer nobody expected is a TLS inspection in the path.
func tlsFailure(err error) Step {
	step := Step{Stage: StageTLS, Cause: CauseTLSFailed, Err: err}
	var (
		verify   *tls.CertificateVerificationError
		unknown  x509.UnknownAuthorityError
		hostname x509.HostnameError
		invalid  x509.CertificateInvalidError
		record   tls.RecordHeaderError
	)
	if errors.As(err, &verify) && len(verify.UnverifiedCertificates) > 0 {
		step.Issuer = issuerName(verify.UnverifiedCertificates[0])
	}
	switch {
	case errors.As(err, &unknown):
		step.Cause = CauseUnknownAuthority
		if step.Issuer == "" && unknown.Cert != nil {
			step.Issuer = issuerName(unknown.Cert)
		}
	case errors.As(err, &hostname):
		step.Cause = CauseHostnameMismatch
		if step.Issuer == "" && hostname.Certificate != nil {
			step.Issuer = issuerName(hostname.Certificate)
		}
	case errors.As(err, &invalid) && invalid.Reason == x509.Expired:
		step.Cause = CauseCertificateExpired
		if step.Issuer == "" && invalid.Cert != nil {
			step.Issuer = issuerName(invalid.Cert)
		}
	case errors.As(err, &record):
		step.Cause = CauseNotTLS
	case isTimeout(err):
		step.Cause = CauseTimeout
	}
	return step
}

func issuerName(cert *x509.Certificate) string {
	if cert == nil {
		return ""
	}
	name := cert.Issuer.CommonName
	if org := strings.Join(cert.Issuer.Organization, ", "); org != "" {
		if name == "" {
			name = org
		} else if !strings.Contains(name, org) {
			name += " (" + org + ")"
		}
	}
	return name
}

// Stations names the stations of a traced load in the order they are passed.
func Stations() []string {
	return []string{StageResolve, StageConnect, StageTLS, StageIndex, StageSignature}
}

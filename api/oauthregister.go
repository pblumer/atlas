package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
)

// Dynamic client registration, RFC 7591 (ADR-0200, step 2).
//
// This is the endpoint that lets a client register *itself*, so a person can point
// a hosted MCP connector at an Atlas and connect without an administrator entering
// anything first. It is what the connector dialogs of the AI tools reach for when
// they are given nothing but a URL.
//
// It is also the one unauthenticated endpoint in this server that writes durable
// state, and the record calls that "a decision of its own". Four things carry that
// decision, and none of them is optional:
//
//   - **Off unless an operator turns it on.** Not merely closed: the route is not
//     mounted and the metadata does not advertise it, so a client discovers the
//     truth instead of being told to try and then refused. The MCP specification
//     makes registration a MAY and puts pre-registration first, so the default
//     costs a compliant client nothing.
//   - **The client is marked as self-registered, all the way to the consent
//     screen.** Without that, opening registration silently degrades every consent
//     decision anybody makes afterwards: "an application is asking for access"
//     stops implying that anyone vetted it, and the person deciding cannot tell.
//   - **The registry is bounded, and the cap evicts rather than refuses.** A cap
//     that only refuses is its own denial of service — whoever fills the table
//     first locks everybody else out, permanently and from outside. What is evicted
//     is the oldest self-registration nobody ever approved; an approved one is
//     never touched, or a stranger could revoke somebody's access by registering
//     enough clients.
//   - **It is throttled on its own budget.** Its own, rather than the shared public
//     one, so a flood of registrations cannot throttle the token exchanges of the
//     clients that already registered — that would turn abuse of this endpoint into
//     an outage for everyone else.
//
// What remains, and is not fixed here: a client that registered but has not yet
// been approved can be evicted by a flood before its person gets to the consent
// screen, and would have to register again. That window is seconds long in
// practice, and closing it properly means asking an administrator to admit each
// registration — which is pre-registration, which is already the default.
//
// Registering buys nothing on its own. A client id and secret let you *ask* a
// person for access; only their approval reaches anything, and what it reaches is
// bounded by their own account (ADR-0196).

const (
	// oauthRegisterPath is RFC 7591's registration endpoint. Published in the
	// authorization-server metadata only when it is actually served.
	oauthRegisterPath = "/oauth/register"

	// maxRegistrationBytes bounds a registration body. RFC 7591 metadata is a
	// handful of short fields; anything larger is not a client with a long name.
	maxRegistrationBytes = 16 << 10

	// maxClientNameRunes bounds what a self-registered client may call itself,
	// because that name is rendered on a consent screen a person is reading in order
	// to make a decision. The page escapes it; this keeps it from crowding out the
	// question being asked.
	maxClientNameRunes = 120
)

// MaxDynamicClients is how many self-registered clients this server keeps. Past
// it, registering evicts the oldest one nobody approved — see the eviction rule
// above for why it is not a refusal.
//
// Sized for what it is: an installation has a handful of connectors, not hundreds,
// and every one somebody actually approved is exempt from the cap's eviction
// anyway. An operator who needs more registers them by hand, which has no cap.
const MaxDynamicClients = 16

// WithDynamicClientRegistration opens RFC 7591 self-registration.
//
// Off by default, deliberately: see the note at the top of this file. Turning it
// on means anybody who can reach this port can create a client record and be shown
// to your people on a consent screen — under a name they chose.
func WithDynamicClientRegistration() Option {
	return func(s *Server) { s.dynamicRegistration = true }
}

// dynamicRegistrationRequest is the subset of RFC 7591 client metadata this server
// has an answer for. Anything else in the body is ignored rather than refused: the
// registration request is an open set, and a client that sends a field about a
// feature this server does not have is not making a mistake.
type dynamicRegistrationRequest struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

// handleRegisterDynamicClient is POST /oauth/register.
//
// Public as a route, because the whole point is a caller that holds nothing yet.
func (s *Server) handleRegisterDynamicClient(w http.ResponseWriter, r *http.Request) {
	if !s.registerRate.allow(httpapi.ClientIP(r)) {
		oauthError(w, http.StatusTooManyRequests, "temporarily_unavailable",
			"too many registrations from this address; try again shortly")
		return
	}
	// One byte past the limit, so an oversized body is *named* rather than silently
	// truncated and then reported as malformed JSON — which would send a client
	// looking for a syntax error it does not have.
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRegistrationBytes+1))
	if err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "the body could not be read")
		return
	}
	if len(body) > maxRegistrationBytes {
		oauthError(w, http.StatusRequestEntityTooLarge, "invalid_client_metadata",
			"the registration request is too large")
		return
	}
	var req dynamicRegistrationRequest
	if err := json.Unmarshal(body, &req); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "the body is not a JSON object")
		return
	}

	// The redirect URIs first, and by the same rule an administrator's registration
	// goes through: https unless loopback, absolute, no fragment. RFC 7591 has a
	// distinct error code for them because a client library can act on it.
	uris, err := validRedirectURIs(req.RedirectURIs)
	if err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
		return
	}
	if err := checkRegistrationMetadata(req); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", err.Error())
		return
	}
	name, err := registrationName(req.ClientName, uris[0])
	if err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", err.Error())
		return
	}

	id, err := newID()
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "generate id")
		return
	}
	suffix, err := randomHex(32)
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "generate secret")
		return
	}
	secret := oauthClientSecretPrefix + suffix
	rec := oauthClient{
		ID: id, Name: name, SecretHash: hashAPIToken(secret),
		RedirectURIs: uris, CreatedAt: time.Now().Unix(), Dynamic: true,
	}

	evicted, err := s.saveDynamicClient(&rec)
	// Before the error check, not after: an eviction already removed a record, and a
	// failure later in the same turn does not put it back. A client that disappeared
	// without a line saying so is the one thing an operator cannot reconstruct.
	// Through audit rather than a bare log line, because the address that caused it
	// is exactly what somebody looking at a churning registry needs.
	for _, gone := range evicted {
		audit(r, logging.AuthOAuthClientDeleted, "oauth client evicted: the self-registration cap was reached",
			slog.String("client_id", gone), slog.Int("cap", MaxDynamicClients))
	}
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", err.Error())
		return
	}
	audit(r, logging.AuthOAuthClientSelfRegistered, "oauth client registered itself",
		slog.String("client_id", rec.ID), slog.String("client_name", rec.Name),
		slog.Int("redirect_uris", len(rec.RedirectURIs)))

	// RFC 7591 §3.2.1: 201, the issued credentials, and the metadata echoed back so
	// the client can see what was actually recorded rather than what it asked for.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	httpapi.JSON(w, http.StatusCreated, map[string]any{
		"client_id":     rec.ID,
		"client_secret": secret,
		// Required when a secret is issued. Zero means it does not expire: this
		// server has no secret rotation, and saying "0" is the honest answer rather
		// than a date nothing enforces.
		"client_secret_expires_at":   0,
		"client_id_issued_at":        rec.CreatedAt,
		"client_name":                rec.Name,
		"redirect_uris":              rec.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "client_secret_post",
	})
}

// checkRegistrationMetadata refuses a request this server could not honour, at
// registration rather than at the first flow that fails.
//
// Each of these is a thing the authorization server genuinely does not do, and
// telling a client now is the difference between a clear error in its logs and a
// person staring at a redirect that never completes.
func checkRegistrationMetadata(req dynamicRegistrationRequest) error {
	for _, g := range req.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			return errUnsupported("grant_type", g, "authorization_code and refresh_token")
		}
	}
	for _, rt := range req.ResponseTypes {
		if rt != "code" {
			return errUnsupported("response_type", rt, "code")
		}
	}
	if m := strings.TrimSpace(req.TokenEndpointAuthMethod); m != "" && m != "client_secret_post" {
		return errUnsupported("token_endpoint_auth_method", m, "client_secret_post")
	}
	return nil
}

func errUnsupported(field, got, supported string) error {
	return errors.New(field + " " + got + " is not supported; this server supports " + supported)
}

// registrationName settles what the consent screen will call this application.
//
// A self-registered client names itself, which is exactly as trustworthy as it
// sounds — hence the marker that travels with it. When it names itself nothing, the
// host it registered a redirect for is used instead: that is at least a claim the
// client had to be able to receive a redirect on.
func registrationName(raw, fallbackURI string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		if u, err := url.Parse(fallbackURI); err == nil && u.Host != "" {
			return u.Host, nil
		}
		return "", errors.New("client_name is required when the redirect URI has no host")
	}
	if utf8.RuneCountInString(name) > maxClientNameRunes {
		return "", errors.New("client_name is too long")
	}
	for _, r := range name {
		// Control characters, line breaks included: this string is rendered on the
		// screen where somebody decides. The page escapes it, so this is not about
		// markup — it is about a name that cannot restructure the question.
		if unicode.IsControl(r) {
			return "", errors.New("client_name must not contain control characters")
		}
	}
	return name, nil
}

// saveDynamicClient makes room if the registry is full and saves the record.
//
// One run-loop turn for the whole thing: the count, the eviction and the save are
// a single decision, and interleaving two registrations across it is how a cap of
// sixteen becomes seventeen. Returns the ids evicted, for the log.
//
// The durable delete goes before the durable save, so a failure between them
// leaves one fewer client rather than one too many — the direction that cannot
// grow the disk. The evicted record is by construction one nobody ever approved.
func (s *Server) saveDynamicClient(rec *oauthClient) ([]string, error) {
	var (
		evicted []string
		failure error
	)
	s.do(func() {
		clients, err := s.oauthClientStore.LoadAll()
		if err != nil {
			failure = errors.New("read the client registry: " + err.Error())
			return
		}
		grants, err := s.oauthGrantStore.LoadAll()
		if err != nil {
			failure = errors.New("read the grants: " + err.Error())
			return
		}
		approved := map[string]bool{}
		for _, g := range grants {
			approved[g.ClientID] = true
		}

		candidates := evictionOrder(clients)
		rec.Seq = nextDynamicSeq(clients)
		for len(candidates) >= MaxDynamicClients {
			victim, ok := firstUnapproved(candidates, approved)
			if !ok {
				// Every self-registration in the table is one somebody said yes to. There
				// is nothing here that may be thrown away, and the answer is an operator's
				// to give: remove a client nobody uses any more, or register the ones that
				// are needed by hand, which has no cap.
				failure = errors.New("the registration table is full of approved clients; an administrator must remove one")
				return
			}
			if err := s.oauthClientStore.Delete(victim); err != nil {
				failure = errors.New("evict a client: " + err.Error())
				return
			}
			s.oauthClients.remove(victim)
			evicted = append(evicted, victim)
			candidates = withoutClient(candidates, victim)
		}

		if err := s.oauthClientStore.Save(*rec); err != nil {
			failure = errors.New("save the client: " + err.Error())
			return
		}
		s.oauthClients.add(*rec)
	})
	return evicted, failure
}

// evictionOrder is the self-registered clients, oldest first. Operator-registered
// ones are not in it at all: an administrator's decision is not something this
// endpoint may undo.
func evictionOrder(clients []oauthClient) []oauthClient {
	out := make([]oauthClient, 0, len(clients))
	for _, c := range clients {
		if c.Dynamic {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Seq != out[b].Seq {
			return out[a].Seq < out[b].Seq
		}
		return out[a].ID < out[b].ID
	})
	return out
}

// nextDynamicSeq is one past the highest sequence any client carries. See
// oauthClient.Seq for why the clock is not enough.
func nextDynamicSeq(clients []oauthClient) int64 {
	var max int64
	for _, c := range clients {
		if c.Seq > max {
			max = c.Seq
		}
	}
	return max + 1
}

// firstUnapproved is the oldest candidate nobody has approved.
func firstUnapproved(candidates []oauthClient, approved map[string]bool) (string, bool) {
	for _, c := range candidates {
		if !approved[c.ID] {
			return c.ID, true
		}
	}
	return "", false
}

func withoutClient(in []oauthClient, id string) []oauthClient {
	out := in[:0]
	for _, c := range in {
		if c.ID != id {
			out = append(out, c)
		}
	}
	return out
}

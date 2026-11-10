// Package entrachannel is the conformance suite that checks the Entra Worker Type's
// create-channel operation against the technical capability contract
// io.atlas.collaboration.channel.create@1.0.0+microsoft-teams (ADR-0483).
//
// Every case runs the operation itself — entra.Run with a real GraphClient — against
// an in-process fake of the one Graph endpoint it calls, so a record can be derived
// and verified anywhere, without a tenant or a credential. The fake's answers to a
// refusal (the status codes and Graph error codes below) are assumptions written down
// here, not answers observed from a tenant; the cases are built so that their outcome
// does not depend on those exact codes, only on the request being refused.
//
// The package's dependency digest is the fixture revision an evidence record carries,
// so changing a case, the fake or the adapter moves every record that names this
// suite, as it should.
package entrachannel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/pblumer/atlas/connector/entra"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/workertype"
	"github.com/pblumer/atlas/workertype/evidence"
)

// Name is the name evidence records use for this suite.
const Name = "entra-create-channel"

// The contract this suite encodes. It refuses to run against any other identity.
const (
	contractID      = "io.atlas.collaboration.channel.create"
	contractVersion = "1.0.0"
	contractProfile = "microsoft-teams"
)

// The cases, in the order they run and are recorded.
const (
	caseCreates                    = "creates a standard channel and answers its id"
	caseUnknownSpaceCreatesNothing = "an unknown space reports no success and creates nothing"
	caseUnknownSpaceCode           = "an unknown space is SPACE_NOT_FOUND"
	caseRepeatCreatesNoSecond      = "a repeated create with the same name creates no second channel"
	caseRepeatCode                 = "a name already in use is CHANNEL_ALREADY_EXISTS"
	caseRefusedSpace               = "a space the application may not write to reports no success and creates nothing"
	caseVisibleToMembers           = "the new channel is visible to the space's members"
)

// codeOf maps a BPMN error code the operation throws to the contract's code. Entra's
// create-channel throws none today, so the map is empty and the two code cases fail;
// a code the operation gains is mapped here in the same change.
var codeOf = map[string]string{}

// runOperation is the operation under test. It is entra.Run; the suite's own tests
// replace it with operations that misbehave, to show each case catches what it names.
var runOperation = entra.Run

// The spaces the fake knows. The ids are fixed, so a record's results never carry one
// that differs between runs.
const (
	knownSpace    = "space-1"
	otherSpace    = "space-2"
	refusedSpace  = "space-refused"
	unknownSpace  = "space-missing"
	workerName    = "suite"
	resultVarName = "channel"
)

func init() { evidence.Register(Name, Suite()) }

// Suite is the registered suite.
func Suite() evidence.Suite {
	return evidence.Suite{
		Package: "github.com/pblumer/atlas/workertype/evidence/suites/entrachannel",
		Environment: "Entra's create-channel run through entra.Run and its GraphClient against an in-process fake of " +
			"POST /teams/{id}/channels. The fake creates a channel in a known team, answers 404 NotFound for an unknown team, " +
			"403 Forbidden for a team the application may not write to, and 409 NameAlreadyExists for a display name the team " +
			"already has; those refusal codes are assumptions, not answers observed from a tenant, and no case depends on them.",
		Run: run,
	}
}

func run(ctx context.Context, contract workertype.TechnicalContract) ([]evidence.Result, error) {
	m := contract.Metadata
	if m.ID != contractID || m.Version != contractVersion || m.Profile != contractProfile {
		return nil, fmt.Errorf("this suite checks %s@%s+%s, not %s@%s+%s", contractID, contractVersion, contractProfile, m.ID, m.Version, m.Profile)
	}
	compiled, err := workertype.CompileTechnicalContract(contract)
	if err != nil {
		return nil, err
	}
	s := &session{ctx: ctx, contract: compiled}
	cases := []func() (evidence.Result, error){
		s.creates,
		s.unknownSpaceCreatesNothing,
		s.unknownSpaceCode,
		s.repeatCreatesNoSecond,
		s.repeatCode,
		s.refusedSpace,
		func() (evidence.Result, error) {
			return evidence.Result{Case: caseVisibleToMembers, Outcome: evidence.OutcomeNotExercised,
				Detail: "only a live tenant shows when a new channel appears to the team's members; the fake has no members"}, nil
		},
	}
	out := make([]evidence.Result, 0, len(cases))
	for _, c := range cases {
		r, err := c()
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// session carries what every case shares: the context and the compiled contract that
// checks each case's input and each answer.
type session struct {
	ctx      context.Context
	contract *workertype.CompiledTechnicalContract
}

// operate runs the operation for one contract input against g and returns the
// contract's output for a success. The input is checked against the contract first —
// an input the contract refuses is a defect of the suite, not an outcome.
//
// The mapping is the adapter a model writes in BPMN: spaceId becomes the task's
// groupId, displayName and description its attributes, with membershipType fixed to
// standard because the contract promises a channel every member of the space sees.
func (s *session) operate(g *fakeGraph, in map[string]any) (map[string]any, error) {
	raw, _ := json.Marshal(in)
	if err := s.contract.ValidateInputJSON(raw); err != nil {
		return nil, fmt.Errorf("suite input refused by the contract: %w", err)
	}
	attrs := map[string]any{"displayName": in["displayName"], "membershipType": "standard"}
	if d, ok := in["description"]; ok {
		attrs["description"] = d
	}
	reg := entra.NewRegistry()
	reg.Register(workerName, entra.NewGraphClient(staticToken{}, g.srv.URL, g.srv.Client()))
	res, err := runOperation(s.ctx, entra.Job{
		Connector: workerName, Operation: "create-channel", GroupID: in["spaceId"].(string),
		Attributes: attrs, ResultVariable: resultVarName, ErrorCodes: entra.Codes,
	}, reg)
	if err != nil {
		return nil, err
	}
	channel, _ := res[resultVarName].(map[string]any)
	out := map[string]any{"channelId": channel["id"]}
	if name, ok := channel["displayName"]; ok {
		out["displayName"] = name
	}
	return out, nil
}

// validOutput reports whether out is what the contract allows a success to answer.
func (s *session) validOutput(out map[string]any) bool {
	raw, err := json.Marshal(out)
	return err == nil && s.contract.ValidateOutputJSON(raw) == nil
}

// contractCode is the contract code err carries, or "" when the operation ended with a
// plain failure a process cannot catch.
func contractCode(err error) string {
	var be *job.BPMNError
	if errors.As(err, &be) {
		return codeOf[be.Code]
	}
	return ""
}

func passed(name string) evidence.Result {
	return evidence.Result{Case: name, Outcome: evidence.OutcomePassed}
}

func failed(name, detail string) evidence.Result {
	return evidence.Result{Case: name, Outcome: evidence.OutcomeFailed, Detail: detail}
}

func (s *session) creates() (evidence.Result, error) {
	g := newFakeGraph()
	defer g.close()
	in := map[string]any{"spaceId": knownSpace, "displayName": "Projekt Alpha", "description": "Abstimmung im Projekt"}
	out, err := s.operate(g, in)
	if err != nil {
		if strings.HasPrefix(err.Error(), "suite input") {
			return evidence.Result{}, err
		}
		return failed(caseCreates, "the operation failed for a known team and a free name"), nil
	}
	post, ok := g.onlyPost()
	switch {
	case !ok:
		return failed(caseCreates, "the operation did not send exactly one create request"), nil
	case post.path != "/teams/"+knownSpace+"/channels":
		return failed(caseCreates, "the create request went to another path than the team's channels"), nil
	case post.body["displayName"] != in["displayName"] || post.body["description"] != in["description"]:
		return failed(caseCreates, "the create request did not carry the display name and description asked for"), nil
	case post.body["membershipType"] != "standard":
		return failed(caseCreates, "the create request did not ask for a standard channel"), nil
	case !s.validOutput(out):
		return failed(caseCreates, "the answer is not what the contract's output schema allows"), nil
	case out["channelId"] != g.channelID(knownSpace, "Projekt Alpha"):
		return failed(caseCreates, "the answered channelId is not the id of the channel created"), nil
	}
	return passed(caseCreates), nil
}

func (s *session) unknownSpaceCreatesNothing() (evidence.Result, error) {
	g := newFakeGraph()
	defer g.close()
	_, err := s.operate(g, map[string]any{"spaceId": unknownSpace, "displayName": "Projekt Alpha"})
	if err == nil {
		return failed(caseUnknownSpaceCreatesNothing, "the operation reported success for a team that does not exist"), nil
	}
	if g.created() != 0 {
		return failed(caseUnknownSpaceCreatesNothing, "a channel was created although the team does not exist"), nil
	}
	return passed(caseUnknownSpaceCreatesNothing), nil
}

func (s *session) unknownSpaceCode() (evidence.Result, error) {
	g := newFakeGraph()
	defer g.close()
	_, err := s.operate(g, map[string]any{"spaceId": unknownSpace, "displayName": "Projekt Alpha"})
	if err == nil {
		return failed(caseUnknownSpaceCode, "the operation reported success for a team that does not exist"), nil
	}
	if got := contractCode(err); got != "SPACE_NOT_FOUND" {
		return failed(caseUnknownSpaceCode, "the operation ends with a plain failure, not a code a process can catch and tell apart"), nil
	}
	return passed(caseUnknownSpaceCode), nil
}

func (s *session) repeat() (*fakeGraph, error) {
	g := newFakeGraph()
	in := map[string]any{"spaceId": knownSpace, "displayName": "Projekt Alpha"}
	if _, err := s.operate(g, in); err != nil {
		g.close()
		return nil, fmt.Errorf("suite setup: the first create failed: %w", err)
	}
	_, err := s.operate(g, in)
	return g, err
}

func (s *session) repeatCreatesNoSecond() (evidence.Result, error) {
	g, err := s.repeat()
	if g == nil {
		return evidence.Result{}, err
	}
	defer g.close()
	if err == nil {
		return failed(caseRepeatCreatesNoSecond, "the second create reported success for a name the team already has"), nil
	}
	if g.created() != 1 {
		return failed(caseRepeatCreatesNoSecond, "the team holds more than one channel of that name"), nil
	}
	return passed(caseRepeatCreatesNoSecond), nil
}

func (s *session) repeatCode() (evidence.Result, error) {
	g, err := s.repeat()
	if g == nil {
		return evidence.Result{}, err
	}
	defer g.close()
	if err == nil {
		return failed(caseRepeatCode, "the second create reported success for a name the team already has"), nil
	}
	if got := contractCode(err); got != "CHANNEL_ALREADY_EXISTS" {
		return failed(caseRepeatCode, "the operation ends with a plain failure, not a code a process can catch and tell apart"), nil
	}
	return passed(caseRepeatCode), nil
}

func (s *session) refusedSpace() (evidence.Result, error) {
	g := newFakeGraph()
	defer g.close()
	_, err := s.operate(g, map[string]any{"spaceId": refusedSpace, "displayName": "Projekt Alpha"})
	if err == nil {
		return failed(caseRefusedSpace, "the operation reported success although the application may not write to the team"), nil
	}
	if g.created() != 0 {
		return failed(caseRefusedSpace, "a channel was created although the application may not write to the team"), nil
	}
	return passed(caseRefusedSpace), nil
}

// staticToken stands in for the tenant's token source; the fake checks no credential.
type staticToken struct{}

func (staticToken) Token(context.Context) (string, error) { return "suite-token", nil }

// post is one create request the fake received.
type post struct {
	path string
	body map[string]any
}

// fakeGraph answers POST /teams/{id}/channels for the teams it knows, and nothing else.
type fakeGraph struct {
	srv      *httptest.Server
	mu       sync.Mutex
	posts    []post
	channels map[string][]string // team id -> display names, in creation order
}

func newFakeGraph() *fakeGraph {
	g := &fakeGraph{channels: map[string][]string{knownSpace: {"General"}, otherSpace: {"General"}, refusedSpace: {"General"}}}
	g.srv = httptest.NewServer(http.HandlerFunc(g.serve))
	return g
}

func (g *fakeGraph) close() { g.srv.Close() }

func (g *fakeGraph) serve(w http.ResponseWriter, r *http.Request) {
	team, ok := strings.CutPrefix(r.URL.Path, "/teams/")
	team, rest, _ := strings.Cut(team, "/")
	if !ok || rest != "channels" || r.Method != http.MethodPost {
		graphError(w, http.StatusNotFound, "NotFound", "the fake answers only POST /teams/{id}/channels")
		return
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		graphError(w, http.StatusBadRequest, "BadRequest", "the body is not a JSON object")
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.posts = append(g.posts, post{path: r.URL.Path, body: body})
	names, known := g.channels[team]
	name, _ := body["displayName"].(string)
	switch {
	case !known:
		graphError(w, http.StatusNotFound, "NotFound", "no team with this group id")
	case team == refusedSpace:
		graphError(w, http.StatusForbidden, "Forbidden", "missing role permissions on the team")
	case containsName(names, name):
		graphError(w, http.StatusConflict, "NameAlreadyExists", "a channel with this name already exists")
	default:
		g.channels[team] = append(names, name)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": channelID(team, name), "displayName": name, "description": body["description"],
			"membershipType": body["membershipType"],
		})
	}
}

func containsName(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

func graphError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(map[string]any{"error": map[string]any{"code": code, "message": msg}})
	_, _ = w.Write(buf.Bytes())
}

// channelID is the id the fake gives a channel: fixed by team and name, so no run
// differs from another.
func channelID(team, name string) string {
	return "19:" + strings.ReplaceAll(strings.ToLower(team+"-"+name), " ", "-") + "@thread.tacv2"
}

func (g *fakeGraph) channelID(team, name string) string { return channelID(team, name) }

// onlyPost is the one create request the fake received, if it received exactly one.
func (g *fakeGraph) onlyPost() (post, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.posts) != 1 {
		return post{}, false
	}
	return g.posts[0], true
}

// created counts the channels created beyond each team's General channel.
func (g *fakeGraph) created() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, names := range g.channels {
		n += len(names) - 1
	}
	return n
}

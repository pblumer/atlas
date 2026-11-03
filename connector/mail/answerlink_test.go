package mail_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/mail"
)

// A mail task that carries the link of a user task answered by link
// (ADR-draft-user-task-answered-by-link): Resolve asks whoever resolves recipients for the
// link as well, and hands it to the subject and bodies as answerLink.

// linkDirectory resolves no recipient and mints the links a test hands it.
type linkDirectory struct {
	link  string
	err   error
	asked []string
	pi    uint64
	scope uint64
}

func (*linkDirectory) Recipients(string) ([]string, error) { return nil, nil }

func (d *linkDirectory) AnswerLink(piKey, scopeKey uint64, task string) (string, error) {
	d.asked = append(d.asked, task)
	d.pi, d.scope = piKey, scopeKey
	return d.link, d.err
}

func answerMailConfig(t *testing.T) compiler.MailConfig {
	return compiler.MailConfig{
		Connector:  "mail",
		To:         compiler.RestExpr{Literal: "kunde@example.ch"},
		Subject:    compiler.RestExpr{Literal: "Kostenvoranschlag"},
		Body:       compiler.RestExpr{Expr: mustExpr(t, `"Bitte freigeben: " + answerLink`)},
		AnswerTask: "approve",
		Retries:    3,
	}
}

func TestResolveHandsTheMailItsAnswerLink(t *testing.T) {
	dir := &linkDirectory{link: "https://atlas.example.ch/public/answer#1.YXBwcm92ZQ.0123"}
	_, j := resolveFixtureWith(t, dir, answerMailConfig(t))
	if j.ResolveError != "" {
		t.Fatalf("resolve error: %s", j.ResolveError)
	}
	if j.Body != "Bitte freigeben: "+dir.link {
		t.Errorf("body = %q, want the link the directory minted", j.Body)
	}
	if len(dir.asked) != 1 || dir.asked[0] != "approve" || dir.pi == 0 || dir.scope != dir.pi {
		t.Errorf("asked for %v in instance %d, scope %d: want the answer task once, in the mail's instance and scope (the root here)",
			dir.asked, dir.pi, dir.scope)
	}
}

func TestAMailWithoutAnAnswerTaskAsksForNoLink(t *testing.T) {
	dir := &linkDirectory{link: "unused"}
	cfg := answerMailConfig(t)
	cfg.AnswerTask = ""
	cfg.Body = compiler.RestExpr{Literal: "Hallo"}
	_, j := resolveFixtureWith(t, dir, cfg)
	if len(dir.asked) != 0 || j.ResolveError != "" || j.Body != "Hallo" {
		t.Errorf("asked %v, resolve error %q, body %q: a plain mail asks for no link", dir.asked, j.ResolveError, j.Body)
	}
}

// TestAMailWhoseLinkCannotBeMintedFailsSayingWhy: the mail is what the link is for, so a
// mail without it does not go out — and the job says why, on the worker's side too.
func TestAMailWhoseLinkCannotBeMintedFailsSayingWhy(t *testing.T) {
	for _, c := range []struct {
		name string
		dir  mail.Directory
		want string
	}{
		{"a resolver that mints no links", stubDirectory{}, "answer link"},
		{"a link that cannot be minted", &linkDirectory{err: errors.New("no external URL is configured")}, "no external URL is configured"},
	} {
		t.Run(c.name, func(t *testing.T) {
			reg, j := resolveFixtureWith(t, c.dir, answerMailConfig(t))
			if !strings.Contains(j.ResolveError, c.want) {
				t.Fatalf("resolve error = %q, want it to say %q", j.ResolveError, c.want)
			}
			_, err := mail.Run(context.Background(), j, reg)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("run: %v, want the resolve error, before any worker is looked up", err)
			}
		})
	}
}

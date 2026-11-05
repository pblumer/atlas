package mail_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/mail"
)

// A mail task that names an account with passwordFor (ADR-0526): Resolve asks the
// directory for the account's one-time password link, handing it the process and the
// recipients, reads it as passwordLink, and marks it for a preview to withhold.

// pwDirectory resolves no recipient and mints the password links a test hands it.
type pwDirectory struct {
	link    string
	err     error
	process string
	account string
	to      []string
}

func (*pwDirectory) Recipients(string) ([]string, error) { return nil, nil }

func (d *pwDirectory) PasswordLink(_ uint64, processID, username string, to, _, _ []string) (string, error) {
	d.process, d.account, d.to = processID, username, to
	return d.link, d.err
}

func passwordMailConfig(t *testing.T) compiler.MailConfig {
	return compiler.MailConfig{
		Connector:   "mail",
		To:          compiler.RestExpr{Literal: "anna@example.org"},
		Subject:     compiler.RestExpr{Literal: "Ihr Zugang"},
		Body:        compiler.RestExpr{Expr: mustExpr(t, `"Hier: " + passwordLink`)},
		PasswordFor: compiler.RestExpr{Literal: " anna "},
		Retries:     3,
	}
}

func TestResolveHandsTheMailItsPasswordLink(t *testing.T) {
	dir := &pwDirectory{link: "https://atlas.example.ch/public/password#dXNy.1.00"}
	_, j := resolveFixtureWith(t, dir, passwordMailConfig(t))
	if j.ResolveError != "" {
		t.Fatalf("resolve error: %s", j.ResolveError)
	}
	if j.Body != "Hier: "+dir.link {
		t.Errorf("body = %q, want the link the directory minted", j.Body)
	}
	if dir.account != "anna" || len(dir.to) != 1 || dir.to[0] != "anna@example.org" || dir.process == "" {
		t.Errorf("asked for %q to %v from %q: want the trimmed account, the resolved recipients and the process", dir.account, dir.to, dir.process)
	}
	if len(j.Withhold) != 1 || j.Withhold[0] != dir.link {
		t.Errorf("withhold = %v, want the link, so a preview does not keep it", j.Withhold)
	}
}

func TestAMailWhosePasswordLinkCannotBeMintedFailsSayingWhy(t *testing.T) {
	for _, c := range []struct {
		name string
		dir  mail.Directory
		want string
	}{
		{"a resolver that mints no password links", stubDirectory{}, "password link"},
		{"a link refused for its recipients", &pwDirectory{err: errors.New("goes to the account's own address and to nobody else")}, "nobody else"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, j := resolveFixtureWith(t, c.dir, passwordMailConfig(t))
			if !strings.Contains(j.ResolveError, c.want) || j.Body != "" {
				t.Fatalf("resolve error = %q, body %q: want it to say %q and send nothing", j.ResolveError, j.Body, c.want)
			}
		})
	}
}

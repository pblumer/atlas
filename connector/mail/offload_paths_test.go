package mail_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/mail"
	"github.com/pblumer/atlas/model"
)

// A recipient that is a reference rather than an address (ADR-0314): looked up at
// send time, and refused when nobody answers to it.

// mapDirectory resolves the references it holds and answers nothing for the rest;
// a reference named in fail makes it fail outright, as an unreachable directory
// would.
type mapDirectory struct {
	known map[string][]string
	fail  string
}

func (d mapDirectory) Recipients(ref string) ([]string, error) {
	if ref == d.fail {
		return nil, errors.New("directory unreachable")
	}
	return d.known[ref], nil
}

// resolveMail runs Resolve against a live instance of a one-task process and returns
// what it answered, error included.
func resolveMail(t *testing.T, dir mail.Directory, cfg compiler.MailConfig) (mail.Job, error) {
	t.Helper()
	log, store := openStore(t)
	cp, jobType := mailProcess(t, cfg)
	var (
		j        mail.Job
		err      error
		captured bool
	)
	driveResolving(t, cp, jobType, store, log, func(ei *model.ElementInstanceValue, elementInstanceKey, jobKey uint64, detail *compiler.ConnectorTaskDetail) {
		j, err = mail.Resolve(store, cp, detail, ei, elementInstanceKey, jobKey, dir)
		captured = true
	})
	if !captured {
		t.Fatal("the mail task never produced a job to resolve")
	}
	return j, err
}

func mailTo(to, cc, bcc string) compiler.MailConfig {
	return compiler.MailConfig{
		Connector: "office365",
		To:        compiler.RestExpr{Literal: to},
		Cc:        compiler.RestExpr{Literal: cc},
		Bcc:       compiler.RestExpr{Literal: bcc},
		Subject:   compiler.RestExpr{Literal: "Hi"},
		Retries:   3,
	}
}

// TestReferencesResolveToAddressesInPlace: an address passes through untouched, a
// reference becomes every address the directory holds for it, and the list keeps the
// order the model wrote — on every recipient line.
func TestReferencesResolveToAddressesInPlace(t *testing.T) {
	dir := mapDirectory{known: map[string][]string{
		"usr_ada": {"ada@example.ch"},
		"grp_ops": {"ops1@example.ch", "ops2@example.ch"},
	}}
	j, err := resolveMail(t, dir, mailTo("usr_ada, team@example.ch", "grp_ops", "audit@example.ch"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := []string{"ada@example.ch", "team@example.ch"}; !reflect.DeepEqual(j.To, want) {
		t.Errorf("To = %v, want %v", j.To, want)
	}
	if want := []string{"ops1@example.ch", "ops2@example.ch"}; !reflect.DeepEqual(j.Cc, want) {
		t.Errorf("Cc = %v, want %v", j.Cc, want)
	}
	if want := []string{"audit@example.ch"}; !reflect.DeepEqual(j.Bcc, want) {
		t.Errorf("Bcc = %v, want %v", j.Bcc, want)
	}
}

// TestAReferenceNobodyAnswersToFailsTheJob: a notification quietly sent to nobody is
// the failure a notification exists to avoid, so each recipient line refuses one,
// naming it.
func TestAReferenceNobodyAnswersToFailsTheJob(t *testing.T) {
	dir := mapDirectory{known: map[string][]string{"usr_ada": {"ada@example.ch"}}}
	for _, tc := range []struct {
		name string
		cfg  compiler.MailConfig
	}{
		{"to", mailTo("usr_ghost", "", "")},
		{"cc", mailTo("usr_ada", "usr_ghost", "")},
		{"bcc", mailTo("usr_ada", "", "usr_ghost")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveMail(t, dir, tc.cfg)
			if err == nil || !strings.Contains(err.Error(), `"usr_ghost" is neither an address nor anybody this server knows`) {
				t.Fatalf("Resolve = %v, want the unknown reference named", err)
			}
		})
	}
}

// TestAnUnreachableDirectoryFailsTheJob: a lookup that could not be made is not the
// same as nobody, and the job says which reference it was trying.
func TestAnUnreachableDirectoryFailsTheJob(t *testing.T) {
	_, err := resolveMail(t, mapDirectory{fail: "usr_ada"}, mailTo("usr_ada", "", ""))
	if err == nil || !strings.Contains(err.Error(), `mail: recipient "usr_ada": directory unreachable`) {
		t.Fatalf("Resolve = %v, want the directory's failure with the reference", err)
	}
}

// TestResolveWithoutADetailIsRefused: a job whose task carries no mail detail has
// nothing to send; it is refused before any variable is read.
func TestResolveWithoutADetailIsRefused(t *testing.T) {
	_, err := mail.Resolve(nil, nil, nil, nil, 0, 0, nil)
	if err == nil || !strings.Contains(err.Error(), "task has no detail") {
		t.Fatalf("Resolve(nil detail) = %v, want the refusal", err)
	}
}

// TestAForwardResolvesItsRecipientsLikeASend: a forward addresses people the way a
// send does, so a reference becomes its addresses at the moment the forward goes out
// (ADR-0478).
func TestAForwardResolvesItsRecipientsLikeASend(t *testing.T) {
	dir := mapDirectory{known: map[string][]string{"grp_ablage": {"ablage@example.ch", "archiv@example.ch"}}}
	j, err := resolveMail(t, dir, compiler.MailConfig{
		Connector: "office365", Operation: mail.OpForward,
		Message: compiler.RestExpr{Literal: " m-1 "},
		To:      compiler.RestExpr{Literal: "grp_ablage"}, Bcc: compiler.RestExpr{Literal: "audit@example.ch"},
		Body: compiler.RestExpr{Literal: "Zur Ablage"}, Retries: 3,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := []string{"ablage@example.ch", "archiv@example.ch"}; !reflect.DeepEqual(j.To, want) || j.Bcc[0] != "audit@example.ch" {
		t.Errorf("To = %v, Bcc = %v", j.To, j.Bcc)
	}
	if j.Target != "m-1" || j.Operation != mail.OpForward || j.Body != "Zur Ablage" || j.ErrorCodes != nil {
		t.Errorf("job = %+v", j)
	}
	if _, err := resolveMail(t, dir, compiler.MailConfig{
		Connector: "office365", Operation: mail.OpForward, Message: compiler.RestExpr{Literal: "m"},
		To: compiler.RestExpr{Literal: "usr_ghost"}, Retries: 3,
	}); err == nil || !strings.Contains(err.Error(), "usr_ghost") {
		t.Errorf("a forward to nobody = %v", err)
	}
}

// TestAnExportResolvesWhereItsDocumentGoes: the store is a name the model wrote, the
// bucket and key are evaluated, the accepted types travel as the compiler normalized
// them, and nothing about the store's credential is in the job.
func TestAnExportResolvesWhereItsDocumentGoes(t *testing.T) {
	j, err := resolveMail(t, nil, compiler.MailConfig{
		Connector: "office365", Operation: mail.OpExportAttachment,
		Message: compiler.RestExpr{Literal: "m-1"}, Attachment: compiler.RestExpr{Literal: " 2 "},
		AllowedTypes: []string{"application/pdf"}, Store: "belege",
		Bucket: compiler.RestExpr{Literal: " eingang "}, Key: compiler.RestExpr{Literal: "a/b.pdf"}, Retries: 3,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if j.Attachment != "2" || j.Store != "belege" || j.Bucket != "eingang" || j.Key != "a/b.pdf" ||
		!reflect.DeepEqual(j.AllowedTypes, []string{"application/pdf"}) {
		t.Errorf("job = %+v", j)
	}
}

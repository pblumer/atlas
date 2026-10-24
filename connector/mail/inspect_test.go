package mail

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// zipOf builds an archive of the named entries; encrypt marks the first entry as
// encrypted, the way a password-protected zip announces it.
func zipOf(t *testing.T, encrypt bool, names ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i, n := range names {
		h := &zip.FileHeader{Name: n, Method: zip.Store}
		if encrypt && i == 0 {
			h.Flags |= 0x1
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("content of " + n))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

var (
	cleanPDF = []byte("%PDF-1.7\n1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n%%EOF")
	cleanPNG = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
)

func TestInspectionRefusesTheShapesMalwareArrivesIn(t *testing.T) {
	ole := append(append([]byte{}, oleMagic...), utf16le("_VBA_PROJECT")...)
	for _, tc := range []struct {
		name, file string
		data       []byte
		want       string
	}{
		{"a program by name", "rechnung.exe", cleanPDF, "programs, scripts"},
		{"a name Windows trims", "rechnung.pdf.exe. ", cleanPDF, "programs, scripts"},
		{"a program under a document's name", "rechnung.pdf", []byte("MZ\x90\x00 this program"), "is a program"},
		{"an ELF program", "daten.bin", []byte("\x7fELF\x02\x01"), "is a program"},
		{"a shortcut's content", "foto.jpg", []byte{0x4C, 0x00, 0x00, 0x00, 0x01, 0x14, 0x02, 0x00, 0, 0}, "is a program"},
		{"a script's content", "notiz.txt", []byte("#!/bin/sh\nrm -rf /\n"), "is a script"},
		{"a name that reverses itself", "rechnung‮fdp.exe", cleanPDF, "bidirectional"},
		{"a macro document by name", "budget.xlsm", zipOf(t, false, "xl/workbook.xml"), "programs, scripts"},
		{"a legacy macro document", "budget.xls", ole, "macros"},
		{"an OOXML document with a VBA project", "budget.xlsx", zipOf(t, false, "xl/workbook.xml", "xl/vbaProject.bin"), "macros"},
		{"an encrypted archive", "scan.zip", zipOf(t, true, "scan.pdf"), "encrypted archive"},
		{"an archive carrying a program", "scan.zip", zipOf(t, false, "readme.txt", "docs/scan.pdf.exe"), "contains scan.pdf.exe"},
		{"a Java program", "tool.zip", zipOf(t, false, "META-INF/MANIFEST.MF", "a/Main.class"), "Java program"},
		{"an Android app", "app.zip", zipOf(t, false, "AndroidManifest.xml", "classes.dex"), "Android app"},
		{"a PDF that runs JavaScript", "form.pdf", []byte("%PDF-1.4\n<< /OpenAction << /S /JavaScript /JS (app.alert(1)) >> >>"), "runs JavaScript"},
		{"a PDF hiding JavaScript in escapes", "form.pdf", []byte("%PDF-1.4\n<< /S /J#61vaScript >>"), "runs JavaScript"},
		{"a PDF that launches a program", "form.pdf", []byte("%PDF-1.4\n<< /S /Launch /F (cmd.exe) >>"), "launches a program"},
		{"a document that is not what its name says", "rechnung.pdf", []byte("plain text, no PDF at all"), "not a PDF"},
		{"an image that is not one", "foto.jpg", []byte("<html><script>x</script></html>"), "not an image"},
	} {
		_, err := inspector{}.inspect(context.Background(), Document{Name: tc.file, Data: tc.data})
		var rej *Rejection
		if !errors.As(err, &rej) || !strings.Contains(rej.Reason, tc.want) {
			t.Errorf("%s: %v, want a rejection saying %q", tc.name, err, tc.want)
		}
	}
}

func TestInspectionPassesDocumentsAndSaysWhatTheyAre(t *testing.T) {
	for _, tc := range []struct {
		file   string
		data   []byte
		want   string
		active bool
	}{
		{"rechnung.pdf", cleanPDF, "application/pdf", false},
		{"Scan.PNG", cleanPNG, "image/png", false},
		{"vertrag.docx", zipOf(t, false, "[Content_Types].xml", "word/document.xml"), "application/vnd.openxmlformats-officedocument.wordprocessingml.document", false},
		{"belege.zip", zipOf(t, false, "a.pdf", "b.csv"), "application/zip", false},
		{"liste.csv", []byte("a;b;c\n1;2;3\n"), "text/plain", false},
		{"quittung.html", []byte("<!DOCTYPE html><html><body>Danke</body></html>"), "text/html", true},
		{"logo.svg", []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`), "image/svg+xml", true},
		{"", cleanPDF, "application/pdf", false},
		{"personal.csv", []byte("MZ-Nummer;Name\n4711;Muster\n"), "text/plain", false},
		{"plan.pdf", append([]byte("%PDF-1.7\n% <svg> in a comment\n"), cleanPDF[9:]...), "application/pdf", false},
	} {
		v, err := inspector{}.inspect(context.Background(), Document{Name: tc.file, Data: tc.data})
		if err != nil {
			t.Errorf("%s: %v", tc.file, err)
			continue
		}
		if v.DetectedType != tc.want || v.Active != tc.active || v.Scanned {
			t.Errorf("%s: verdict %+v, want %s active=%v unscanned", tc.file, v, tc.want, tc.active)
		}
	}
}

func TestAllowedTypesAreJudgedFromTheContent(t *testing.T) {
	in := inspector{allowed: SplitTypes("application/pdf, IMAGE/*")}
	if _, err := in.inspect(context.Background(), Document{Name: "a.pdf", Data: cleanPDF}); err != nil {
		t.Errorf("a PDF where PDFs are allowed: %v", err)
	}
	if _, err := in.inspect(context.Background(), Document{Name: "b.png", Data: cleanPNG}); err != nil {
		t.Errorf("a PNG where image/* is allowed: %v", err)
	}
	_, err := in.inspect(context.Background(), Document{Name: "c.csv", ContentType: "application/pdf", Data: []byte("a;b\n")})
	var rej *Rejection
	if !errors.As(err, &rej) || !strings.Contains(rej.Reason, "text/plain, which this task does not accept") {
		t.Errorf("a CSV that claims to be a PDF = %v; the claim must not count", err)
	}
	if got := SplitTypes(" , a/b ,"); len(got) != 1 || got[0] != "a/b" {
		t.Errorf("SplitTypes = %v", got)
	}
}

// fakeScanner answers a fixed verdict.
type fakeScanner struct {
	sig     string
	err     error
	scanned [][]byte
}

func (f *fakeScanner) Name() string { return "fake" }
func (f *fakeScanner) Scan(_ context.Context, data []byte) (string, error) {
	f.scanned = append(f.scanned, data)
	return f.sig, f.err
}
func (f *fakeScanner) Ping(context.Context) error { return f.err }

func TestTheScannerDecidesAfterThePolicy(t *testing.T) {
	ctx := context.Background()
	clean := &fakeScanner{}
	v, err := inspector{scanner: clean}.inspect(ctx, Document{Name: "a.pdf", Data: cleanPDF})
	if err != nil || !v.Scanned || v.Scanner != "fake" || len(clean.scanned) != 1 {
		t.Errorf("a clean scan = %+v, %v (scanned %d)", v, err, len(clean.scanned))
	}
	if f := v.Fields(); f["scanned"] != true || f["detectedType"] != "application/pdf" || f["active"] != false {
		t.Errorf("verdict fields = %v", f)
	}

	infected := &fakeScanner{sig: "Eicar-Signature"}
	_, err = inspector{scanner: infected}.inspect(ctx, Document{Name: "a.pdf", Data: cleanPDF})
	var rej *Rejection
	if !errors.As(err, &rej) || !strings.Contains(rej.Reason, "found Eicar-Signature") {
		t.Errorf("a finding = %v, want a rejection naming the signature", err)
	}

	down := &fakeScanner{err: errors.New("connection refused")}
	_, err = inspector{scanner: down}.inspect(ctx, Document{Name: "a.pdf", Data: cleanPDF})
	if err == nil || errors.As(err, &rej) || !strings.Contains(err.Error(), "could not be scanned") {
		t.Errorf("a scanner that cannot answer = %v, want a failure that is not a rejection and not a pass", err)
	}

	policyFirst := &fakeScanner{}
	_, _ = inspector{scanner: policyFirst}.inspect(ctx, Document{Name: "x.exe", Data: cleanPDF})
	if len(policyFirst.scanned) != 0 {
		t.Error("a document the policy refused was still sent to the scanner")
	}
}

// fakeClamd serves clamd's z-commands on a TCP port: INSTREAM answers verdict once the
// stream ends, PING answers PONG.
func fakeClamd(t *testing.T, verdict string) (addr string, got *bytes.Buffer) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	got = &bytes.Buffer{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				cmd := make([]byte, 0, 16)
				one := make([]byte, 1)
				for {
					if _, err := io.ReadFull(c, one); err != nil {
						return
					}
					if one[0] == 0 {
						break
					}
					cmd = append(cmd, one[0])
				}
				switch string(cmd) {
				case "zPING":
					_, _ = c.Write([]byte("PONG\x00"))
				case "zINSTREAM":
					var size [4]byte
					for {
						if _, err := io.ReadFull(c, size[:]); err != nil {
							return
						}
						n := binary.BigEndian.Uint32(size[:])
						if n == 0 {
							break
						}
						chunk := make([]byte, n)
						if _, err := io.ReadFull(c, chunk); err != nil {
							return
						}
						got.Write(chunk)
					}
					_, _ = c.Write([]byte(verdict + "\x00"))
				}
			}(conn)
		}
	}()
	return ln.Addr().String(), got
}

func TestClamdINSTREAM(t *testing.T) {
	ctx := context.Background()
	big := bytes.Repeat([]byte("A"), 3*clamdChunk+17) // more than one chunk, and a ragged last one
	addr, got := fakeClamd(t, "stream: OK")
	sc, err := NewScanner("clamd://" + addr)
	if err != nil {
		t.Fatal(err)
	}
	if sig, err := sc.Scan(ctx, big); err != nil || sig != "" {
		t.Errorf("clean = %q, %v", sig, err)
	}
	if !bytes.Equal(got.Bytes(), big) {
		t.Errorf("clamd received %d bytes, want the %d sent", got.Len(), len(big))
	}
	if err := sc.Ping(ctx); err != nil {
		t.Errorf("ping: %v", err)
	}

	addr, _ = fakeClamd(t, "stream: Win.Test.EICAR_HDB-1 FOUND")
	sc, _ = NewScanner(addr) // a bare host:port is clamd over TCP
	if sig, err := sc.Scan(ctx, []byte("x")); err != nil || sig != "Win.Test.EICAR_HDB-1" {
		t.Errorf("found = %q, %v", sig, err)
	}

	addr, _ = fakeClamd(t, "INSTREAM size limit exceeded. ERROR")
	sc, _ = NewScanner("clamd://" + addr)
	if sig, err := sc.Scan(ctx, []byte("x")); err == nil || sig != "" {
		t.Errorf("an ERROR answer = %q, %v; it must never read as clean", sig, err)
	}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	_ = ln.Close()
	sc, _ = NewScanner("clamd://" + dead)
	if _, err := sc.Scan(ctx, []byte("x")); err == nil {
		t.Error("an unreachable clamd answered a scan")
	}
	if err := sc.Ping(ctx); err == nil {
		t.Error("an unreachable clamd answered a ping")
	}
}

func TestNormalizeScannerEndpoint(t *testing.T) {
	for in, want := range map[string]string{
		"":                                "",
		"clamav.intern":                   "clamd://clamav.intern:3310",
		"clamd://clamav.intern":           "clamd://clamav.intern:3310",
		"CLAMD://10.0.0.5:3311":           "clamd://10.0.0.5:3311",
		"clamd:///run/clamav/clamd.ctl":   "clamd:///run/clamav/clamd.ctl",
		"  clamd://[::1]:3310  ":          "clamd://[::1]:3310",
		"clamd://clamav.intern:3310/path": "clamd://clamav.intern:3310",
	} {
		if got, err := NormalizeScannerEndpoint(in); err != nil || got != want {
			t.Errorf("%q = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"icap://av:1344", "clamd://", "clamd:///"} {
		if _, err := NormalizeScannerEndpoint(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	if sc, err := NewScanner(""); sc != nil || err != nil {
		t.Errorf("no endpoint builds no scanner: %v, %v", sc, err)
	}
	sc, _ := NewScanner("clamd:///run/clamav/clamd.ctl")
	if c := sc.(*clamdScanner); c.network != "unix" || c.addr != "/run/clamav/clamd.ctl" {
		t.Errorf("socket scanner = %+v", c)
	}
}

// probeClient is a Client whose check answers err.
type probeClient struct{ err error }

func (probeClient) Send(context.Context, Message) error { return nil }
func (p probeClient) Probe(context.Context) error       { return p.err }

func TestAWorkerWithAScannerKeepsItsAbilities(t *testing.T) {
	if WithScanner(probeClient{}, nil) != (probeClient{}) {
		t.Error("no scanner must leave the client as it is")
	}
	sc := &fakeScanner{}
	c := WithScanner(probeClient{}, sc)
	if ScannerOf(c) != sc || ScannerOf(probeClient{}) != nil {
		t.Error("ScannerOf does not find the Worker's scanner")
	}
	if _, err := MailboxOf(c); !errors.Is(err, errPreviewHasNoMailbox) {
		t.Errorf("a wrapped client without a mailbox = %v", err)
	}
	if err := Probe(context.Background(), c); err != nil {
		t.Errorf("probe = %v", err)
	}
	if err := Probe(context.Background(), WithScanner(probeClient{}, &fakeScanner{err: errors.New("down")})); err == nil ||
		!strings.Contains(err.Error(), "scanner does not answer") {
		t.Errorf("a Worker whose scanner is down passed its check: %v", err)
	}
	if err := Probe(context.Background(), WithScanner(probeClient{err: errors.New("bad password")}, sc)); err == nil ||
		!strings.Contains(err.Error(), "bad password") {
		t.Errorf("the provider's own check must come first: %v", err)
	}
	smtp := NewSMTPClient(Connector{Endpoint: "smtp.example.com:587"})
	smtp.attachIMAP("imaps://imap.example.com:993", "me", "pw")
	if mb, err := MailboxOf(WithScanner(smtp, sc)); err != nil || mb == nil {
		t.Errorf("a wrapped SMTP Worker lost its mailbox: %v", err)
	}
}

func TestAProviderWorkerNamingABadScannerIsRefused(t *testing.T) {
	if _, err := NewProviderClient(ProviderConfig{Provider: ProviderSMTP, Endpoint: "smtp.example.com:587", Sender: "a@b.ch", Scanner: "icap://x"}); err == nil {
		t.Error("a Worker whose scanner cannot be built must not start without it")
	}
	c, err := NewProviderClient(ProviderConfig{Provider: ProviderSMTP, Endpoint: "smtp.example.com:587", Sender: "a@b.ch", Scanner: "clamav:3310"})
	if err != nil || ScannerOf(c) == nil {
		t.Errorf("a Worker naming clamd = %v, scanner %v", err, ScannerOf(c))
	}
}

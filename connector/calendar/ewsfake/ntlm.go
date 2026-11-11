package ewsfake

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"strings"
	"unicode/utf16"

	"golang.org/x/crypto/md4" //nolint:staticcheck // NTLM is defined over MD4; a server has to compute it.
)

// The server side of NTLMv2 (MS-NLMP), as much of it as verifying a client takes: it
// sends a challenge and checks the client's NTProofStr against the password it knows.
// It does no signing, sealing or key exchange; an HTTP server authenticating a request
// does none either.

const (
	flagUnicode     = 1 << 0
	flagReqTarget   = 1 << 2
	flagNTLM        = 1 << 9
	flagTargetTypeD = 1 << 16
	flagExtSession  = 1 << 19
	flagTargetInfo  = 1 << 23
)

var ntlmSignature = []byte("NTLMSSP\x00")

func utf16le(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(u))
	for i, r := range u {
		binary.LittleEndian.PutUint16(b[2*i:], r)
	}
	return b
}

func fromUTF16le(b []byte) string {
	if len(b)%2 != 0 {
		return ""
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[2*i:])
	}
	return string(utf16.Decode(u))
}

// ntlmMessageType is the type of an NTLM message, or 0 when it is none.
func ntlmMessageType(msg []byte) uint32 {
	if len(msg) < 12 || !bytes.Equal(msg[:8], ntlmSignature) {
		return 0
	}
	return binary.LittleEndian.Uint32(msg[8:12])
}

// challengeMessage is the CHALLENGE (type 2) the fake answers every NEGOTIATE with:
// NTLMv2 with extended session security, the domain as target name and target info.
func challengeMessage(serverChallenge [8]byte, domain string) []byte {
	target := utf16le(domain)
	var info bytes.Buffer
	for _, av := range []struct {
		id  uint16
		val []byte
	}{{2, target}, {1, utf16le("EXCH01")}} {
		_ = binary.Write(&info, binary.LittleEndian, av.id)
		_ = binary.Write(&info, binary.LittleEndian, uint16(len(av.val)))
		info.Write(av.val)
	}
	info.Write([]byte{0, 0, 0, 0}) // MsvAvEOL

	const head = 48
	var b bytes.Buffer
	b.Write(ntlmSignature)
	_ = binary.Write(&b, binary.LittleEndian, uint32(2))
	_ = binary.Write(&b, binary.LittleEndian, uint16(len(target)))
	_ = binary.Write(&b, binary.LittleEndian, uint16(len(target)))
	_ = binary.Write(&b, binary.LittleEndian, uint32(head))
	_ = binary.Write(&b, binary.LittleEndian, uint32(flagUnicode|flagReqTarget|flagNTLM|flagTargetTypeD|flagExtSession|flagTargetInfo))
	b.Write(serverChallenge[:])
	b.Write(make([]byte, 8))
	_ = binary.Write(&b, binary.LittleEndian, uint16(info.Len()))
	_ = binary.Write(&b, binary.LittleEndian, uint16(info.Len()))
	_ = binary.Write(&b, binary.LittleEndian, uint32(head+len(target)))
	b.Write(target)
	b.Write(info.Bytes())
	return b.Bytes()
}

func field(msg []byte, at int) []byte {
	if len(msg) < at+8 {
		return nil
	}
	l := int(binary.LittleEndian.Uint16(msg[at:]))
	off := int(binary.LittleEndian.Uint32(msg[at+4:]))
	if off < 0 || off+l > len(msg) {
		return nil
	}
	return msg[off : off+l]
}

func hmacMD5(key []byte, data ...[]byte) []byte {
	m := hmac.New(md5.New, key)
	for _, d := range data {
		m.Write(d)
	}
	return m.Sum(nil)
}

// verifyAuthenticate checks an AUTHENTICATE (type 3) message: the user it names is the
// account's, and its NTLMv2 response was computed from the account's password over the
// challenge the fake sent. It answers the user name as the client wrote it.
func verifyAuthenticate(msg []byte, serverChallenge [8]byte, username, password string) bool {
	if ntlmMessageType(msg) != 3 {
		return false
	}
	nt := field(msg, 20)
	domain := fromUTF16le(field(msg, 28))
	user := fromUTF16le(field(msg, 36))
	if len(nt) < 16+28 {
		return false // an NTLMv1 response, or none
	}
	wantUser, wantDomain := username, ""
	if d, u, ok := strings.Cut(username, `\`); ok {
		wantDomain, wantUser = d, u
	}
	if !strings.EqualFold(user, wantUser) || !strings.EqualFold(domain, wantDomain) {
		return false
	}
	h := md4.New()
	h.Write(utf16le(password))
	v2 := hmacMD5(h.Sum(nil), utf16le(strings.ToUpper(user)+domain))
	proof := hmacMD5(v2, serverChallenge[:], nt[16:])
	return hmac.Equal(proof, nt[:16])
}

// ntlmToken decodes the token of an "NTLM …" or "Negotiate …" Authorization header.
func ntlmToken(header string) []byte {
	scheme, tok, ok := strings.Cut(header, " ")
	if !ok || (scheme != "NTLM" && scheme != "Negotiate") {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(tok))
	if err != nil {
		return nil
	}
	return raw
}

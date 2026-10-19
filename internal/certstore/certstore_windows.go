//go:build windows

package certstore

import (
	"crypto"
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const supported = true

var (
	modncrypt            = windows.NewLazySystemDLL("ncrypt.dll")
	procNCryptSignHash   = modncrypt.NewProc("NCryptSignHash")
	procNCryptFreeObject = modncrypt.NewProc("NCryptFreeObject")
)

// CNG's names for what a PKCS #1 v1.5 signature passes (bcrypt.h, ncrypt.h).
const (
	bcryptPadPKCS1   = 0x00000002
	ncryptSilentFlag = 0x00000040
)

// bcryptPKCS1PaddingInfo is BCRYPT_PKCS1_PADDING_INFO: the hash the digest was made with.
type bcryptPKCS1PaddingInfo struct {
	algID *uint16
}

// hashNames are CNG's names for the hashes a PKCS #1 v1.5 signature may name.
var hashNames = map[crypto.Hash]string{
	crypto.SHA1:   "SHA1",
	crypto.SHA256: "SHA256",
	crypto.SHA384: "SHA384",
	crypto.SHA512: "SHA512",
}

// locations are searched in this order — the order Connect-MicrosoftTeams and the
// Teams Phone Worker use, so one thumbprint names one certificate for all of them.
var locations = []struct {
	name string
	flag uint32
}{
	{`CurrentUser\My`, windows.CERT_SYSTEM_STORE_CURRENT_USER},
	{`LocalMachine\My`, windows.CERT_SYSTEM_STORE_LOCAL_MACHINE},
}

func open(tp [20]byte) (*x509.Certificate, crypto.Signer, error) {
	for _, loc := range locations {
		var der []byte
		found, err := withCertificate(loc.flag, tp, func(ctx *windows.CertContext) error {
			der = append([]byte(nil), unsafe.Slice(ctx.EncodedCert, ctx.Length)...)
			return nil
		})
		if err != nil || !found {
			continue // a store this account cannot open holds nothing it could use
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, nil, fmt.Errorf("certstore: the certificate in %s cannot be read: %w", loc.name, err)
		}
		s := &storeSigner{tp: tp, location: loc.flag, where: loc.name, pub: cert.PublicKey}
		if err := verify(cert, s); err != nil {
			return nil, nil, fmt.Errorf("certstore: the certificate in %s of %s: %w", loc.name, accountName(), err)
		}
		return cert, s, nil
	}
	return nil, nil, fmt.Errorf("certstore: no certificate with this thumbprint in CurrentUser\\My or LocalMachine\\My of %s, the account this process runs as", accountName())
}

// withCertificate opens one My store read-only, finds the certificate with thumbprint
// tp and hands it to use, releasing both afterwards. Nothing is held between calls: a
// signer that opens the store per signature cannot outlive a renewed or removed
// certificate with a stale handle, and it signs once an hour at most.
func withCertificate(location uint32, tp [20]byte, use func(*windows.CertContext) error) (bool, error) {
	name, err := windows.UTF16PtrFromString("MY")
	if err != nil {
		return false, err
	}
	store, err := windows.CertOpenStore(windows.CERT_STORE_PROV_SYSTEM_W, 0, 0,
		location|windows.CERT_STORE_OPEN_EXISTING_FLAG|windows.CERT_STORE_READONLY_FLAG, uintptr(unsafe.Pointer(name)))
	runtime.KeepAlive(name)
	if err != nil {
		return false, err
	}
	defer windows.CertCloseStore(store, 0)
	blob := windows.CryptHashBlob{Size: uint32(len(tp)), Data: &tp[0]}
	ctx, err := windows.CertFindCertificateInStore(store, windows.X509_ASN_ENCODING|windows.PKCS_7_ASN_ENCODING,
		0, windows.CERT_FIND_HASH, unsafe.Pointer(&blob), nil)
	if err != nil {
		return false, nil // CRYPT_E_NOT_FOUND: not in this store
	}
	defer windows.CertFreeCertificateContext(ctx)
	return true, use(ctx)
}

// storeSigner signs with the key of the certificate it names, through CNG. The key is
// never read: CNG is handed the digest and answers the signature.
type storeSigner struct {
	tp       [20]byte
	location uint32
	where    string
	pub      crypto.PublicKey
}

func (s *storeSigner) Public() crypto.PublicKey { return s.pub }

func (s *storeSigner) Sign(_ io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	if _, pss := opts.(*rsa.PSSOptions); pss {
		return nil, errors.New("certstore: only PKCS #1 v1.5 signatures are supported")
	}
	alg, ok := hashNames[opts.HashFunc()]
	if !ok || len(digest) != opts.HashFunc().Size() {
		return nil, fmt.Errorf("certstore: cannot sign a %d-byte digest of %v", len(digest), opts.HashFunc())
	}
	var sig []byte
	found, err := withCertificate(s.location, s.tp, func(ctx *windows.CertContext) error {
		var key windows.Handle
		var spec uint32
		var mustFree bool
		// Silent: a service has nobody to show a PIN or consent prompt to, so a key
		// that would ask fails instead of hanging. Only CNG: every key a current
		// Windows creates or imports is one, and CNG opens the legacy software ones too.
		if err := windows.CryptAcquireCertificatePrivateKey(ctx,
			windows.CRYPT_ACQUIRE_ONLY_NCRYPT_KEY_FLAG|windows.CRYPT_ACQUIRE_SILENT_FLAG, nil, &key, &spec, &mustFree); err != nil {
			return fmt.Errorf("no access to the private key — the account needs read permission on it (Manage Private Keys): %w", err)
		}
		if mustFree {
			defer procNCryptFreeObject.Call(uintptr(key))
		}
		if spec != windows.CERT_NCRYPT_KEY_SPEC {
			return errors.New("the private key is not a CNG key")
		}
		var err error
		sig, err = ncryptSign(key, alg, digest)
		return err
	})
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("certstore: the certificate is no longer in %s", s.where)
	}
	return sig, nil
}

// ncryptSign asks CNG for a PKCS #1 v1.5 signature over digest: once for its length,
// once for the signature.
func ncryptSign(key windows.Handle, alg string, digest []byte) ([]byte, error) {
	algID, err := windows.UTF16PtrFromString(alg)
	if err != nil {
		return nil, err
	}
	pad := bcryptPKCS1PaddingInfo{algID: algID}
	var size uint32
	r, _, _ := procNCryptSignHash.Call(uintptr(key), uintptr(unsafe.Pointer(&pad)),
		uintptr(unsafe.Pointer(&digest[0])), uintptr(len(digest)), 0, 0, uintptr(unsafe.Pointer(&size)),
		bcryptPadPKCS1|ncryptSilentFlag)
	// SECURITY_STATUS is 32 bits; the upper half of the register carries nothing.
	if status := uint32(r); status != 0 {
		return nil, fmt.Errorf("NCryptSignHash: %w", windows.Errno(status))
	}
	sig := make([]byte, size)
	r, _, _ = procNCryptSignHash.Call(uintptr(key), uintptr(unsafe.Pointer(&pad)),
		uintptr(unsafe.Pointer(&digest[0])), uintptr(len(digest)), uintptr(unsafe.Pointer(&sig[0])), uintptr(size),
		uintptr(unsafe.Pointer(&size)), bcryptPadPKCS1|ncryptSilentFlag)
	if status := uint32(r); status != 0 {
		return nil, fmt.Errorf("NCryptSignHash: %w", windows.Errno(status))
	}
	runtime.KeepAlive(algID)
	return sig[:size], nil
}

// accountName is the account whose CurrentUser store was searched — for a service, its
// service account, which is the first thing to check when a certificate is not found.
func accountName() string {
	n := uint32(256)
	buf := make([]uint16, n)
	if err := windows.GetUserNameEx(windows.NameSamCompatible, &buf[0], &n); err != nil {
		return "this account"
	}
	return windows.UTF16ToString(buf[:n])
}

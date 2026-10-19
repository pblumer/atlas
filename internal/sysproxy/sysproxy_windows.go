//go:build windows

package sysproxy

import (
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// WinHTTP's names for what this file passes and reads (winhttp.h).
const (
	winhttpAccessTypeNoProxy    = 1
	winhttpAccessTypeNamedProxy = 3

	winhttpAutoProxyAutoDetect = 0x00000001
	winhttpAutoProxyConfigURL  = 0x00000002
	winhttpAutoDetectTypeDHCP  = 0x00000001
	winhttpAutoDetectTypeDNSA  = 0x00000002

	errorWinHTTPLoginFailure        = 12015
	errorWinHTTPAutoDetectionFailed = 12180
)

var (
	modwinhttp  = windows.NewLazySystemDLL("winhttp.dll")
	modkernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procWinHttpGetIEProxyConfigForCurrentUser = modwinhttp.NewProc("WinHttpGetIEProxyConfigForCurrentUser")
	procWinHttpOpen                           = modwinhttp.NewProc("WinHttpOpen")
	procWinHttpSetTimeouts                    = modwinhttp.NewProc("WinHttpSetTimeouts")
	procWinHttpGetProxyForUrl                 = modwinhttp.NewProc("WinHttpGetProxyForUrl")
	procGlobalFree                            = modkernel32.NewProc("GlobalFree")
)

// ieProxyConfig is WINHTTP_CURRENT_USER_IE_PROXY_CONFIG.
type ieProxyConfig struct {
	autoDetect    int32
	autoConfigURL *uint16
	proxy         *uint16
	proxyBypass   *uint16
}

// autoProxyOptions is WINHTTP_AUTOPROXY_OPTIONS.
type autoProxyOptions struct {
	flags                 uint32
	autoDetectFlags       uint32
	autoConfigURL         *uint16
	reserved              uintptr
	reservedDword         uint32
	autoLogonIfChallenged int32
}

// proxyInfo is WINHTTP_PROXY_INFO.
type proxyInfo struct {
	accessType  uint32
	proxy       *uint16
	proxyBypass *uint16
}

// platform is WinHTTP, the component .NET's HttpWindowsProxy asks too — which is what
// makes a Go Worker take the route a PowerShell on the same host takes. A Windows without
// it (none ships so) leaves the variables as the only source.
func platform() backend {
	for _, p := range []*windows.LazyProc{procWinHttpGetIEProxyConfigForCurrentUser, procWinHttpOpen,
		procWinHttpSetTimeouts, procWinHttpGetProxyForUrl, procGlobalFree} {
		if p.Find() != nil {
			return nil
		}
	}
	return &winHTTP{}
}

type winHTTP struct {
	once    sync.Once
	session uintptr
	openErr error
}

// readSettings reads the Internet Options of the account this process runs as — for a
// service, the service account's, which a group policy usually sets per machine.
func (w *winHTTP) readSettings() (Settings, error) {
	var c ieProxyConfig
	if r, _, err := procWinHttpGetIEProxyConfigForCurrentUser.Call(uintptr(unsafe.Pointer(&c))); r == 0 {
		return Settings{}, err
	}
	defer globalFree(c.autoConfigURL, c.proxy, c.proxyBypass)
	return Settings{
		AutoDetect:    c.autoDetect != 0,
		AutoConfigURL: windows.UTF16PtrToString(c.autoConfigURL),
		Proxy:         windows.UTF16PtrToString(c.proxy),
		Bypass:        windows.UTF16PtrToString(c.proxyBypass),
	}, nil
}

// open starts the one WinHTTP session auto-configuration runs in, for the life of the
// process. It is synchronous and goes direct itself: it only ever fetches a PAC script.
// The timeouts bound that fetch, so a PAC server that hangs delays a request by seconds
// instead of holding it.
func (w *winHTTP) open() (uintptr, error) {
	w.once.Do(func() {
		agent, err := windows.UTF16PtrFromString("atlas")
		if err != nil {
			w.openErr = err
			return
		}
		h, _, err := procWinHttpOpen.Call(uintptr(unsafe.Pointer(agent)), winhttpAccessTypeNoProxy, 0, 0, 0)
		if h == 0 {
			w.openErr = err
			return
		}
		procWinHttpSetTimeouts.Call(h, 5000, 5000, 5000, 10000)
		w.session = h
	})
	return w.session, w.openErr
}

func (w *winHTTP) autoProxy(target string, autoDetect bool, configURL string) (string, error) {
	session, err := w.open()
	if err != nil {
		return "", err
	}
	u, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return "", err
	}
	var opts autoProxyOptions
	if autoDetect {
		opts.flags |= winhttpAutoProxyAutoDetect
		opts.autoDetectFlags = winhttpAutoDetectTypeDHCP | winhttpAutoDetectTypeDNSA
	}
	if configURL != "" {
		pac, err := windows.UTF16PtrFromString(configURL)
		if err != nil {
			return "", err
		}
		opts.flags |= winhttpAutoProxyConfigURL
		opts.autoConfigURL = pac
	}
	for retried := false; ; retried = true {
		var info proxyInfo
		r, _, callErr := procWinHttpGetProxyForUrl.Call(session, uintptr(unsafe.Pointer(u)),
			uintptr(unsafe.Pointer(&opts)), uintptr(unsafe.Pointer(&info)))
		if r != 0 {
			defer globalFree(info.proxy, info.proxyBypass)
			if info.accessType == winhttpAccessTypeNamedProxy {
				return windows.UTF16PtrToString(info.proxy), nil
			}
			return "", nil
		}
		switch errno, _ := callErr.(windows.Errno); {
		case errno == errorWinHTTPLoginFailure && !retried:
			// A PAC server that asks who is calling: .NET then retries once with the
			// account's own logon, and so does this.
			opts.autoLogonIfChallenged = 1
		case errno == errorWinHTTPAutoDetectionFailed:
			return "", errAutoDetectFailed
		default:
			return "", callErr
		}
	}
}

// globalFree releases the strings WinHTTP allocated for its answers.
func globalFree(ps ...*uint16) {
	for _, p := range ps {
		if p != nil {
			procGlobalFree.Call(uintptr(unsafe.Pointer(p)))
		}
	}
}

package main

import (
	"log/slog"

	"github.com/pblumer/atlas/internal/sysproxy"
	"github.com/pblumer/atlas/logging"
)

// installOutboundProxy makes this process's outbound HTTP take the route .NET takes on
// the same host — a proxy variable when one is set, otherwise on Windows the system's
// proxy settings (ADR-draft-windows-system-proxy) — and logs which it took. Called once,
// after logging is set up and before the first request; every client that leaves its
// transport to the default inherits the choice, every Worker Type's among them.
func installOutboundProxy(getenv func(string) string) {
	mode, s := sysproxy.Install(getenv)
	logging.Info(logging.OutboundProxySelected, "outbound HTTP proxy chosen", outboundProxyAttrs(mode, s)...)
}

// outboundProxyAttrs is what the startup line says: the source, and for the Windows
// settings the settings themselves, without any credentials a proxy address carries.
func outboundProxyAttrs(mode sysproxy.Mode, s sysproxy.Settings) []slog.Attr {
	attrs := []slog.Attr{slog.String("source", string(mode))}
	if mode == sysproxy.ModeWindows {
		r := s.Redacted()
		attrs = append(attrs, slog.Bool("autoDetect", r.AutoDetect), slog.String("pacUrl", r.AutoConfigURL),
			slog.String("proxy", r.Proxy), slog.String("bypass", r.Bypass))
	}
	return attrs
}

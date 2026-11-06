package main

import "github.com/pblumer/atlas/internal/sysproxy"

// installOutboundProxy is [sysproxy.InstallAndLog], shared with the worker command.
func installOutboundProxy(getenv func(string) string) { sysproxy.InstallAndLog(getenv) }

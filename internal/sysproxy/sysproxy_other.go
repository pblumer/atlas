//go:build !windows

package sysproxy

// platform has no system proxy settings to offer outside Windows: there the variables
// are the convention, and Go's transport already reads them.
func platform() backend { return nil }

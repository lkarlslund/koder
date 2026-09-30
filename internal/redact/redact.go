// Package redact decides which values must be hidden from debug traces.
package redact

import "strings"

// Placeholder replaces a redacted value.
const Placeholder = "[redacted]"

// IsSensitiveHeader reports whether an HTTP header can carry credentials or
// session state and must not be recorded.
func IsSensitiveHeader(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	switch lower {
	case "authorization", "proxy-authorization", "cookie", "set-cookie":
		return true
	}
	return strings.Contains(lower, "api-key") || strings.Contains(lower, "token") || strings.Contains(lower, "secret")
}

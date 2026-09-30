package redact

import "testing"

func TestIsSensitiveHeader(t *testing.T) {
	for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Set-Cookie", "X-Api-Key", "api-key", "X-Auth-Token", "X-Client-Secret"} {
		if !IsSensitiveHeader(name) {
			t.Errorf("expected %q to be sensitive", name)
		}
	}
	for _, name := range []string{"Content-Type", "Accept", "User-Agent", "X-Request-Id"} {
		if IsSensitiveHeader(name) {
			t.Errorf("expected %q not to be sensitive", name)
		}
	}
}

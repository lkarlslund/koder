package service

import (
	"context"
	"testing"
)

func TestAuditIDContextAcceptsTrustedSafeIdentifiers(t *testing.T) {
	t.Parallel()
	ctx, err := WithAuditID(context.Background(), " request:01a01688-fc5d-7f7d-8bb8-de244977f8a1 ")
	if err != nil {
		t.Fatalf("WithAuditID() error = %v", err)
	}
	if got := AuditIDFromContext(ctx); got != "request:01a01688-fc5d-7f7d-8bb8-de244977f8a1" {
		t.Fatalf("AuditIDFromContext() = %q", got)
	}
	if AuditIDFromContext(nil) != "" {
		t.Fatal("nil context returned an audit ID")
	}
}

func TestAuditIDContextRejectsUntrustedShapes(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", "contains space", "line\nbreak", string(make([]byte, 129))} {
		if _, err := WithAuditID(context.Background(), value); err == nil {
			t.Fatalf("WithAuditID(%q) unexpectedly succeeded", value)
		}
	}
	if _, err := WithAuditID(nil, "request-1"); err == nil {
		t.Fatal("WithAuditID(nil) unexpectedly succeeded")
	}
}

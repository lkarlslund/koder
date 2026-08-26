package service

import (
	"context"
	"fmt"
	"strings"
)

type auditIDContextKey struct{}

// WithAuditID attaches a transport- or runtime-generated correlation ID. Models and
// tool arguments must not be allowed to choose this value themselves.
func WithAuditID(ctx context.Context, auditID string) (context.Context, error) {
	if ctx == nil {
		return nil, fmt.Errorf("memory audit context is required")
	}
	auditID = strings.TrimSpace(auditID)
	if auditID == "" || len(auditID) > 128 || !validAuditID(auditID) {
		return nil, fmt.Errorf("memory audit ID is invalid")
	}
	return context.WithValue(ctx, auditIDContextKey{}, auditID), nil
}

func AuditIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	auditID, _ := ctx.Value(auditIDContextKey{}).(string)
	return auditID
}

func validAuditID(value string) bool {
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' ||
			char == '-' || char == '_' || char == '.' || char == ':' {
			continue
		}
		return false
	}
	return true
}

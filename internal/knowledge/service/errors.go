package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

type ErrorCode string

const (
	ErrorCodeUnavailable ErrorCode = "unavailable"
	ErrorCodeForbidden   ErrorCode = "forbidden"
	ErrorCodeNotFound    ErrorCode = "not_found"
	ErrorCodeConflict    ErrorCode = "conflict"
	ErrorCodeStale       ErrorCode = "stale"
	ErrorCodeDependency  ErrorCode = "dependency"
	ErrorCodeInvalid     ErrorCode = "invalid"
	ErrorCodeTruncated   ErrorCode = "truncated"
	ErrorCodeInternal    ErrorCode = "internal"
)

// ErrorDetails carries bounded structured recovery information. Dependency identifiers
// may be returned only after the service has authorized the actor to see the target.
type ErrorDetails struct {
	ChunkID           knowledge.ChunkID                     `json:"chunk_id,omitempty"`
	EntryID           knowledge.EntryID                     `json:"entry_id,omitempty"`
	ChunkBlockers     *knowledgeStore.ChunkDeletionBlockers `json:"chunk_blockers,omitempty"`
	EntryBlockers     *knowledgeStore.EntryDeletionBlockers `json:"entry_blockers,omitempty"`
	TruncationReasons []string                              `json:"truncation_reasons,omitempty"`
}

// ServiceError is the stable tool/HTTP/UI error contract. cause is intentionally not
// serialized because backend errors may contain paths, provider details, or knowledge.
type ServiceError struct {
	Code      ErrorCode     `json:"code"`
	Message   string        `json:"message"`
	Retryable bool          `json:"retryable,omitempty"`
	Details   *ErrorDetails `json:"details,omitempty"`
	cause     error
}

func (e *ServiceError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *ServiceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// ClassifyError converts internal domain/store/provider errors to a sanitized stable
// outcome. It is idempotent and preserves the original cause for errors.Is/errors.As.
func ClassifyError(err error) *ServiceError {
	if err == nil {
		return nil
	}
	var existing *ServiceError
	if errors.As(err, &existing) {
		return existing
	}
	if details := dependencyErrorDetails(err); details != nil {
		return newServiceError(ErrorCodeDependency, err, details)
	}
	code := classifyErrorCode(err)
	return newServiceError(code, err, nil)
}

func classifyErrorCode(err error) ErrorCode {
	switch {
	case errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, ErrSemanticIndexUnavailable),
		errors.Is(err, knowledgeStore.ErrClosed),
		errors.Is(err, knowledgeStore.ErrReadOnly),
		errors.Is(err, knowledgeStore.ErrUnsupported),
		errors.Is(err, knowledgeStore.ErrIncompatible):
		return ErrorCodeUnavailable
	case errors.Is(err, ErrChunkPolicyDenied),
		errors.Is(err, ErrToolOfferDenied),
		errors.Is(err, ErrOperationalPolicyDenied),
		errors.Is(err, ErrProtectedChunk),
		errors.Is(err, ErrClassificationRejected):
		return ErrorCodeForbidden
	case errors.Is(err, knowledgeStore.ErrNotFound):
		return ErrorCodeNotFound
	case errors.Is(err, knowledgeStore.ErrStaleCursor):
		return ErrorCodeStale
	case errors.Is(err, knowledgeStore.ErrConflict),
		errors.Is(err, ErrDuplicateLink),
		errors.Is(err, ErrInvalidLifecycleTransition),
		errors.Is(err, ErrParentChunkArchived),
		errors.Is(err, ErrChunkMustBeArchived),
		errors.Is(err, ErrEntryMustBeArchived),
		errors.Is(err, ErrEntryNotEditable),
		errors.Is(err, ErrLinkEndpointUnavailable):
		return ErrorCodeConflict
	case errors.Is(err, knowledge.ErrInvalidRecord),
		errors.Is(err, knowledgeStore.ErrInvalidCursor),
		errors.Is(err, ErrDeleteConfirmationRequired),
		errors.Is(err, ErrReviewRequired),
		errors.Is(err, ErrInvalidSupersession),
		errors.Is(err, ErrPersonalOriginPolicy):
		return ErrorCodeInvalid
	default:
		return ErrorCodeInternal
	}
}

func newServiceError(code ErrorCode, cause error, details *ErrorDetails) *ServiceError {
	message, retryable := serviceErrorPresentation(code)
	return &ServiceError{Code: code, Message: message, Retryable: retryable, Details: details, cause: cause}
}

func serviceErrorPresentation(code ErrorCode) (string, bool) {
	switch code {
	case ErrorCodeUnavailable:
		return "Knowledge is temporarily unavailable.", true
	case ErrorCodeForbidden:
		return "This Knowledge operation is not allowed.", false
	case ErrorCodeNotFound:
		return "The Knowledge object was not found.", false
	case ErrorCodeConflict:
		return "Knowledge changed; refresh and try again.", true
	case ErrorCodeStale:
		return "This Knowledge result is stale; run the request again.", true
	case ErrorCodeDependency:
		return "Knowledge has dependent objects that must be handled first.", false
	case ErrorCodeInvalid:
		return "The Knowledge request is invalid.", false
	case ErrorCodeTruncated:
		return "The Knowledge result was truncated by safety limits.", false
	default:
		return "Knowledge could not complete the operation.", false
	}
}

func dependencyErrorDetails(err error) *ErrorDetails {
	var chunkError *ChunkDeletionBlockedError
	if errors.As(err, &chunkError) {
		blockers := cloneChunkDeletionBlockers(chunkError.Blockers)
		return &ErrorDetails{ChunkID: chunkError.ChunkID, ChunkBlockers: &blockers}
	}
	var entryError *EntryDeletionBlockedError
	if errors.As(err, &entryError) {
		blockers := cloneEntryDeletionBlockers(entryError.Blockers)
		return &ErrorDetails{EntryID: entryError.EntryID, EntryBlockers: &blockers}
	}
	if errors.Is(err, ErrChunkNotEmpty) || errors.Is(err, ErrEntryDeletionBlocked) {
		return &ErrorDetails{}
	}
	return nil
}

func cloneChunkDeletionBlockers(value knowledgeStore.ChunkDeletionBlockers) knowledgeStore.ChunkDeletionBlockers {
	value.EntryIDs = slices.Clone(value.EntryIDs)
	value.LinkIDs = slices.Clone(value.LinkIDs)
	value.EvidenceIDs = slices.Clone(value.EvidenceIDs)
	value.DependencyIDs = slices.Clone(value.DependencyIDs)
	value.DependentChunkIDs = slices.Clone(value.DependentChunkIDs)
	return value
}

func cloneEntryDeletionBlockers(value knowledgeStore.EntryDeletionBlockers) knowledgeStore.EntryDeletionBlockers {
	value.LinkIDs = slices.Clone(value.LinkIDs)
	value.SupersededEntryIDs = slices.Clone(value.SupersededEntryIDs)
	return value
}

// NewTruncatedError is for callers that explicitly require a complete bounded operation.
// Normal list/search/traversal APIs should return their partial result and truncation
// metadata instead of converting successful partial work to an error.
func NewTruncatedError(reasons []string) *ServiceError {
	reasons = sanitizeTruncationReasons(reasons)
	return newServiceError(ErrorCodeTruncated, fmt.Errorf("knowledge result truncated"), &ErrorDetails{TruncationReasons: reasons})
}

func sanitizeTruncationReasons(values []string) []string {
	result := make([]string, 0, min(len(values), 32))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 64 || !isSafeReasonCode(value) {
			continue
		}
		result = append(result, value)
		if len(result) == 32 {
			break
		}
	}
	slices.Sort(result)
	return slices.Compact(result)
}

func isSafeReasonCode(value string) bool {
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_' || char == '-' || char == '.' {
			continue
		}
		return false
	}
	return true
}

package assignment

import (
	"context"
	"errors"

	d "github.com/shentschel/teddycloud/next/backend/internal/domain/assignment"
)

var (
	ErrInvalidInput       = errors.New("invalid assignment input")
	ErrUnauthorized       = errors.New("assignment authority required")
	ErrTagNotFound        = errors.New("assignment Tag not found")
	ErrTargetNotFound     = errors.New("assignment target not found")
	ErrDependencyConflict = errors.New("assignment dependency conflict")
	ErrRevisionConflict   = errors.New("assignment revision conflict")
	ErrCommandConflict    = errors.New("assignment command conflict")
	ErrRetired            = errors.New("assignment retired")
	ErrURIUnsupported     = errors.New("assignment URI requires B1")
	ErrLimit              = errors.New("assignment retention limit")
	ErrBusy               = errors.New("assignment service busy")
	ErrContention         = errors.New("assignment repository contention")
	ErrUnavailable        = errors.New("assignment repository unavailable")
	ErrIndeterminate      = errors.New("assignment commit indeterminate")
	ErrUnsupportedState   = errors.New("assignment schema or state unsupported")
	ErrKnowledgeRevision  = errors.New("unknown assignment knowledge revision")
)

func sanitize(err error) error {
	if err == nil {
		return nil
	}
	for _, allowed := range []error{context.Canceled, context.DeadlineExceeded,
		ErrInvalidInput, ErrUnauthorized, ErrTagNotFound, ErrTargetNotFound,
		ErrDependencyConflict, ErrRevisionConflict, ErrCommandConflict, ErrRetired,
		ErrURIUnsupported, ErrLimit, ErrBusy, ErrContention, ErrUnavailable,
		ErrIndeterminate, ErrUnsupportedState, ErrKnowledgeRevision} {
		if errors.Is(err, allowed) {
			return allowed
		}
	}
	switch {
	case errors.Is(err, d.ErrCommandConflict):
		return ErrCommandConflict
	case errors.Is(err, d.ErrRevisionConflict):
		return ErrRevisionConflict
	case errors.Is(err, d.ErrDependencyConflict):
		return ErrDependencyConflict
	case errors.Is(err, d.ErrDecisionRetired):
		return ErrRetired
	case errors.Is(err, d.ErrReviewAuthority):
		return ErrUnauthorized
	case errors.Is(err, d.ErrRevisionOverflow), errors.Is(err, d.ErrEvidenceEventLimit),
		errors.Is(err, d.ErrObservationLimit), errors.Is(err, d.ErrSupportLinkLimit),
		errors.Is(err, d.ErrRetainedByteLimit), errors.Is(err, d.ErrCommandEvidenceLimit):
		return ErrLimit
	case errors.Is(err, d.ErrInvalidID), errors.Is(err, d.ErrInvalidReference),
		errors.Is(err, d.ErrInvalidDecision), errors.Is(err, d.ErrInvalidInterval),
		errors.Is(err, d.ErrInvalidEvidence), errors.Is(err, d.ErrInvalidResolution),
		errors.Is(err, d.ErrEvidenceSubject), errors.Is(err, d.ErrDuplicateEvidence),
		errors.Is(err, d.ErrDuplicateReview), errors.Is(err, d.ErrInvalidCanonicalValue):
		return ErrInvalidInput
	}
	return ErrUnavailable
}

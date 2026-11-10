package smswire

import (
	"errors"
	"fmt"
	"time"
)

// Error is a call that failed: the API's answer (Status, Code, Message) or a network problem
// (Status 0, Code "network_error" or "timeout").
type Error struct {
	// HTTP status, or 0 when no answer arrived.
	Status int
	// Stable machine code, e.g. "invalid_request", "rate_limited", "network_error", "timeout".
	Code string
	// What went wrong, in words.
	Message string
	// How long until trying again makes sense (from Retry-After), when the API said.
	RetryAfter *time.Duration
	// The underlying transport error, for Status 0.
	Err error
}

func (e *Error) Error() string {
	if e.Status == 0 {
		return fmt.Sprintf("smswire: %s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("smswire: %d %s: %s", e.Status, e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

// ErrInvalidSignature is what VerifyWebhook's errors match with errors.Is. Answer such a webhook with 400.
var ErrInvalidSignature = errors.New("smswire: invalid webhook signature")

// SignatureError is a webhook whose signature is missing, wrong or too old.
type SignatureError struct{ Reason string }

func (e *SignatureError) Error() string { return "smswire: invalid webhook signature: " + e.Reason }

func (e *SignatureError) Is(target error) bool { return target == ErrInvalidSignature }

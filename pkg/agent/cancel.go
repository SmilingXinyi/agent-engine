package agent

import (
	"context"
	"errors"
)

// IsExecutionCanceled reports whether an error was caused by context cancellation.
func IsExecutionCanceled(err error) bool {
	return errors.Is(err, context.Canceled)
}

// IsExecutionDeadlineExceeded reports whether an error was caused by context deadline expiration.
func IsExecutionDeadlineExceeded(err error) bool {
	return errors.Is(err, context.DeadlineExceeded)
}

// ContextErrorOr returns err when present, otherwise returns the context error.
func ContextErrorOr(ctx context.Context, err error) error {
	if err != nil {
		return err
	}
	return ctx.Err()
}

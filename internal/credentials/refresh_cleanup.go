package credentials

import (
	"errors"
	"fmt"
)

type refreshFailure struct {
	cause error
	clear func(string) error
}

func (e *refreshFailure) Error() string { return e.cause.Error() }
func (e *refreshFailure) Unwrap() error { return e.cause }

// ClearCredentialsAfterRefresh clears the refresh-pinned store conditionally.
func ClearCredentialsAfterRefresh(refreshErr error, refreshToken string) error {
	var failure *refreshFailure
	if !errors.As(refreshErr, &failure) {
		return fmt.Errorf("refresh failure has no pinned credential store")
	}
	return failure.clear(refreshToken)
}

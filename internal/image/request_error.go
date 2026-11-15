package image

import (
	"context"
	"errors"
	"fmt"
)

// RequestError distinguishes a local interruption from cancellation of paid
// upstream work. Preserve causes so callers can still detect cancellation.
func RequestError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("image request interrupted locally; remote outcome unknown, work may continue and be charged: %w", err)
	}
	return err
}

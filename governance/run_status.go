package governance

import (
	"context"
	"errors"
)

func interruptedGovernanceError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

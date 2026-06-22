package node

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"
)

const (
	panelRequestTimeout = 12 * time.Second
	panelTaskTimeout    = 45 * time.Second
	minPanelTaskBudget  = 3 * time.Second
)

func panelRequestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return context.WithCancel(ctx)
		}
		if remaining < panelRequestTimeout {
			return context.WithTimeout(ctx, remaining)
		}
	}
	return context.WithTimeout(ctx, panelRequestTimeout)
}

func isPanelTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	errText := err.Error()
	return strings.Contains(errText, "Client.Timeout exceeded") ||
		strings.Contains(errText, "context deadline exceeded")
}

func hasPanelTaskBudget(ctx context.Context) bool {
	if err := ctx.Err(); err != nil {
		return false
	}
	if deadline, ok := ctx.Deadline(); ok {
		return time.Until(deadline) > minPanelTaskBudget
	}
	return true
}

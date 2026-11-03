package engines

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// RunChecked is the user-work boundary: probe once (cached), then make exactly
// one adapter invocation. It never replays a possibly billed generation.
func RunChecked(ctx context.Context, cfg Object, prompt, cwd string, images []string) (string, Object, error) {
	if ctx.Err() != nil {
		return "", nil, ctx.Err()
	}
	if problem := NetworkProblem(ctx, cfg, 6*time.Second); problem != "" {
		if ctx.Err() != nil {
			return "", nil, ctx.Err()
		}
		return "", nil, errors.New(problem)
	}
	answer, usage, err := Run(ctx, cfg, prompt, cwd, images)
	if err == nil {
		return answer, usage, nil
	}
	if ctx.Err() != nil {
		return answer, usage, ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return answer, usage, err
	}
	message := ExplainNetwork(cfg, err.Error())
	if message == err.Error() {
		return answer, usage, err
	}
	// Keep the original error available to errors.Is/As without repeating its text.
	return answer, usage, fmt.Errorf("%s: %w", message[len(err.Error())+1:], err)
}

// StreamChecked checks reachability before opening a stream. A failed or
// interrupted stream is never restarted by this wrapper, including after output.
func StreamChecked(ctx context.Context, cfg Object, prompt, cwd string, images []string, emit func(string) error) (Object, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if emit == nil {
		return nil, errors.New("stream callback required")
	}
	if problem := NetworkProblem(ctx, cfg, 6*time.Second); problem != "" {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New(problem)
	}
	var callbackErr error
	usage, err := Stream(ctx, cfg, prompt, cwd, images, func(piece string) error { callbackErr = emit(piece); return callbackErr })
	// Callback failures belong to the consumer, not the network. Preserve their
	// identity rather than adding connectivity guidance (redacting secrets still
	// takes precedence if the callback itself includes configured credentials).
	if callbackErr != nil {
		return usage, safeError(callbackErr, cfg)
	}
	if ctx.Err() != nil {
		return usage, ctx.Err()
	}
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return usage, err
	}
	message := ExplainNetwork(cfg, err.Error())
	if message == err.Error() {
		return usage, err
	}
	return usage, fmt.Errorf("%s: %w", message[len(err.Error())+1:], err)
}

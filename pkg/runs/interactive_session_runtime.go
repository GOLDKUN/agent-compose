package runs

import (
	"context"
)

// InteractiveSessionRuntime owns the runtime interaction of a session. RPC
// attachments communicate through the session rather than owning this object.
// Implementations must serialize input and close exactly once.
type InteractiveSessionRuntime interface {
	Run(context.Context, RunAttachReceiver, RunAttachSender) (TransitionRequest, error)
	Send(context.Context, RunAttachInput) error
	Close() error
}

// InteractiveSessionFactory is kept next to its consumer so controller
// execution can migrate from request-owned interactions incrementally.
type InteractiveSessionFactory interface {
	OpenInteractiveSession(context.Context, interactionRunContext, RunAttachInput) (InteractiveSessionRuntime, error)
}

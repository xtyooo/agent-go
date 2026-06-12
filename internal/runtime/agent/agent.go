package agent

import (
	"context"

	"github.com/learn-demo/agent-go/internal/runtime/event"
)

// Input is the per-request context passed from the HTTP layer to an Agent.
type Input struct {
	Query          string
	ConversationID string
	RequestID      string
	Temperature    *float64
	MaxRounds      int
}

// Agent is the minimal streaming interface shared by all Agent implementations.
type Agent interface {
	Run(ctx context.Context, input Input) (<-chan event.Event, error)
}

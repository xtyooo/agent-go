package planexecute

import (
	"context"
	"log/slog"
	"time"

	"github.com/learn-demo/agent-go/internal/runtime/event"
)

type eventSender struct {
	ctx            context.Context
	logger         *slog.Logger
	conversationID string
	events         chan<- event.Event
	record         *runRecord
	startedAt      time.Time
}

func (s *eventSender) send(evt event.Event) bool {
	select {
	case <-s.ctx.Done():
		s.logger.Warn("🛑 Plan-Execute 事件发送被取消",
			"conversation_id", s.conversationID,
			"event_type", evt.Type,
			"elapsed_ms", elapsedMillis(s.startedAt),
			"error", s.ctx.Err(),
		)
		return false
	default:
	}

	select {
	case <-s.ctx.Done():
		return false
	case s.events <- evt:
		s.record.capture(evt, elapsedMillis(s.startedAt))
		return true
	}
}

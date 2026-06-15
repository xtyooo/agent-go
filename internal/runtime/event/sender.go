package event

import (
	"context"
	"log/slog"
)

type SenderConfig struct {
	Ctx            context.Context
	Out            chan<- Event
	Logger         *slog.Logger
	ConversationID string
	Elapsed        func() int64
	Capture        func(Event, int64)
	CancelMessage  string
}

type Sender struct {
	cfg SenderConfig
}

func NewSender(cfg SenderConfig) *Sender {
	if cfg.Ctx == nil {
		cfg.Ctx = context.Background()
	}
	if cfg.CancelMessage == "" {
		cfg.CancelMessage = "事件发送被取消"
	}
	return &Sender{cfg: cfg}
}

func (s *Sender) Send(evt Event) bool {
	select {
	case <-s.cfg.Ctx.Done():
		s.logCanceled(evt)
		return false
	default:
	}

	select {
	case <-s.cfg.Ctx.Done():
		s.logCanceled(evt)
		return false
	case s.cfg.Out <- evt:
		elapsedMs := s.elapsedMillis()
		if s.cfg.Capture != nil {
			s.cfg.Capture(evt, elapsedMs)
		}
		return true
	}
}

func (s *Sender) logCanceled(evt Event) {
	if s.cfg.Logger == nil {
		return
	}
	args := []any{
		"event_type", evt.Type,
		"error", s.cfg.Ctx.Err(),
	}
	if s.cfg.ConversationID != "" {
		args = append(args, "conversation_id", s.cfg.ConversationID)
	}
	if s.cfg.Elapsed != nil {
		args = append(args, "elapsed_ms", s.elapsedMillis())
	}
	s.cfg.Logger.Warn(s.cfg.CancelMessage, args...)
}

func (s *Sender) elapsedMillis() int64 {
	if s.cfg.Elapsed == nil {
		return 0
	}
	return s.cfg.Elapsed()
}

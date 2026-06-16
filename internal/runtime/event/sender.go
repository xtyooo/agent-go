package event

import (
	"context"
)

type SenderConfig struct {
	Ctx       context.Context
	Out       chan<- Event
	OnCancel  func(Event, error)
	AfterSend func(Event)
}

type Sender struct {
	cfg SenderConfig
}

func NewSender(cfg SenderConfig) *Sender {
	if cfg.Ctx == nil {
		cfg.Ctx = context.Background()
	}
	return &Sender{cfg: cfg}
}

func (s *Sender) Send(evt Event) bool {
	select {
	case <-s.cfg.Ctx.Done():
		s.cancel(evt)
		return false
	default:
	}

	select {
	case <-s.cfg.Ctx.Done():
		s.cancel(evt)
		return false
	case s.cfg.Out <- evt:
		if s.cfg.AfterSend != nil {
			s.cfg.AfterSend(evt)
		}
		return true
	}
}

func (s *Sender) cancel(evt Event) {
	if s.cfg.OnCancel != nil {
		s.cfg.OnCancel(evt, s.cfg.Ctx.Err())
	}
}

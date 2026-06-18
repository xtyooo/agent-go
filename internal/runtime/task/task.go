package task

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"agentG/internal/runtime/event"
)

const (
	defaultMaxBufferedEvents = 1000
	subscriberBufferSize     = 64
	stopEventWriteTimeout    = 2 * time.Second
)

// Manager keeps track of active Agent runs and lets HTTP clients reconnect to
// the same run without owning the run's lifecycle.
type Manager struct {
	mu     sync.Mutex
	tasks  map[string]*Info
	logger *slog.Logger
}

type RegisterOptions struct {
	Query     string
	RequestID string
	TraceID   string
}

type Snapshot struct {
	ConversationID  string    `json:"conversationId"`
	AgentType       string    `json:"agentType"`
	Query           string    `json:"query,omitempty"`
	RequestID       string    `json:"requestId,omitempty"`
	TraceID         string    `json:"traceId,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	RunningMs       int64     `json:"runningMs"`
	EventCount      int       `json:"eventCount"`
	SubscriberCount int       `json:"subscriberCount"`
	Stopped         bool      `json:"stopped"`
	Running         bool      `json:"running"`
}

type EventRecord struct {
	Seq   int         `json:"seq"`
	Event event.Event `json:"event"`
}

// Info represents one running Agent task.
type Info struct {
	ConversationID string
	AgentType      string
	Query          string
	RequestID      string
	TraceID        string
	CreatedAt      time.Time

	ctx    context.Context
	cancel context.CancelFunc
	stopCh chan struct{}
	once   sync.Once

	manager *Manager
	stopped atomic.Bool

	mu          sync.Mutex
	events      []EventRecord
	nextSeq     int
	maxBuffered int
	subscribers map[chan EventRecord]struct{}
	done        bool
}

func NewManager(logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		tasks:  make(map[string]*Info),
		logger: logger,
	}
}

func (m *Manager) Register(parent context.Context, conversationID string, agentType string) (*Info, error) {
	return m.RegisterWithOptions(parent, conversationID, agentType, RegisterOptions{})
}

// RegisterWithOptions registers a task. The execution context is intentionally
// detached from the HTTP request context so page refreshes only close that SSE
// client, not the underlying Agent run.
func (m *Manager) RegisterWithOptions(parent context.Context, conversationID string, agentType string, options RegisterOptions) (*Info, error) {
	if conversationID == "" {
		return nil, fmt.Errorf("conversationID is required")
	}
	_ = parent

	m.mu.Lock()
	defer m.mu.Unlock()

	if existing := m.tasks[conversationID]; existing != nil {
		m.logger.Warn("conversation already has a running task",
			"conversation_id", conversationID,
			"agent_type", existing.AgentType,
			"running_ms", time.Since(existing.CreatedAt).Milliseconds(),
		)
		return nil, fmt.Errorf("conversation %s already has a running task", conversationID)
	}

	ctx, cancel := context.WithCancel(context.Background())
	info := &Info{
		ConversationID: conversationID,
		AgentType:      agentType,
		Query:          options.Query,
		RequestID:      options.RequestID,
		TraceID:        options.TraceID,
		CreatedAt:      time.Now(),
		ctx:            ctx,
		cancel:         cancel,
		stopCh:         make(chan struct{}),
		manager:        m,
		events:         make([]EventRecord, 0, 128),
		maxBuffered:    defaultMaxBufferedEvents,
		subscribers:    make(map[chan EventRecord]struct{}),
	}
	m.tasks[conversationID] = info

	m.logger.Info("agent task registered",
		"conversation_id", conversationID,
		"agent_type", agentType,
		"trace_id", options.TraceID,
	)
	return info, nil
}

func (m *Manager) Stop(conversationID string) bool {
	info, ok := m.Get(conversationID)
	if !ok {
		m.logger.Warn("stop task failed: task not found", "conversation_id", conversationID)
		return false
	}

	info.stop()
	m.logger.Info("agent task stop signal sent",
		"conversation_id", conversationID,
		"agent_type", info.AgentType,
		"running_ms", time.Since(info.CreatedAt).Milliseconds(),
	)
	return true
}

func (m *Manager) Remove(info *Info) {
	if info == nil {
		return
	}

	m.mu.Lock()
	if current := m.tasks[info.ConversationID]; current == info {
		delete(m.tasks, info.ConversationID)
	}
	m.mu.Unlock()

	info.cancel()
	info.close()
	m.logger.Info("agent task cleaned up",
		"conversation_id", info.ConversationID,
		"agent_type", info.AgentType,
		"stopped", info.stopped.Load(),
		"running_ms", time.Since(info.CreatedAt).Milliseconds(),
	)
}

func (m *Manager) Get(conversationID string) (*Info, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	info := m.tasks[conversationID]
	return info, info != nil
}

func (m *Manager) Snapshot(conversationID string) (Snapshot, bool) {
	info, ok := m.Get(conversationID)
	if !ok {
		return Snapshot{}, false
	}
	return info.Snapshot(), true
}

func (m *Manager) List() []Snapshot {
	m.mu.Lock()
	infos := make([]*Info, 0, len(m.tasks))
	for _, info := range m.tasks {
		infos = append(infos, info)
	}
	m.mu.Unlock()

	snapshots := make([]Snapshot, 0, len(infos))
	for _, info := range infos {
		snapshots = append(snapshots, info.Snapshot())
	}
	return snapshots
}

// Subscribe returns a channel that first replays buffered events with seq >=
// fromSeq and then follows live events. The caller must call unsubscribe.
func (m *Manager) Subscribe(conversationID string, fromSeq int) (<-chan EventRecord, func(), Snapshot, bool) {
	info, ok := m.Get(conversationID)
	if !ok {
		return nil, func() {}, Snapshot{}, false
	}
	ch, unsubscribe := info.subscribe(fromSeq)
	return ch, unsubscribe, info.Snapshot(), true
}

// WrapEvents keeps backward compatibility for tests and callers that want a
// simple channel. It also records and broadcasts the wrapped events.
func (m *Manager) WrapEvents(info *Info, source <-chan event.Event) <-chan event.Event {
	out := make(chan event.Event)
	go func() {
		defer close(out)
		for record := range m.ForwardEvents(info, source) {
			out <- record.Event
		}
	}()
	return out
}

// ForwardEvents consumes the Agent source stream, records every event, fans it
// out to subscribers, and closes the task when the source ends or is stopped.
func (m *Manager) ForwardEvents(info *Info, source <-chan event.Event) <-chan EventRecord {
	out := make(chan EventRecord)
	go m.forwardEvents(info, source, out)
	return out
}

func (m *Manager) forwardEvents(info *Info, source <-chan event.Event, out chan<- EventRecord) {
	defer close(out)
	defer info.close()

	for {
		select {
		case <-info.stopCh:
			m.forwardStopEvents(out, info)
			return
		case <-info.ctx.Done():
			if info.stopped.Load() {
				m.forwardStopEvents(out, info)
			}
			return
		case evt, ok := <-source:
			if !ok {
				return
			}
			select {
			case <-info.stopCh:
				m.forwardStopEvents(out, info)
				return
			default:
			}
			record := info.appendEvent(evt)
			if !sendRecord(context.Background(), out, record) {
				return
			}
		}
	}
}

func (m *Manager) forwardStopEvents(out chan<- EventRecord, info *Info) {
	for _, evt := range []event.Event{
		event.Thinking("已停止生成\n"),
		event.Complete(),
	} {
		record := info.appendEvent(evt)
		if !sendRecord(context.Background(), out, record) {
			return
		}
	}
}

func (i *Info) Context() context.Context {
	if i == nil || i.ctx == nil {
		return context.Background()
	}
	return i.ctx
}

func (i *Info) Snapshot() Snapshot {
	i.mu.Lock()
	defer i.mu.Unlock()
	return Snapshot{
		ConversationID:  i.ConversationID,
		AgentType:       i.AgentType,
		Query:           i.Query,
		RequestID:       i.RequestID,
		TraceID:         i.TraceID,
		CreatedAt:       i.CreatedAt,
		RunningMs:       time.Since(i.CreatedAt).Milliseconds(),
		EventCount:      i.nextSeq,
		SubscriberCount: len(i.subscribers),
		Stopped:         i.stopped.Load(),
		Running:         !i.done,
	}
}

func (i *Info) stop() {
	i.once.Do(func() {
		i.stopped.Store(true)
		close(i.stopCh)
		i.cancel()
	})
}

func (i *Info) appendEvent(evt event.Event) EventRecord {
	i.mu.Lock()
	i.nextSeq++
	evt.Seq = i.nextSeq
	record := EventRecord{Seq: i.nextSeq, Event: evt}
	i.events = append(i.events, record)
	if len(i.events) > i.maxBuffered {
		copy(i.events, i.events[len(i.events)-i.maxBuffered:])
		i.events = i.events[:i.maxBuffered]
	}
	for subscriber := range i.subscribers {
		select {
		case subscriber <- record:
		default:
			// Drop for slow subscribers; they can refresh and replay the buffer.
		}
	}
	i.mu.Unlock()
	return record
}

func (i *Info) subscribe(fromSeq int) (<-chan EventRecord, func()) {
	i.mu.Lock()
	replay := make([]EventRecord, 0, len(i.events))
	for _, record := range i.events {
		if record.Seq >= fromSeq {
			replay = append(replay, record)
		}
	}
	ch := make(chan EventRecord, len(replay)+subscriberBufferSize)
	for _, record := range replay {
		ch <- record
	}
	if i.done {
		i.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	i.subscribers[ch] = struct{}{}
	i.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			i.mu.Lock()
			if _, ok := i.subscribers[ch]; ok {
				delete(i.subscribers, ch)
				close(ch)
			}
			i.mu.Unlock()
		})
	}
	return ch, unsubscribe
}

func (i *Info) close() {
	i.mu.Lock()
	if i.done {
		i.mu.Unlock()
		return
	}
	i.done = true
	for subscriber := range i.subscribers {
		close(subscriber)
		delete(i.subscribers, subscriber)
	}
	i.mu.Unlock()
}

func sendRecord(ctx context.Context, out chan<- EventRecord, record EventRecord) bool {
	timer := time.NewTimer(stopEventWriteTimeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	case out <- record:
		return true
	}
}

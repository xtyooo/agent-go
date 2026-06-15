package task

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/learn-demo/agent-go/internal/runtime/event"
)

const stopEventWriteTimeout = 2 * time.Second

// Manager 管理正在执行的 Agent 流式任务。
//
// Java dodo-agent 使用 ConcurrentHashMap 保存 conversationId -> TaskInfo，
// 并通过 Reactor Disposable 停止模型流。Go 版对应关系是：
//   - map + sync.Mutex：保存正在执行的任务。
//   - context.CancelFunc：替代 Disposable，从源头取消模型 HTTP stream 和工具执行。
//   - channel：替代 Sinks.Many，承载 Agent -> SSE 的事件流。
type Manager struct {
	mu     sync.Mutex
	tasks  map[string]*Info
	logger *slog.Logger
}

// Info 是一次正在运行的 Agent 任务。
type Info struct {
	// ConversationID 是任务所属会话，同一会话同一时间只允许一个任务。
	ConversationID string
	// AgentType 是当前任务类型，例如 websearch。
	AgentType string
	// CreatedAt 是任务注册时间，用于日志和后续超时清理。
	CreatedAt time.Time

	ctx     context.Context
	cancel  context.CancelFunc
	stopCh  chan struct{}
	once    sync.Once
	stopped atomic.Bool
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

// Register 注册一个会话任务，并返回派生出来的任务 context。
// 如果同一个 conversationId 已有任务在执行，返回错误，调用方应拒绝本次请求。
func (m *Manager) Register(parent context.Context, conversationID string, agentType string) (*Info, error) {
	if conversationID == "" {
		return nil, fmt.Errorf("conversationID is required")
	}
	if parent == nil {
		parent = context.Background()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if existing := m.tasks[conversationID]; existing != nil {
		m.logger.Warn("\U000026A0 会话已有任务正在执行，拒绝注册新任务",
			"conversation_id", conversationID,
			"agent_type", existing.AgentType,
			"running_ms", time.Since(existing.CreatedAt).Milliseconds(),
		)
		return nil, fmt.Errorf("conversation %s already has a running task", conversationID)
	}

	ctx, cancel := context.WithCancel(parent)
	info := &Info{
		ConversationID: conversationID,
		AgentType:      agentType,
		CreatedAt:      time.Now(),
		ctx:            ctx,
		cancel:         cancel,
		stopCh:         make(chan struct{}),
	}
	m.tasks[conversationID] = info

	m.logger.Info("\U0001F4CC Agent 任务已注册",
		"conversation_id", conversationID,
		"agent_type", agentType,
	)
	return info, nil
}

// Stop 主动停止一个会话任务。
// 它会关闭 stopCh 并调用 cancel，使模型流和工具调用尽快收到 context cancellation。
func (m *Manager) Stop(conversationID string) bool {
	m.mu.Lock()
	info := m.tasks[conversationID]
	m.mu.Unlock()
	if info == nil {
		m.logger.Warn("\U000026A0 停止任务失败：未找到运行中的任务", "conversation_id", conversationID)
		return false
	}

	info.stop()
	m.logger.Info("\U0001F6D1 Agent 任务停止信号已发出",
		"conversation_id", conversationID,
		"agent_type", info.AgentType,
		"running_ms", time.Since(info.CreatedAt).Milliseconds(),
	)
	return true
}

// Remove 清理任务注册表。
// 正常完成、客户端断开、用户停止后都必须调用，避免任务残留导致后续请求被误判为并发。
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
	m.logger.Info("\U0001F9F9 Agent 任务已清理",
		"conversation_id", info.ConversationID,
		"agent_type", info.AgentType,
		"stopped", info.stopped.Load(),
		"running_ms", time.Since(info.CreatedAt).Milliseconds(),
	)
}

// WrapEvents 包装 Agent 原始事件流。
// 当 Stop 被调用时，这里会向原 SSE 输出一条停止提示并补 complete，然后关闭输出通道。
func (m *Manager) WrapEvents(info *Info, source <-chan event.Event) <-chan event.Event {
	out := make(chan event.Event)
	go m.forwardEvents(info, source, out)
	return out
}

func (m *Manager) forwardEvents(info *Info, source <-chan event.Event, out chan<- event.Event) {
	defer close(out)

	for {
		select {
		case <-info.stopCh:
			sendStopEvents(context.Background(), out)
			return
		case <-info.ctx.Done():
			if info.stopped.Load() {
				sendStopEvents(context.Background(), out)
			}
			return
		case evt, ok := <-source:
			if !ok {
				return
			}
			select {
			case <-info.stopCh:
				sendStopEvents(context.Background(), out)
				return
			case out <- evt:
			}
		}
	}
}

// Context 返回任务 context。Agent 必须使用这个 context 调用模型和工具。
func (i *Info) Context() context.Context {
	if i == nil || i.ctx == nil {
		return context.Background()
	}
	return i.ctx
}

func (i *Info) stop() {
	i.once.Do(func() {
		i.stopped.Store(true)
		close(i.stopCh)
		i.cancel()
	})
}

func sendStopEvents(ctx context.Context, out chan<- event.Event) {
	sendTaskEvent(ctx, out, event.Thinking("已停止生成\n"))
	sendTaskEvent(ctx, out, event.Complete())
}

func sendTaskEvent(ctx context.Context, out chan<- event.Event, evt event.Event) bool {
	// 停止提示只是收尾体验，不能因为前端已经断开而长期阻塞任务清理。
	timer := time.NewTimer(stopEventWriteTimeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	case out <- evt:
		return true
	}
}

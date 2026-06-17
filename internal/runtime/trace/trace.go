package trace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/learn-demo/agent-go/internal/runtime/event"
)

const defaultMaxEventContentChars = 4000

// Config 控制 Agent trace 的本地记录行为。
// Trace 属于教学版可观测性能力：它记录 SSE 事件、耗时和运行元数据，便于排查一次 Agent Run 卡在哪里。
type Config struct {
	// Enabled 控制是否记录 trace；关闭后 HTTP 层仍可正常流式输出，只是不会落盘。
	Enabled bool
	// Directory 是 trace JSON 文件存储目录。
	Directory string
	// MaxEventContentChars 限制单个事件可保存的长文本字段长度，避免搜索结果或模型输出把 trace 文件撑得过大。
	MaxEventContentChars int
}

// RunMeta 是一次 Agent Run 开始时就能确定的元数据。
// 注意这里不包含模型 API Key、Tavily Key、MCP Header 等敏感信息。
type RunMeta struct {
	TraceID        string    `json:"traceId"`
	RequestID      string    `json:"requestId,omitempty"`
	ConversationID string    `json:"conversationId,omitempty"`
	AgentType      string    `json:"agentType,omitempty"`
	Query          string    `json:"query,omitempty"`
	RemoteAddr     string    `json:"remoteAddr,omitempty"`
	StartedAt      time.Time `json:"startedAt"`
}

// EventRecord 是 trace 中保存的一条 SSE 事件。
// OffsetMs 表示该事件相对 Run 开始的耗时，Replay 时可以选择按原节奏或快速回放。
type EventRecord struct {
	Index    int         `json:"index"`
	OffsetMs int64       `json:"offsetMs"`
	Event    event.Event `json:"event"`
}

// Run 是一次可回放的 Agent 执行记录。
type Run struct {
	RunMeta
	EndedAt      *time.Time         `json:"endedAt,omitempty"`
	ElapsedMs    int64              `json:"elapsedMs,omitempty"`
	Status       string             `json:"status,omitempty"`
	EventCount   int                `json:"eventCount"`
	TypeCounts   map[event.Type]int `json:"typeCounts,omitempty"`
	FirstEventMs int64              `json:"firstEventMs,omitempty"`
	Error        string             `json:"error,omitempty"`
	Events       []EventRecord      `json:"events"`
}

// Summary 是 Finish 后返回给 HTTP 层打印日志的轻量摘要。
type Summary struct {
	TraceID      string
	Status       string
	EventCount   int
	TypeCounts   map[event.Type]int
	FirstEventMs int64
	ElapsedMs    int64
	FilePath     string
}

// Store 定义 trace 记录和读取能力。
// 当前实现是本地 JSON 文件，后续如果要接入数据库或对象存储，只需要替换这个接口实现。
type Store interface {
	Start(ctx context.Context, meta RunMeta) (*Recorder, error)
	Load(ctx context.Context, traceID string) (Run, error)
}

// FileStore 把每次 Run 保存为一个 JSON 文件。
type FileStore struct {
	cfg    Config
	logger *slog.Logger

	mu     sync.Mutex
	active map[string]*Run
}

// NewFileStore 创建本地文件 trace store。
func NewFileStore(cfg Config, logger *slog.Logger) (*FileStore, error) {
	if logger == nil {
		logger = slog.Default()
	}
	cfg.Directory = strings.TrimSpace(cfg.Directory)
	if cfg.Directory == "" {
		cfg.Directory = "traces"
	}
	if cfg.MaxEventContentChars <= 0 {
		cfg.MaxEventContentChars = defaultMaxEventContentChars
	}
	if cfg.Enabled {
		if err := os.MkdirAll(cfg.Directory, 0o755); err != nil {
			return nil, fmt.Errorf("创建 trace 目录失败: %w", err)
		}
	}
	return &FileStore{
		cfg:    cfg,
		logger: logger,
		active: make(map[string]*Run),
	}, nil
}

// Enabled 表示当前 store 是否会真实落盘。
func (s *FileStore) Enabled() bool {
	return s != nil && s.cfg.Enabled
}

// Start 开启一次 trace 记录。
func (s *FileStore) Start(ctx context.Context, meta RunMeta) (*Recorder, error) {
	if s == nil || !s.cfg.Enabled {
		return noopRecorder(), nil
	}
	if meta.TraceID == "" {
		meta.TraceID = NewID()
	}
	if meta.StartedAt.IsZero() {
		meta.StartedAt = time.Now()
	}
	run := &Run{
		RunMeta:    meta,
		Status:     "running",
		TypeCounts: make(map[event.Type]int),
		Events:     make([]EventRecord, 0, 128),
	}

	s.mu.Lock()
	s.active[meta.TraceID] = run
	s.mu.Unlock()

	s.logger.Info("🧭 Agent Trace 已开始",
		"trace_id", meta.TraceID,
		"request_id", meta.RequestID,
		"conversation_id", meta.ConversationID,
		"agent_type", meta.AgentType,
	)
	return &Recorder{
		store:     s,
		traceID:   meta.TraceID,
		startedAt: meta.StartedAt,
		enabled:   true,
	}, nil
}

// Load 根据 traceId 读取完整 Run。
func (s *FileStore) Load(ctx context.Context, traceID string) (Run, error) {
	if s == nil || !s.cfg.Enabled {
		return Run{}, errors.New("trace store is disabled")
	}
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return Run{}, errors.New("traceID is required")
	}
	if err := ctx.Err(); err != nil {
		return Run{}, err
	}

	payload, err := os.ReadFile(s.filePath(traceID))
	if err != nil {
		return Run{}, fmt.Errorf("读取 trace 失败: %w", err)
	}
	var run Run
	if err := json.Unmarshal(payload, &run); err != nil {
		return Run{}, fmt.Errorf("解析 trace 失败: %w", err)
	}
	return run, nil
}

func (s *FileStore) record(traceID string, evt event.Event, offsetMs int64) {
	if s == nil || !s.cfg.Enabled {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	run := s.active[traceID]
	if run == nil {
		return
	}
	sanitized := sanitizeEvent(evt, s.cfg.MaxEventContentChars)
	run.EventCount++
	run.TypeCounts[sanitized.Type]++
	if run.EventCount == 1 {
		run.FirstEventMs = offsetMs
	}
	run.Events = append(run.Events, EventRecord{
		Index:    run.EventCount,
		OffsetMs: offsetMs,
		Event:    sanitized,
	})
}

func (s *FileStore) finish(traceID string, status string, err error) (Summary, error) {
	if s == nil || !s.cfg.Enabled {
		return Summary{}, nil
	}

	s.mu.Lock()
	run := s.active[traceID]
	if run != nil {
		delete(s.active, traceID)
	}
	s.mu.Unlock()

	if run == nil {
		return Summary{}, fmt.Errorf("trace %s is not active", traceID)
	}
	if status == "" {
		status = "completed"
	}
	now := time.Now()
	run.EndedAt = &now
	run.ElapsedMs = now.Sub(run.StartedAt).Milliseconds()
	run.Status = status
	if err != nil {
		run.Error = err.Error()
	}

	payload, marshalErr := json.MarshalIndent(run, "", "  ")
	if marshalErr != nil {
		return Summary{}, fmt.Errorf("编码 trace 失败: %w", marshalErr)
	}
	path := s.filePath(traceID)
	if writeErr := os.WriteFile(path, payload, 0o644); writeErr != nil {
		return Summary{}, fmt.Errorf("写入 trace 失败: %w", writeErr)
	}

	summary := Summary{
		TraceID:      traceID,
		Status:       run.Status,
		EventCount:   run.EventCount,
		TypeCounts:   copyTypeCounts(run.TypeCounts),
		FirstEventMs: run.FirstEventMs,
		ElapsedMs:    run.ElapsedMs,
		FilePath:     path,
	}
	s.logger.Info("📊 Agent Trace 已保存",
		"trace_id", traceID,
		"status", summary.Status,
		"event_count", summary.EventCount,
		"first_event_ms", summary.FirstEventMs,
		"elapsed_ms", summary.ElapsedMs,
		"file", path,
	)
	return summary, nil
}

func (s *FileStore) filePath(traceID string) string {
	return filepath.Join(s.cfg.Directory, safeTraceID(traceID)+".json")
}

// Recorder 是 HTTP/SSE 边界持有的单次 trace 写入器。
type Recorder struct {
	store     *FileStore
	traceID   string
	startedAt time.Time
	enabled   bool

	mu       sync.Mutex
	finished bool
}

func noopRecorder() *Recorder {
	return &Recorder{startedAt: time.Now()}
}

// TraceID 返回本次记录 ID。
func (r *Recorder) TraceID() string {
	if r == nil {
		return ""
	}
	return r.traceID
}

// Enabled 表示 recorder 是否会真实记录。
func (r *Recorder) Enabled() bool {
	return r != nil && r.enabled
}

// Record 保存一条 SSE 事件。
func (r *Recorder) Record(evt event.Event) {
	if r == nil || !r.enabled || r.store == nil {
		return
	}
	r.mu.Lock()
	finished := r.finished
	r.mu.Unlock()
	if finished {
		return
	}
	r.store.record(r.traceID, evt, time.Since(r.startedAt).Milliseconds())
}

// Finish 结束本次 trace 并落盘。
func (r *Recorder) Finish(status string, err error) (Summary, error) {
	if r == nil || !r.enabled || r.store == nil {
		return Summary{}, nil
	}
	r.mu.Lock()
	if r.finished {
		r.mu.Unlock()
		return Summary{}, nil
	}
	r.finished = true
	r.mu.Unlock()
	return r.store.finish(r.traceID, status, err)
}

// NewID 生成适合放进 URL 和文件名的 traceId。
func NewID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return time.Now().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(b[:])
}

func sanitizeEvent(evt event.Event, maxChars int) event.Event {
	evt.Content = truncateString(evt.Content, maxChars)
	evt.Message = truncateString(evt.Message, maxChars)
	evt.Detail = truncateString(evt.Detail, maxChars)
	evt.Arguments = truncateString(evt.Arguments, maxChars)
	evt.Result = truncateString(evt.Result, maxChars)
	return evt
}

func truncateString(value string, maxChars int) string {
	if maxChars <= 0 || utf8.RuneCountInString(value) <= maxChars {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxChars]) + "...[trace已截断]"
}

func safeTraceID(traceID string) string {
	var b strings.Builder
	for _, r := range traceID {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "trace"
	}
	return b.String()
}

func copyTypeCounts(in map[event.Type]int) map[event.Type]int {
	out := make(map[event.Type]int, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

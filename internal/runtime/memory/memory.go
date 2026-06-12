package memory

import (
	"context"
	"time"

	"github.com/learn-demo/agent-go/internal/runtime/model"
)

// SessionRecord 对应 Java dodo-agent 的 AiSession，也对应数据库表 ai_session。
// 它既承担“给下一次请求构造历史上下文”的职责，也承担审计和前端会话详情展示职责。
type SessionRecord struct {
	// ID 是数据库主键。
	ID int64
	// SessionID 是 conversationId，同一个会话下可以有多条问答记录。
	SessionID string
	// AgentType 是 Agent 类型，例如 websearch。
	AgentType string
	// Question 是用户问题。
	Question string
	// Answer 是 AI 最终回复正文。
	Answer string
	// Thinking 是模型思考过程文本。
	Thinking string
	// Tools 是本次回答用到的工具名，逗号分隔。
	Tools string
	// Reference 是 reference 事件内容，通常是搜索引用 JSON。
	Reference string
	// Recommend 是推荐问题 JSON，当前 Go 版暂未生成。
	Recommend string
	// FirstResponseTime 是首次响应耗时，单位毫秒。
	FirstResponseTime int64
	// TotalResponseTime 是完整响应耗时，单位毫秒。
	TotalResponseTime int64
	// FileID 是文件或 PPT 关联 ID，WebSearch 当前为空。
	FileID string
	// CreateTime 是创建时间。
	CreateTime time.Time
	// UpdateTime 是更新时间。
	UpdateTime time.Time
}

// SaveQuestionRequest 对应 Java SaveQuestionRequest。
type SaveQuestionRequest struct {
	SessionID         string
	AgentType         string
	Question          string
	FileID            string
	Tools             string
	FirstResponseTime int64
}

// UpdateAnswerRequest 对应 Java UpdateAnswerRequest。
type UpdateAnswerRequest struct {
	ID                int64
	Answer            string
	Thinking          string
	Tools             string
	Reference         string
	Recommend         string
	FirstResponseTime int64
	TotalResponseTime int64
}

// Store 是会话记忆运行时的最小接口。
// Agent 只依赖这里，不直接依赖 MySQL 或具体 SQL。
type Store interface {
	// FindRecent 查询最近 N 条会话记录。返回顺序是时间正序，方便直接追加到 messages。
	FindRecent(ctx context.Context, sessionID string, maxRecords int) ([]SessionRecord, error)
	// SaveQuestion 先保存用户问题，返回新建记录 ID。
	SaveQuestion(ctx context.Context, req SaveQuestionRequest) (SessionRecord, error)
	// UpdateAnswer 在流结束时补齐 AI 回答、工具、引用和耗时。
	UpdateAnswer(ctx context.Context, req UpdateAnswerRequest) error
	// Close 释放底层资源。
	Close() error
}

// HistoryMessages 把数据库会话记录转换成模型消息。
// 这里只保留 user/assistant 问答，不把工具消息、thinking 或引用塞回上下文。
func HistoryMessages(records []SessionRecord) []model.Message {
	messages := make([]model.Message, 0, len(records)*2)
	for _, record := range records {
		if record.Question != "" {
			messages = append(messages, model.Message{
				Role:    model.RoleUser,
				Content: record.Question,
			})
		}
		if record.Answer != "" {
			messages = append(messages, model.Message{
				Role:    model.RoleAssistant,
				Content: record.Answer,
			})
		}
	}
	return messages
}

// NoopStore 是未开启持久化时的空实现。
// 它让 Agent 主流程无需到处判断 memory 是否为 nil。
type NoopStore struct{}

func (NoopStore) FindRecent(ctx context.Context, sessionID string, maxRecords int) ([]SessionRecord, error) {
	return nil, nil
}

func (NoopStore) SaveQuestion(ctx context.Context, req SaveQuestionRequest) (SessionRecord, error) {
	return SessionRecord{}, nil
}

func (NoopStore) UpdateAnswer(ctx context.Context, req UpdateAnswerRequest) error {
	return nil
}

func (NoopStore) Close() error {
	return nil
}

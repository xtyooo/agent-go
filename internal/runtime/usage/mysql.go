package usage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type MySQLConfig struct {
	DSN             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

type MySQLStore struct {
	db    *gorm.DB
	sqlDB *sql.DB
}

type AiModelUsage struct {
	ID               int64     `gorm:"column:id;primaryKey;autoIncrement"`
	RequestID        string    `gorm:"column:request_id;type:varchar(64);index:idx_model_usage_request"`
	TraceID          string    `gorm:"column:trace_id;type:varchar(96);index:idx_model_usage_trace"`
	ConversationID   string    `gorm:"column:conversation_id;type:varchar(128);index:idx_model_usage_conversation"`
	AgentType        string    `gorm:"column:agent_type;type:varchar(64);index:idx_model_usage_agent_time,priority:1"`
	Model            string    `gorm:"column:model;type:varchar(128);index:idx_model_usage_model_time,priority:1"`
	Stream           bool      `gorm:"column:stream"`
	PromptTokens     int       `gorm:"column:prompt_tokens"`
	CompletionTokens int       `gorm:"column:completion_tokens"`
	TotalTokens      int       `gorm:"column:total_tokens"`
	CachedTokens     int       `gorm:"column:cached_tokens"`
	ReasoningTokens  int       `gorm:"column:reasoning_tokens"`
	ElapsedMs        int64     `gorm:"column:elapsed_ms"`
	Success          bool      `gorm:"column:success"`
	Error            string    `gorm:"column:error;type:text"`
	CreatedAt        time.Time `gorm:"column:created_at;autoCreateTime;index:idx_model_usage_agent_time,priority:2;index:idx_model_usage_model_time,priority:2;index:idx_model_usage_created"`
}

func (AiModelUsage) TableName() string {
	return "ai_model_usage"
}

func NewMySQLStore(cfg MySQLConfig) (*MySQLStore, error) {
	dsn := strings.TrimSpace(cfg.DSN)
	if dsn == "" {
		return nil, fmt.Errorf("mysql dsn is required")
	}

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("open mysql with gorm: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get raw sql db: %w", err)
	}
	if cfg.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}
	return &MySQLStore{db: db, sqlDB: sqlDB}, nil
}

func (s *MySQLStore) EnsureSchema(ctx context.Context) error {
	if err := s.db.WithContext(ctx).AutoMigrate(&AiModelUsage{}); err != nil {
		return fmt.Errorf("auto migrate ai_model_usage: %w", err)
	}
	return nil
}

func (s *MySQLStore) Save(ctx context.Context, record Record) error {
	record = normalizeRecord(record)
	row := AiModelUsage{
		RequestID:        record.RequestID,
		TraceID:          record.TraceID,
		ConversationID:   record.ConversationID,
		AgentType:        record.AgentType,
		Model:            record.Model,
		Stream:           record.Stream,
		PromptTokens:     record.PromptTokens,
		CompletionTokens: record.CompletionTokens,
		TotalTokens:      record.TotalTokens,
		CachedTokens:     record.CachedTokens,
		ReasoningTokens:  record.ReasoningTokens,
		ElapsedMs:        record.ElapsedMs,
		Success:          record.Success,
		Error:            record.Error,
		CreatedAt:        record.CreatedAt,
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("insert model usage: %w", err)
	}
	return nil
}

func (s *MySQLStore) Overview(ctx context.Context, query Query) (Overview, error) {
	query = normalizeQuery(query)

	var rows []AiModelUsage
	db := s.db.WithContext(ctx).Model(&AiModelUsage{}).
		Where("created_at >= ? AND created_at <= ?", query.From, query.To)
	if query.AgentType != "" {
		db = db.Where("agent_type = ?", query.AgentType)
	}
	if query.Model != "" {
		db = db.Where("model = ?", query.Model)
	}
	if err := db.Order("created_at ASC").Order("id ASC").Find(&rows).Error; err != nil {
		return Overview{}, fmt.Errorf("query model usage: %w", err)
	}

	records := make([]Record, 0, len(rows))
	for _, row := range rows {
		records = append(records, row.toRecord())
	}
	return BuildOverview(records, query), nil
}

func (s *MySQLStore) Close() error {
	if s == nil || s.sqlDB == nil {
		return nil
	}
	return s.sqlDB.Close()
}

func (row AiModelUsage) toRecord() Record {
	return Record{
		ID:               row.ID,
		RequestID:        row.RequestID,
		TraceID:          row.TraceID,
		ConversationID:   row.ConversationID,
		AgentType:        row.AgentType,
		Model:            row.Model,
		Stream:           row.Stream,
		PromptTokens:     row.PromptTokens,
		CompletionTokens: row.CompletionTokens,
		TotalTokens:      row.TotalTokens,
		CachedTokens:     row.CachedTokens,
		ReasoningTokens:  row.ReasoningTokens,
		ElapsedMs:        row.ElapsedMs,
		Success:          row.Success,
		Error:            row.Error,
		CreatedAt:        row.CreatedAt,
	}
}

var _ Store = (*MySQLStore)(nil)
var _ SchemaStore = (*MySQLStore)(nil)

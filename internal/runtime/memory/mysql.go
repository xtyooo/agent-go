package memory

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

// MySQLStore 使用 GORM + MySQL 表 ai_session 保存会话记忆。
// 表字段参考 Java dodo-agent 的 AiSession 实体，使用 ORM 方式更接近 MyBatis-Plus 的开发体验。
type MySQLStore struct {
	db    *gorm.DB
	sqlDB *sql.DB
}

// MySQLConfig 是 MySQL 会话记忆配置。
type MySQLConfig struct {
	// DSN 是 go-sql-driver/mysql 的连接串。
	DSN string
	// MaxOpenConns 是最大打开连接数。
	MaxOpenConns int
	// MaxIdleConns 是最大空闲连接数。
	MaxIdleConns int
	// ConnMaxLifetime 是连接最大生命周期。
	ConnMaxLifetime time.Duration
}

// AiSession 是 ai_session 表的 GORM Model。
// 命名和字段语义对齐 Java AiSession，后续 SessionController 可以直接复用。
type AiSession struct {
	// ID 是主键 ID。
	ID int64 `gorm:"column:id;primaryKey;autoIncrement"`
	// SessionID 是 conversationId。
	SessionID string `gorm:"column:session_id;type:varchar(128);not null;index:idx_ai_session_session_create,priority:1"`
	// AgentType 是智能体类型，例如 websearch。
	AgentType string `gorm:"column:agent_type;type:varchar(64)"`
	// SessionName 是前端展示的会话名称，未命名时为空。
	SessionName string `gorm:"column:session_name;type:varchar(255)"`
	// Question 是用户问题。
	Question string `gorm:"column:question;type:mediumtext"`
	// Answer 是 AI 回复。
	Answer string `gorm:"column:answer;type:mediumtext"`
	// Tools 是涉及的工具名称，逗号分隔。
	Tools string `gorm:"column:tools;type:text"`
	// Reference 是参考链接 JSON。
	Reference string `gorm:"column:reference;type:mediumtext"`
	// FirstResponseTime 是首次响应耗时，单位毫秒。
	FirstResponseTime int64 `gorm:"column:first_response_time"`
	// TotalResponseTime 是整体响应耗时，单位毫秒。
	TotalResponseTime int64 `gorm:"column:total_response_time"`
	// CreateTime 是创建时间。字段名保持 Java 表结构 create_time。
	CreateTime time.Time `gorm:"column:create_time;autoCreateTime;index:idx_ai_session_session_create,priority:2"`
	// UpdateTime 是更新时间。字段名保持 Java 表结构 update_time。
	UpdateTime time.Time `gorm:"column:update_time;autoUpdateTime;index:idx_ai_session_update_time"`
	// Thinking 是思考过程。
	Thinking string `gorm:"column:thinking;type:mediumtext"`
	// FileID 是关联文件 ID，列名沿用 Java 的 fileid。
	FileID string `gorm:"column:fileid;type:varchar(255)"`
	// Recommend 是推荐问题 JSON。
	Recommend string `gorm:"column:recommend;type:mediumtext"`
}

func (AiSession) TableName() string {
	return "ai_session"
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

// EnsureSchema 使用 GORM AutoMigrate 创建或补齐 ai_session 表结构。
// 它替代了上一版手写 CREATE TABLE SQL，后续新增表时也可以沿用同一套迁移方式。
func (s *MySQLStore) EnsureSchema(ctx context.Context) error {
	if err := s.db.WithContext(ctx).AutoMigrate(&AiSession{}); err != nil {
		return fmt.Errorf("auto migrate ai_session: %w", err)
	}
	return nil
}

func (s *MySQLStore) FindRecent(ctx context.Context, sessionID string, maxRecords int) ([]SessionRecord, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || maxRecords <= 0 {
		return nil, nil
	}

	var rows []AiSession
	if err := s.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Order("create_time DESC").
		Order("id DESC").
		Limit(maxRecords).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("query recent sessions: %w", err)
	}

	records := make([]SessionRecord, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		records = append(records, rows[i].toRecord())
	}
	return records, nil
}

func (s *MySQLStore) SaveQuestion(ctx context.Context, req SaveQuestionRequest) (SessionRecord, error) {
	sessionName, err := s.findSessionName(ctx, req.SessionID)
	if err != nil {
		return SessionRecord{}, err
	}

	row := AiSession{
		SessionID:         req.SessionID,
		AgentType:         req.AgentType,
		SessionName:       sessionName,
		Question:          req.Question,
		Tools:             req.Tools,
		FirstResponseTime: req.FirstResponseTime,
		FileID:            req.FileID,
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return SessionRecord{}, fmt.Errorf("insert session question: %w", err)
	}
	return row.toRecord(), nil
}

func (s *MySQLStore) findSessionName(ctx context.Context, sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", nil
	}

	var row AiSession
	err := s.db.WithContext(ctx).
		Select("session_name").
		Where("session_id = ? AND session_name <> ''", sessionID).
		Order("update_time DESC").
		Order("id DESC").
		First(&row).Error
	if err == nil {
		return row.SessionName, nil
	}
	if err == gorm.ErrRecordNotFound {
		return "", nil
	}
	return "", fmt.Errorf("query session name: %w", err)
}

func (s *MySQLStore) UpdateAnswer(ctx context.Context, req UpdateAnswerRequest) error {
	if req.ID <= 0 {
		return nil
	}

	updates := map[string]any{
		"answer":              req.Answer,
		"thinking":            req.Thinking,
		"tools":               req.Tools,
		"reference":           req.Reference,
		"recommend":           req.Recommend,
		"first_response_time": req.FirstResponseTime,
		"total_response_time": req.TotalResponseTime,
		"update_time":         time.Now(),
	}
	if err := s.db.WithContext(ctx).
		Model(&AiSession{}).
		Where("id = ?", req.ID).
		Updates(updates).Error; err != nil {
		return fmt.Errorf("update session answer: %w", err)
	}
	return nil
}

func (s *MySQLStore) ListSessions(ctx context.Context, req ListSessionsRequest) ([]SessionSummary, int64, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	offset := req.Offset
	if offset < 0 {
		offset = 0
	}

	var total int64
	if err := s.sessionListBaseQuery(ctx, req).Distinct("session_id").Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count sessions: %w", err)
	}
	if total == 0 {
		return []SessionSummary{}, 0, nil
	}

	type sessionGroup struct {
		SessionID    string
		MessageCount int64
		CreateTime   time.Time
		UpdateTime   time.Time
	}
	var groups []sessionGroup
	if err := s.sessionListBaseQuery(ctx, req).
		Select("session_id, COUNT(*) AS message_count, MIN(create_time) AS create_time, MAX(update_time) AS update_time").
		Group("session_id").
		Order("update_time DESC").
		Order("session_id DESC").
		Limit(limit).
		Offset(offset).
		Find(&groups).Error; err != nil {
		return nil, 0, fmt.Errorf("query sessions: %w", err)
	}

	summaries := make([]SessionSummary, 0, len(groups))
	for _, group := range groups {
		var first AiSession
		if err := s.db.WithContext(ctx).
			Where("session_id = ?", group.SessionID).
			Order("create_time ASC").
			Order("id ASC").
			First(&first).Error; err != nil {
			return nil, 0, fmt.Errorf("query first session record: %w", err)
		}

		var last AiSession
		if err := s.db.WithContext(ctx).
			Where("session_id = ?", group.SessionID).
			Order("update_time DESC").
			Order("id DESC").
			First(&last).Error; err != nil {
			return nil, 0, fmt.Errorf("query last session record: %w", err)
		}

		summaries = append(summaries, SessionSummary{
			SessionID:     group.SessionID,
			SessionName:   last.SessionName,
			AgentType:     last.AgentType,
			FirstQuestion: first.Question,
			LastQuestion:  last.Question,
			LastAnswer:    last.Answer,
			MessageCount:  group.MessageCount,
			CreateTime:    group.CreateTime,
			UpdateTime:    group.UpdateTime,
		})
	}

	return summaries, total, nil
}

func (s *MySQLStore) FindBySession(ctx context.Context, sessionID string) ([]SessionRecord, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, nil
	}

	var rows []AiSession
	if err := s.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Order("create_time ASC").
		Order("id ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("query session detail: %w", err)
	}

	records := make([]SessionRecord, 0, len(rows))
	for _, row := range rows {
		records = append(records, row.toRecord())
	}
	return records, nil
}

func (s *MySQLStore) DeleteSession(ctx context.Context, sessionID string) (int64, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return 0, nil
	}

	result := s.db.WithContext(ctx).Where("session_id = ?", sessionID).Delete(&AiSession{})
	if result.Error != nil {
		return 0, fmt.Errorf("delete session: %w", result.Error)
	}
	return result.RowsAffected, nil
}

func (s *MySQLStore) RenameSession(ctx context.Context, sessionID string, name string) (int64, error) {
	sessionID = strings.TrimSpace(sessionID)
	name = strings.TrimSpace(name)
	if sessionID == "" {
		return 0, nil
	}

	result := s.db.WithContext(ctx).
		Model(&AiSession{}).
		Where("session_id = ?", sessionID).
		Updates(map[string]any{
			"session_name": name,
			"update_time":  time.Now(),
		})
	if result.Error != nil {
		return 0, fmt.Errorf("rename session: %w", result.Error)
	}
	return result.RowsAffected, nil
}

func (s *MySQLStore) sessionListBaseQuery(ctx context.Context, req ListSessionsRequest) *gorm.DB {
	db := s.db.WithContext(ctx).Model(&AiSession{})
	if agentType := strings.TrimSpace(req.AgentType); agentType != "" {
		db = db.Where("agent_type = ?", agentType)
	}
	if keyword := strings.TrimSpace(req.Keyword); keyword != "" {
		like := "%" + keyword + "%"
		db = db.Where("session_id LIKE ? OR session_name LIKE ? OR question LIKE ?", like, like, like)
	}
	return db
}

func (s *MySQLStore) Close() error {
	if s == nil || s.sqlDB == nil {
		return nil
	}
	return s.sqlDB.Close()
}

func (s AiSession) toRecord() SessionRecord {
	return SessionRecord{
		ID:                s.ID,
		SessionID:         s.SessionID,
		AgentType:         s.AgentType,
		SessionName:       s.SessionName,
		Question:          s.Question,
		Answer:            s.Answer,
		Thinking:          s.Thinking,
		Tools:             s.Tools,
		Reference:         s.Reference,
		Recommend:         s.Recommend,
		FirstResponseTime: s.FirstResponseTime,
		TotalResponseTime: s.TotalResponseTime,
		FileID:            s.FileID,
		CreateTime:        s.CreateTime,
		UpdateTime:        s.UpdateTime,
	}
}

var _ Store = (*MySQLStore)(nil)

package memory

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// MySQLStore 使用 MySQL 表 ai_session 保存会话记忆。
// 表字段参考 Java dodo-agent 的 AiSession 实体，便于后续和原项目行为对齐。
type MySQLStore struct {
	db *sql.DB
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

func NewMySQLStore(cfg MySQLConfig) (*MySQLStore, error) {
	dsn := strings.TrimSpace(cfg.DSN)
	if dsn == "" {
		return nil, fmt.Errorf("mysql dsn is required")
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	if cfg.MaxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		db.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}

	return &MySQLStore{db: db}, nil
}

// EnsureSchema 创建 ai_session 表和常用索引。
// 这是学习项目的便利实现；生产环境可以替换为独立 migration 工具。
func (s *MySQLStore) EnsureSchema(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS ai_session (
  id BIGINT NOT NULL AUTO_INCREMENT,
  session_id VARCHAR(128) NOT NULL,
  agent_type VARCHAR(64) NULL,
  question MEDIUMTEXT NULL,
  answer MEDIUMTEXT NULL,
  tools TEXT NULL,
  reference MEDIUMTEXT NULL,
  first_response_time BIGINT NULL,
  total_response_time BIGINT NULL,
  create_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  update_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  thinking MEDIUMTEXT NULL,
  fileid VARCHAR(255) NULL,
  recommend MEDIUMTEXT NULL,
  PRIMARY KEY (id),
  KEY idx_ai_session_session_create (session_id, create_time),
  KEY idx_ai_session_update_time (update_time)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`

	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("ensure ai_session schema: %w", err)
	}
	return nil
}

func (s *MySQLStore) FindRecent(ctx context.Context, sessionID string, maxRecords int) ([]SessionRecord, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || maxRecords <= 0 {
		return nil, nil
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT id, session_id, agent_type, question, answer, tools, reference,
       first_response_time, total_response_time, create_time, update_time,
       thinking, fileid, recommend
FROM (
  SELECT id, session_id, agent_type, question, answer, tools, reference,
         first_response_time, total_response_time, create_time, update_time,
         thinking, fileid, recommend
  FROM ai_session
  WHERE session_id = ?
  ORDER BY create_time DESC, id DESC
  LIMIT ?
) recent
ORDER BY create_time ASC, id ASC`, sessionID, maxRecords)
	if err != nil {
		return nil, fmt.Errorf("query recent sessions: %w", err)
	}
	defer rows.Close()

	var records []SessionRecord
	for rows.Next() {
		record, err := scanSessionRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent sessions: %w", err)
	}
	return records, nil
}

func (s *MySQLStore) SaveQuestion(ctx context.Context, req SaveQuestionRequest) (SessionRecord, error) {
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `
INSERT INTO ai_session
  (session_id, agent_type, question, tools, first_response_time, fileid, create_time, update_time)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		req.SessionID,
		req.AgentType,
		req.Question,
		nullString(req.Tools),
		nullInt64(req.FirstResponseTime),
		nullString(req.FileID),
		now,
		now,
	)
	if err != nil {
		return SessionRecord{}, fmt.Errorf("insert session question: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return SessionRecord{}, fmt.Errorf("read inserted session id: %w", err)
	}

	return SessionRecord{
		ID:                id,
		SessionID:         req.SessionID,
		AgentType:         req.AgentType,
		Question:          req.Question,
		Tools:             req.Tools,
		FirstResponseTime: req.FirstResponseTime,
		FileID:            req.FileID,
		CreateTime:        now,
		UpdateTime:        now,
	}, nil
}

func (s *MySQLStore) UpdateAnswer(ctx context.Context, req UpdateAnswerRequest) error {
	if req.ID <= 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE ai_session
SET answer = ?,
    thinking = ?,
    tools = ?,
    reference = ?,
    recommend = ?,
    first_response_time = ?,
    total_response_time = ?,
    update_time = ?
WHERE id = ?`,
		req.Answer,
		nullString(req.Thinking),
		nullString(req.Tools),
		nullString(req.Reference),
		nullString(req.Recommend),
		nullInt64(req.FirstResponseTime),
		nullInt64(req.TotalResponseTime),
		time.Now(),
		req.ID,
	)
	if err != nil {
		return fmt.Errorf("update session answer: %w", err)
	}
	return nil
}

func (s *MySQLStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSessionRecord(scanner rowScanner) (SessionRecord, error) {
	var record SessionRecord
	var agentType, question, answer, tools, reference, thinking, fileID, recommend sql.NullString
	var firstResponseTime, totalResponseTime sql.NullInt64

	err := scanner.Scan(
		&record.ID,
		&record.SessionID,
		&agentType,
		&question,
		&answer,
		&tools,
		&reference,
		&firstResponseTime,
		&totalResponseTime,
		&record.CreateTime,
		&record.UpdateTime,
		&thinking,
		&fileID,
		&recommend,
	)
	if err != nil {
		return SessionRecord{}, fmt.Errorf("scan session record: %w", err)
	}

	record.AgentType = agentType.String
	record.Question = question.String
	record.Answer = answer.String
	record.Tools = tools.String
	record.Reference = reference.String
	record.FirstResponseTime = firstResponseTime.Int64
	record.TotalResponseTime = totalResponseTime.Int64
	record.Thinking = thinking.String
	record.FileID = fileID.String
	record.Recommend = recommend.String
	return record, nil
}

func nullString(value string) sql.NullString {
	if strings.TrimSpace(value) == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func nullInt64(value int64) sql.NullInt64 {
	if value <= 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: value, Valid: true}
}

var _ Store = (*MySQLStore)(nil)

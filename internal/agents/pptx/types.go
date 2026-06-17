package pptx

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

type Status string

const (
	StatusInit        Status = "INIT"
	StatusRequirement Status = "REQUIREMENT"
	StatusSearch      Status = "SEARCH"
	StatusTemplate    Status = "TEMPLATE"
	StatusOutline     Status = "OUTLINE"
	StatusSchema      Status = "SCHEMA"
	StatusRender      Status = "RENDER"
	StatusSuccess     Status = "SUCCESS"
	StatusFailed      Status = "FAILED"
)

type Intent string

const (
	IntentCreate Intent = "CREATE_PPT"
	IntentModify Intent = "MODIFY_PPT"
	IntentResume Intent = "RESUME_PPT"
)

// Instance 对应 Java dodo-agent 的 AiPptInst。
// 第一版使用内存 Store 保存，字段保持和 Java 表结构接近，方便后续迁移到 GORM。
type Instance struct {
	ID             int64
	ConversationID string
	TemplateCode   string
	Status         Status
	Query          string
	Requirement    string
	SearchInfo     string
	Outline        string
	PPTSchema      string
	FileURL        string
	ErrorMsg       string
	CreateTime     time.Time
	UpdateTime     time.Time
}

func (i Instance) Clone() Instance {
	return i
}

type Store interface {
	Create(ctx context.Context, conversationID string, query string) (Instance, error)
	Latest(ctx context.Context, conversationID string) (Instance, bool, error)
	Get(ctx context.Context, id int64) (Instance, bool, error)
	Update(ctx context.Context, id int64, update func(*Instance)) (Instance, error)
}

type MemoryStore struct {
	mu     sync.Mutex
	nextID int64
	rows   map[int64]Instance
	latest map[string]int64
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		nextID: 1,
		rows:   make(map[int64]Instance),
		latest: make(map[string]int64),
	}
}

func (s *MemoryStore) Create(ctx context.Context, conversationID string, query string) (Instance, error) {
	if err := ctx.Err(); err != nil {
		return Instance{}, err
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return Instance{}, fmt.Errorf("conversation id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	inst := Instance{
		ID:             s.nextID,
		ConversationID: conversationID,
		Status:         StatusInit,
		Query:          strings.TrimSpace(query),
		CreateTime:     now,
		UpdateTime:     now,
	}
	s.nextID++
	s.rows[inst.ID] = inst
	s.latest[conversationID] = inst.ID
	return inst.Clone(), nil
}

func (s *MemoryStore) Latest(ctx context.Context, conversationID string) (Instance, bool, error) {
	if err := ctx.Err(); err != nil {
		return Instance{}, false, err
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return Instance{}, false, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.latest[conversationID]
	if !ok {
		return Instance{}, false, nil
	}
	inst, ok := s.rows[id]
	return inst.Clone(), ok, nil
}

func (s *MemoryStore) Get(ctx context.Context, id int64) (Instance, bool, error) {
	if err := ctx.Err(); err != nil {
		return Instance{}, false, err
	}
	if id <= 0 {
		return Instance{}, false, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	inst, ok := s.rows[id]
	return inst.Clone(), ok, nil
}

func (s *MemoryStore) Update(ctx context.Context, id int64, update func(*Instance)) (Instance, error) {
	if err := ctx.Err(); err != nil {
		return Instance{}, err
	}
	if id <= 0 {
		return Instance{}, fmt.Errorf("ppt instance id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	inst, ok := s.rows[id]
	if !ok {
		return Instance{}, fmt.Errorf("ppt instance %d not found", id)
	}
	if update != nil {
		update(&inst)
	}
	inst.UpdateTime = time.Now()
	s.rows[id] = inst
	s.latest[inst.ConversationID] = inst.ID
	return inst.Clone(), nil
}

type Template struct {
	Code        string
	Name        string
	StyleTags   string
	Description string
	SlideCount  int
	Schema      string
}

func defaultTemplates() []Template {
	return []Template{
		{
			Code:        "business-simple",
			Name:        "商务简洁模板",
			StyleTags:   "商务,简洁,汇报",
			Description: "适合企业汇报、方案介绍、项目总结。",
			SlideCount:  6,
			Schema: `{
  "pages": [
    {"pageType":"COVER","templatePageIndex":1,"fields":{"title":{"type":"text","fontLimit":18},"subtitle":{"type":"text","fontLimit":40}}},
    {"pageType":"CATALOG","templatePageIndex":2,"fields":{"items":{"type":"text","fontLimit":80}}},
    {"pageType":"CONTENT","templatePageIndex":3,"fields":{"title":{"type":"text","fontLimit":18},"bullets":{"type":"text","fontLimit":120}}},
    {"pageType":"CONTENT","templatePageIndex":4,"fields":{"title":{"type":"text","fontLimit":18},"bullets":{"type":"text","fontLimit":120}}},
    {"pageType":"COMPARE","templatePageIndex":5,"fields":{"title":{"type":"text","fontLimit":18},"left":{"type":"text","fontLimit":80},"right":{"type":"text","fontLimit":80}}},
    {"pageType":"END","templatePageIndex":6,"fields":{"summary":{"type":"text","fontLimit":60}}}
  ]
}`,
		},
		{
			Code:        "tech-blue",
			Name:        "科技蓝模板",
			StyleTags:   "科技,产品,趋势",
			Description: "适合技术趋势、产品方案、AI 主题分享。",
			SlideCount:  6,
			Schema: `{
  "pages": [
    {"pageType":"COVER","templatePageIndex":1,"fields":{"title":{"type":"text","fontLimit":18},"subtitle":{"type":"text","fontLimit":36},"visual":{"type":"image"}}},
    {"pageType":"CATALOG","templatePageIndex":2,"fields":{"items":{"type":"text","fontLimit":80}}},
    {"pageType":"CONTENT","templatePageIndex":3,"fields":{"title":{"type":"text","fontLimit":18},"bullets":{"type":"text","fontLimit":120},"image":{"type":"image"}}},
    {"pageType":"CONTENT","templatePageIndex":4,"fields":{"title":{"type":"text","fontLimit":18},"bullets":{"type":"text","fontLimit":120}}},
    {"pageType":"COMPARE","templatePageIndex":5,"fields":{"title":{"type":"text","fontLimit":18},"left":{"type":"text","fontLimit":80},"right":{"type":"text","fontLimit":80}}},
    {"pageType":"END","templatePageIndex":6,"fields":{"summary":{"type":"text","fontLimit":60}}}
  ]
}`,
		},
	}
}

func statusDesc(status Status) string {
	switch status {
	case StatusInit:
		return "初始化"
	case StatusRequirement:
		return "需求澄清"
	case StatusSearch:
		return "信息收集"
	case StatusTemplate:
		return "模板选择"
	case StatusOutline:
		return "大纲生成"
	case StatusSchema:
		return "Schema生成"
	case StatusRender:
		return "PPT渲染"
	case StatusSuccess:
		return "完成"
	case StatusFailed:
		return "失败"
	default:
		return string(status)
	}
}

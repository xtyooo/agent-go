package usage

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

type Metadata struct {
	RequestID      string `json:"requestId"`
	TraceID        string `json:"traceId"`
	ConversationID string `json:"conversationId"`
	AgentType      string `json:"agentType"`
}

type metadataKey struct{}

func WithMetadata(ctx context.Context, meta Metadata) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, metadataKey{}, meta)
}

func MetadataFromContext(ctx context.Context) Metadata {
	if ctx == nil {
		return Metadata{}
	}
	meta, _ := ctx.Value(metadataKey{}).(Metadata)
	return meta
}

type Record struct {
	ID               int64     `json:"id"`
	RequestID        string    `json:"requestId"`
	TraceID          string    `json:"traceId"`
	ConversationID   string    `json:"conversationId"`
	AgentType        string    `json:"agentType"`
	Model            string    `json:"model"`
	Stream           bool      `json:"stream"`
	PromptTokens     int       `json:"promptTokens"`
	CompletionTokens int       `json:"completionTokens"`
	TotalTokens      int       `json:"totalTokens"`
	CachedTokens     int       `json:"cachedTokens"`
	ReasoningTokens  int       `json:"reasoningTokens"`
	ElapsedMs        int64     `json:"elapsedMs"`
	Success          bool      `json:"success"`
	Error            string    `json:"error,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
}

type Query struct {
	From      time.Time
	To        time.Time
	AgentType string
	Model     string
	Interval  time.Duration
	Limit     int
}

type Summary struct {
	CallCount        int64   `json:"callCount"`
	ErrorCount       int64   `json:"errorCount"`
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	TotalTokens      int64   `json:"totalTokens"`
	CachedTokens     int64   `json:"cachedTokens"`
	ReasoningTokens  int64   `json:"reasoningTokens"`
	CacheHitRate     float64 `json:"cacheHitRate"`
	AvgTotalTokens   float64 `json:"avgTotalTokens"`
	AvgLatencyMs     float64 `json:"avgLatencyMs"`
}

type SeriesPoint struct {
	Time             time.Time `json:"time"`
	CallCount        int64     `json:"callCount"`
	PromptTokens     int64     `json:"promptTokens"`
	CompletionTokens int64     `json:"completionTokens"`
	TotalTokens      int64     `json:"totalTokens"`
	CachedTokens     int64     `json:"cachedTokens"`
}

type ModelAggregate struct {
	Model            string  `json:"model"`
	CallCount        int64   `json:"callCount"`
	ErrorCount       int64   `json:"errorCount"`
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	TotalTokens      int64   `json:"totalTokens"`
	CachedTokens     int64   `json:"cachedTokens"`
	ReasoningTokens  int64   `json:"reasoningTokens"`
	CacheHitRate     float64 `json:"cacheHitRate"`
	AvgLatencyMs     float64 `json:"avgLatencyMs"`
}

type Overview struct {
	From       time.Time        `json:"from"`
	To         time.Time        `json:"to"`
	IntervalMs int64            `json:"intervalMs"`
	Summary    Summary          `json:"summary"`
	Series     []SeriesPoint    `json:"series"`
	Models     []ModelAggregate `json:"models"`
	AgentTypes []string         `json:"agentTypes"`
	ModelNames []string         `json:"modelNames"`
	Records    []Record         `json:"records"`
}

type Store interface {
	Save(ctx context.Context, record Record) error
	Overview(ctx context.Context, query Query) (Overview, error)
	Close() error
}

type SchemaStore interface {
	EnsureSchema(ctx context.Context) error
}

type NoopStore struct{}

func (NoopStore) Save(context.Context, Record) error {
	return nil
}

func (NoopStore) Overview(context.Context, Query) (Overview, error) {
	return Overview{
		Series:     []SeriesPoint{},
		Models:     []ModelAggregate{},
		AgentTypes: []string{},
		ModelNames: []string{},
		Records:    []Record{},
	}, nil
}

func (NoopStore) Close() error {
	return nil
}

type MemoryStore struct {
	mu      sync.RWMutex
	nextID  int64
	records []Record
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

func (s *MemoryStore) Save(ctx context.Context, record Record) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	record = normalizeRecord(record)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	record.ID = s.nextID
	s.records = append(s.records, record)
	return nil
}

func (s *MemoryStore) Overview(ctx context.Context, query Query) (Overview, error) {
	if err := ctxErr(ctx); err != nil {
		return Overview{}, err
	}
	query = normalizeQuery(query)

	s.mu.RLock()
	defer s.mu.RUnlock()

	matched := make([]Record, 0, len(s.records))
	for _, record := range s.records {
		if matches(record, query) {
			matched = append(matched, record)
		}
	}
	return BuildOverview(matched, query), nil
}

func (s *MemoryStore) Close() error {
	return nil
}

func BuildOverview(records []Record, query Query) Overview {
	query = normalizeQuery(query)
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].CreatedAt.Before(records[j].CreatedAt)
	})

	summary := Summary{}
	seriesMap := map[int64]*SeriesPoint{}
	modelMap := map[string]*ModelAggregate{}
	agentTypes := map[string]struct{}{}
	modelNames := map[string]struct{}{}

	for _, record := range records {
		addSummary(&summary, record)
		bucket := bucketStart(record.CreatedAt, query.From, query.Interval)
		point := seriesMap[bucket.UnixMilli()]
		if point == nil {
			point = &SeriesPoint{Time: bucket}
			seriesMap[bucket.UnixMilli()] = point
		}
		point.CallCount++
		point.PromptTokens += int64(record.PromptTokens)
		point.CompletionTokens += int64(record.CompletionTokens)
		point.TotalTokens += int64(record.TotalTokens)
		point.CachedTokens += int64(record.CachedTokens)

		modelName := record.Model
		if strings.TrimSpace(modelName) == "" {
			modelName = "unknown"
		}
		aggregate := modelMap[modelName]
		if aggregate == nil {
			aggregate = &ModelAggregate{Model: modelName}
			modelMap[modelName] = aggregate
		}
		addModelAggregate(aggregate, record)

		if record.AgentType != "" {
			agentTypes[record.AgentType] = struct{}{}
		}
		if record.Model != "" {
			modelNames[record.Model] = struct{}{}
		}
	}
	finalizeSummary(&summary)

	series := fillSeries(query, seriesMap)
	models := make([]ModelAggregate, 0, len(modelMap))
	for _, aggregate := range modelMap {
		finalizeModelAggregate(aggregate)
		models = append(models, *aggregate)
	}
	sort.SliceStable(models, func(i, j int) bool {
		if models[i].TotalTokens == models[j].TotalTokens {
			return models[i].Model < models[j].Model
		}
		return models[i].TotalTokens > models[j].TotalTokens
	})

	recent := append([]Record(nil), records...)
	sort.SliceStable(recent, func(i, j int) bool {
		return recent[i].CreatedAt.After(recent[j].CreatedAt)
	})
	if query.Limit > 0 && len(recent) > query.Limit {
		recent = recent[:query.Limit]
	}

	return Overview{
		From:       query.From,
		To:         query.To,
		IntervalMs: query.Interval.Milliseconds(),
		Summary:    summary,
		Series:     series,
		Models:     models,
		AgentTypes: sortedKeys(agentTypes),
		ModelNames: sortedKeys(modelNames),
		Records:    recent,
	}
}

func normalizeRecord(record Record) Record {
	record.RequestID = strings.TrimSpace(record.RequestID)
	record.TraceID = strings.TrimSpace(record.TraceID)
	record.ConversationID = strings.TrimSpace(record.ConversationID)
	record.AgentType = strings.TrimSpace(record.AgentType)
	record.Model = strings.TrimSpace(record.Model)
	record.Error = strings.TrimSpace(record.Error)
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now()
	}
	return record
}

func normalizeQuery(query Query) Query {
	now := time.Now()
	if query.To.IsZero() {
		query.To = now
	}
	if query.From.IsZero() || query.From.After(query.To) {
		query.From = query.To.Add(-24 * time.Hour)
	}
	if query.Interval <= 0 {
		query.Interval = defaultInterval(query.To.Sub(query.From))
	}
	if query.Limit <= 0 {
		query.Limit = 100
	}
	if query.Limit > 500 {
		query.Limit = 500
	}
	query.AgentType = strings.TrimSpace(query.AgentType)
	query.Model = strings.TrimSpace(query.Model)
	return query
}

func defaultInterval(span time.Duration) time.Duration {
	switch {
	case span <= 8*time.Hour:
		return 30 * time.Minute
	case span <= 36*time.Hour:
		return time.Hour
	case span <= 8*24*time.Hour:
		return 12 * time.Hour
	default:
		return 24 * time.Hour
	}
}

func matches(record Record, query Query) bool {
	if !record.CreatedAt.IsZero() && record.CreatedAt.Before(query.From) {
		return false
	}
	if !record.CreatedAt.IsZero() && record.CreatedAt.After(query.To) {
		return false
	}
	if query.AgentType != "" && record.AgentType != query.AgentType {
		return false
	}
	if query.Model != "" && record.Model != query.Model {
		return false
	}
	return true
}

func addSummary(summary *Summary, record Record) {
	summary.CallCount++
	if !record.Success {
		summary.ErrorCount++
	}
	summary.PromptTokens += int64(record.PromptTokens)
	summary.CompletionTokens += int64(record.CompletionTokens)
	summary.TotalTokens += int64(record.TotalTokens)
	summary.CachedTokens += int64(record.CachedTokens)
	summary.ReasoningTokens += int64(record.ReasoningTokens)
	summary.AvgLatencyMs += float64(record.ElapsedMs)
}

func finalizeSummary(summary *Summary) {
	if summary.CallCount > 0 {
		summary.AvgTotalTokens = float64(summary.TotalTokens) / float64(summary.CallCount)
		summary.AvgLatencyMs = summary.AvgLatencyMs / float64(summary.CallCount)
	}
	if summary.PromptTokens > 0 {
		summary.CacheHitRate = float64(summary.CachedTokens) / float64(summary.PromptTokens)
	}
}

func addModelAggregate(aggregate *ModelAggregate, record Record) {
	aggregate.CallCount++
	if !record.Success {
		aggregate.ErrorCount++
	}
	aggregate.PromptTokens += int64(record.PromptTokens)
	aggregate.CompletionTokens += int64(record.CompletionTokens)
	aggregate.TotalTokens += int64(record.TotalTokens)
	aggregate.CachedTokens += int64(record.CachedTokens)
	aggregate.ReasoningTokens += int64(record.ReasoningTokens)
	aggregate.AvgLatencyMs += float64(record.ElapsedMs)
}

func finalizeModelAggregate(aggregate *ModelAggregate) {
	if aggregate.CallCount > 0 {
		aggregate.AvgLatencyMs = aggregate.AvgLatencyMs / float64(aggregate.CallCount)
	}
	if aggregate.PromptTokens > 0 {
		aggregate.CacheHitRate = float64(aggregate.CachedTokens) / float64(aggregate.PromptTokens)
	}
}

func fillSeries(query Query, points map[int64]*SeriesPoint) []SeriesPoint {
	series := make([]SeriesPoint, 0)
	start := bucketStart(query.From, query.From, query.Interval)
	for cursor := start; !cursor.After(query.To); cursor = cursor.Add(query.Interval) {
		key := cursor.UnixMilli()
		if point := points[key]; point != nil {
			series = append(series, *point)
		} else {
			series = append(series, SeriesPoint{Time: cursor})
		}
	}
	return series
}

func bucketStart(value time.Time, from time.Time, interval time.Duration) time.Time {
	if interval <= 0 {
		return value
	}
	if value.Before(from) {
		return from
	}
	offset := value.Sub(from)
	return from.Add(offset / interval * interval).Truncate(time.Second)
}

func sortedKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

var _ Store = (*MemoryStore)(nil)
var _ Store = NoopStore{}

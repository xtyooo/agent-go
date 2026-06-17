package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const tavilyMaxQueryChars = 400

type WebSearchTool struct {
	// apiKey 是 Tavily API 密钥；为空时降级到 mock 搜索。
	apiKey string
	// endpoint 是 Tavily Search API 地址。
	endpoint string
	// projectID 是可选项目 ID，会写入 X-Project-ID 请求头。
	projectID string
	// searchDepth 控制 Tavily 搜索深度，例如 basic。
	searchDepth string
	// maxResults 限制单次 Tavily 搜索结果数量。
	maxResults int
	// client 执行 Tavily HTTP 请求。
	client *http.Client
	// fallback 在没有配置 Tavily key 时提供确定性的 mock 搜索结果。
	fallback *WebSearchMockTool
}

type SearchResult struct {
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Content string  `json:"content"`
	Score   float64 `json:"score,omitempty"`
}

// WebSearchConfig 是 web_search 工具的外部配置。
// 它通常来自 application.yaml，由 main.go 显式传入，避免工具层自己读取环境变量。
type WebSearchConfig struct {
	// APIKey 是 Tavily API 密钥。
	APIKey string
	// Endpoint 是 Tavily Search API 地址。
	Endpoint string
	// ProjectID 是可选项目 ID。
	ProjectID string
	// SearchDepth 是 Tavily 搜索深度，例如 basic。
	SearchDepth string
	// MaxResults 是最大搜索结果数。
	MaxResults int
	// Timeout 是 Tavily HTTP 请求超时时间。
	Timeout time.Duration
}

func NewWebSearchTool(cfg WebSearchConfig) *WebSearchTool {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		endpoint = "https://api.tavily.com/search"
	}
	searchDepth := strings.TrimSpace(cfg.SearchDepth)
	if searchDepth == "" {
		searchDepth = "basic"
	}
	maxResults := cfg.MaxResults
	if maxResults <= 0 {
		maxResults = 5
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}

	return &WebSearchTool{
		apiKey:      strings.TrimSpace(cfg.APIKey),
		endpoint:    endpoint,
		projectID:   strings.TrimSpace(cfg.ProjectID),
		searchDepth: searchDepth,
		maxResults:  maxResults,
		client: &http.Client{
			Timeout: timeout,
		},
		fallback: NewWebSearchMockTool(),
	}
}

func (t *WebSearchTool) Definition() Definition {
	return Definition{
		Name:        "web_search",
		Description: "Search the web with Tavily HTTP API when application.yaml tools.tavily.api-key is configured; otherwise return deterministic mock search results.",
		Schema:      searchSchema(),
	}
}

func (t *WebSearchTool) Execute(ctx context.Context, input Input) (Result, error) {
	originalQuery := StringArg(input.Arguments, "query")
	query := normalizeSearchQuery(originalQuery, tavilyMaxQueryChars)
	if query == "" {
		return Result{}, fmt.Errorf("query is required")
	}

	if strings.TrimSpace(t.apiKey) == "" {
		return t.fallbackResult(ctx, input, "application.yaml tools.tavily.api-key is not configured")
	}

	requestBody := tavilySearchRequest{
		Query:       query,
		SearchDepth: t.searchDepth,
		MaxResults:  t.maxResults,
	}
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return Result{}, fmt.Errorf("encode tavily search request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(payload))
	if err != nil {
		return Result{}, fmt.Errorf("create tavily search request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.apiKey)
	if t.projectID != "" {
		req.Header.Set("X-Project-ID", t.projectID)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("call tavily search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Result{}, fmt.Errorf("tavily search returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	var decoded tavilySearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return Result{}, fmt.Errorf("decode tavily search response: %w", err)
	}

	results := make([]SearchResult, 0, len(decoded.Results))
	for _, item := range decoded.Results {
		results = append(results, SearchResult{
			Title:   item.Title,
			URL:     item.URL,
			Content: item.Content,
			Score:   item.Score,
		})
	}

	return Result{
		Name:    "web_search",
		Content: formatSearchContent(decoded.Answer, results),
		Data: map[string]any{
			"provider":        "tavily",
			"query":           query,
			"query_truncated": query != strings.TrimSpace(originalQuery),
			"answer":          decoded.Answer,
			"results":         results,
			"request_id":      decoded.RequestID,
		},
	}, nil
}

type tavilySearchRequest struct {
	Query       string `json:"query"`
	SearchDepth string `json:"search_depth,omitempty"`
	MaxResults  int    `json:"max_results,omitempty"`
}

type tavilySearchResponse struct {
	Answer    string `json:"answer"`
	RequestID string `json:"request_id"`
	Results   []struct {
		Title   string  `json:"title"`
		URL     string  `json:"url"`
		Content string  `json:"content"`
		Score   float64 `json:"score"`
	} `json:"results"`
}

func formatSearchContent(answer string, results []SearchResult) string {
	var b strings.Builder
	if strings.TrimSpace(answer) != "" {
		b.WriteString("Answer: ")
		b.WriteString(strings.TrimSpace(answer))
		b.WriteString("\n\n")
	}

	for i, result := range results {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(fmt.Sprintf("[%d] %s\n", i+1, emptyFallback(result.Title, "Untitled")))
		b.WriteString(result.URL)
		if strings.TrimSpace(result.Content) != "" {
			b.WriteString("\n")
			b.WriteString(strings.TrimSpace(result.Content))
		}
	}

	if b.Len() == 0 {
		return "No search results returned."
	}
	return b.String()
}

func emptyFallback(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func normalizeSearchQuery(query string, maxChars int) string {
	query = strings.Join(strings.Fields(strings.TrimSpace(query)), " ")
	if maxChars <= 0 {
		return query
	}
	runes := []rune(query)
	if len(runes) <= maxChars {
		return query
	}
	return strings.TrimSpace(string(runes[:maxChars]))
}

func (t *WebSearchTool) fallbackResult(ctx context.Context, input Input, reason string) (Result, error) {
	result, err := t.fallback.Execute(ctx, input)
	if err != nil {
		return Result{}, err
	}
	result.Name = "web_search"
	if result.Data == nil {
		result.Data = map[string]any{}
	}
	result.Data["provider"] = "mock"
	result.Data["fallback_reason"] = reason
	return result, nil
}

package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type OpenAICompatible struct {
	// baseURL 是 OpenAI-compatible 服务地址，不包含尾部斜杠。
	baseURL string
	// apiKey 用于 Authorization Bearer。
	apiKey string
	// model 是请求中的 model 字段。
	model string
	// httpClient 复用连接；流式请求不设置整体 Timeout，由 ctx 控制生命周期。
	httpClient *http.Client
	// logger 输出模型层观测日志。
	logger *slog.Logger
}

type OpenAIConfig struct {
	// BaseURL 示例：https://dashscope.aliyuncs.com/compatible-mode
	BaseURL string
	// APIKey 是模型平台密钥。
	APIKey string
	// Model 是模型名称。
	Model string
	// Logger 可选；为空时使用 slog.Default。
	Logger *slog.Logger
}

func NewOpenAICompatible(cfg OpenAIConfig) (*OpenAICompatible, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("model base URL is required")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("model API key is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("model name is required")
	}

	return &OpenAICompatible{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		httpClient: &http.Client{
			Timeout: 0,
		},
		logger: cfg.Logger,
	}, nil
}

func (m *OpenAICompatible) Generate(ctx context.Context, req Request) (Response, error) {
	logger := m.log()
	if req.RequestID != "" {
		logger = logger.With("request_id", req.RequestID)
	}
	startedAt := time.Now()
	logger.Info("\U0001F9E0 模型非流式请求开始",
		"model", m.model,
		"message_count", len(req.Messages),
		"tool_schema_count", len(req.Tools),
		"temperature", req.Temperature,
		"max_tokens", req.MaxTokens,
	)

	body := chatCompletionRequest{
		Model:       m.model,
		Messages:    toOpenAIMessages(req.Messages),
		Tools:       toOpenAITools(req.Tools),
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      false,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("encode model request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return Response{}, fmt.Errorf("create model request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+m.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(httpReq)
	if err != nil {
		logger.Error("\U0000274C 模型非流式请求失败",
			"model", m.model,
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		return Response{}, fmt.Errorf("call model: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		logger.Error("\U0000274C 模型非流式请求返回异常状态码",
			"model", m.model,
			"status", resp.StatusCode,
			"body_chars", len(msg),
			"elapsed_ms", elapsedMillis(startedAt),
		)
		return Response{}, fmt.Errorf("model returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	var decoded chatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		logger.Error("\U0000274C 模型非流式响应解析失败",
			"model", m.model,
			"status", resp.StatusCode,
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		return Response{}, fmt.Errorf("decode model response: %w", err)
	}

	var b strings.Builder
	var toolCalls []ToolCall
	for _, choice := range decoded.Choices {
		b.WriteString(choice.Message.Content)
		toolCalls = append(toolCalls, toModelToolCalls(choice.Message.ToolCalls)...)
	}

	logger.Info("\U00002705 模型非流式请求完成",
		"model", m.model,
		"status", resp.StatusCode,
		"choice_count", len(decoded.Choices),
		"content_chars", b.Len(),
		"tool_call_count", len(toolCalls),
		"elapsed_ms", elapsedMillis(startedAt),
	)
	return Response{Content: b.String(), ToolCalls: toolCalls}, nil
}

func (m *OpenAICompatible) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	logger := m.log()
	if req.RequestID != "" {
		logger = logger.With("request_id", req.RequestID)
	}
	startedAt := time.Now()
	logger.Info("\U0001F9E0 模型流式请求开始",
		"model", m.model,
		"message_count", len(req.Messages),
		"tool_schema_count", len(req.Tools),
		"temperature", req.Temperature,
		"max_tokens", req.MaxTokens,
	)

	body := chatCompletionRequest{
		Model:       m.model,
		Messages:    toOpenAIMessages(req.Messages),
		Tools:       toOpenAITools(req.Tools),
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      true,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode model request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create model request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+m.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := m.httpClient.Do(httpReq)
	if err != nil {
		logger.Error("\U0000274C 模型流式请求失败",
			"model", m.model,
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		return nil, fmt.Errorf("call model stream: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		logger.Error("\U0000274C 模型流式请求返回异常状态码",
			"model", m.model,
			"status", resp.StatusCode,
			"body_chars", len(msg),
			"elapsed_ms", elapsedMillis(startedAt),
		)
		return nil, fmt.Errorf("model returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	// 返回 channel 后，真正读取 resp.Body 的工作在 goroutine 中进行；
	// 这对应 Java Reactor stream 的异步数据流。
	chunks := make(chan Chunk)
	go m.streamChunks(ctx, logger, resp, chunks, startedAt)

	return chunks, nil
}

func (m *OpenAICompatible) streamChunks(ctx context.Context, logger *slog.Logger, resp *http.Response, chunks chan<- Chunk, startedAt time.Time) {
	defer close(chunks)
	defer resp.Body.Close()
	headersMs := elapsedMillis(startedAt)

	logger.Info("\U0001F4E5 模型流 Scanner 已开始读取响应体",
		"model", m.model,
		"status", resp.StatusCode,
		"buffer_max_bytes", 1024*1024,
		"model_http_headers_ms", headersMs,
	)

	// Scanner 按行读取 SSE。模型平台一般输出形如：data: {...}\n\n。
	scanner := bufio.NewScanner(resp.Body)
	// 默认 Scanner token 上限太小，工具参数或长文本 chunk 可能超过 64K。
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineCount := 0
	dataLineCount := 0
	emptyLineCount := 0
	commentLineCount := 0
	contentChunkCount := 0
	contentChars := 0
	toolDeltaCount := 0
	firstChunkMs := int64(-1)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		lineCount++
		if line == "" || strings.HasPrefix(line, ":") {
			if line == "" {
				emptyLineCount++
			} else {
				commentLineCount++
			}
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		dataLineCount++
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			logger.Info("\U00002705 模型流 Scanner 读取完成，已收到 DONE 标记",
				"model", m.model,
				"line_count", lineCount,
				"data_line_count", dataLineCount,
				"empty_line_count", emptyLineCount,
				"comment_line_count", commentLineCount,
				"content_chunk_count", contentChunkCount,
				"content_chars", contentChars,
				"tool_delta_count", toolDeltaCount,
				"model_http_headers_ms", headersMs,
				"first_model_chunk_ms", firstChunkMs,
				"elapsed_ms", elapsedMillis(startedAt),
			)
			sendChunk(ctx, chunks, Chunk{Done: true})
			return
		}

		// data 行只负责拆出一个模型 delta；tool_call arguments 的跨 chunk 合并交给 Agent roundState。
		chunk, err := parseStreamChunk([]byte(data))
		if err != nil {
			logger.Error("\U0000274C 模型流 chunk 解析失败",
				"model", m.model,
				"line_count", lineCount,
				"data_line_count", dataLineCount,
				"data_chars", len(data),
				"elapsed_ms", elapsedMillis(startedAt),
				"error", err,
			)
			sendChunk(ctx, chunks, Chunk{Err: fmt.Errorf("parse model stream chunk: %w", err)})
			return
		}
		if chunk.Content == "" && len(chunk.ToolCalls) == 0 {
			continue
		}
		if firstChunkMs < 0 {
			firstChunkMs = elapsedMillis(startedAt)
		}
		if chunk.Content != "" {
			contentChunkCount++
			contentChars += len(chunk.Content)
		}
		if len(chunk.ToolCalls) > 0 {
			toolDeltaCount += len(chunk.ToolCalls)
		}
		if !sendChunk(ctx, chunks, chunk) {
			logger.Warn("\U0001F6D1 模型流下游消费者已取消",
				"model", m.model,
				"line_count", lineCount,
				"data_line_count", dataLineCount,
				"content_chunk_count", contentChunkCount,
				"content_chars", contentChars,
				"tool_delta_count", toolDeltaCount,
				"elapsed_ms", elapsedMillis(startedAt),
				"error", ctx.Err(),
			)
			return
		}
	}

	if err := scanner.Err(); err != nil {
		logger.Error("\U0000274C 模型流 Scanner 读取失败",
			"model", m.model,
			"line_count", lineCount,
			"data_line_count", dataLineCount,
			"content_chunk_count", contentChunkCount,
			"content_chars", contentChars,
			"tool_delta_count", toolDeltaCount,
			"elapsed_ms", elapsedMillis(startedAt),
			"error", err,
		)
		sendChunk(ctx, chunks, Chunk{Err: fmt.Errorf("read model stream: %w", err)})
		return
	}

	logger.Warn("\U000026A0 模型流结束但未收到 DONE 标记",
		"model", m.model,
		"line_count", lineCount,
		"data_line_count", dataLineCount,
		"content_chunk_count", contentChunkCount,
		"content_chars", contentChars,
		"tool_delta_count", toolDeltaCount,
		"model_http_headers_ms", headersMs,
		"first_model_chunk_ms", firstChunkMs,
		"elapsed_ms", elapsedMillis(startedAt),
	)
}

func sendChunk(ctx context.Context, out chan<- Chunk, chunk Chunk) bool {
	select {
	case <-ctx.Done():
		return false
	case out <- chunk:
		return true
	}
}

func (m *OpenAICompatible) log() *slog.Logger {
	if m.logger == nil {
		return slog.Default()
	}
	return m.logger
}

func elapsedMillis(startedAt time.Time) int64 {
	return time.Since(startedAt).Milliseconds()
}

func toOpenAIMessages(messages []Message) []openAIMessage {
	out := make([]openAIMessage, 0, len(messages))
	for _, msg := range messages {
		out = append(out, openAIMessage{
			Role:       string(msg.Role),
			Content:    msg.Content,
			Name:       msg.Name,
			ToolCallID: msg.ToolCallID,
			ToolCalls:  toOpenAIMessageToolCalls(msg.ToolCalls),
		})
	}
	return out
}

func parseStreamChunk(data []byte) (Chunk, error) {
	var resp chatCompletionStreamResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return Chunk{}, err
	}

	var b strings.Builder
	var toolCalls []ToolCall
	for _, choice := range resp.Choices {
		b.WriteString(choice.Delta.Content)
		toolCalls = append(toolCalls, toModelToolCalls(choice.Delta.ToolCalls)...)
	}
	return Chunk{Content: b.String(), ToolCalls: toolCalls}, nil
}

func toOpenAITools(tools []ToolDefinition) []openAITool {
	out := make([]openAITool, 0, len(tools))
	for _, current := range tools {
		out = append(out, openAITool{
			Type: "function",
			Function: openAIFunction{
				Name:        current.Name,
				Description: current.Description,
				Parameters:  current.Schema,
			},
		})
	}
	return out
}

func toOpenAIMessageToolCalls(toolCalls []ToolCall) []openAIToolCall {
	out := make([]openAIToolCall, 0, len(toolCalls))
	for _, current := range toolCalls {
		out = append(out, openAIToolCall{
			ID:   current.ID,
			Type: "function",
			Function: openAIFunctionCall{
				Name:      current.Name,
				Arguments: current.Arguments,
			},
		})
	}
	return out
}

func toModelToolCalls(toolCalls []openAIToolCall) []ToolCall {
	out := make([]ToolCall, 0, len(toolCalls))
	for _, current := range toolCalls {
		out = append(out, ToolCall{
			ID:        current.ID,
			Index:     current.Index,
			Name:      current.Function.Name,
			Arguments: current.Function.Arguments,
		})
	}
	return out
}

type chatCompletionRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Tools       []openAITool    `json:"tools,omitempty"`
	Temperature float64         `json:"temperature,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Stream      bool            `json:"stream"`
}

// openAIMessage 是 OpenAI-compatible API 的原始 message 形状。
// 注意 ToolCallID 使用下划线字段名 tool_call_id，和内部 Event 的 JSON 字段不同。
type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content"`
	Name       string           `json:"name,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
}

// openAITool 对应 OpenAI tools 数组中的 function tool。
type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// openAIToolCall 同时用于非流式 message.tool_calls 和流式 delta.tool_calls。
type openAIToolCall struct {
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type,omitempty"`
	Index    int                `json:"index,omitempty"`
	Function openAIFunctionCall `json:"function"`
}

type openAIFunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content   string           `json:"content"`
			ToolCalls []openAIToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
}

type chatCompletionStreamResponse struct {
	Choices []struct {
		Delta struct {
			Content   string           `json:"content"`
			ToolCalls []openAIToolCall `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
}

var _ Model = (*OpenAICompatible)(nil)

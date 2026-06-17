package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/learn-demo/agent-go/internal/agents/planexecute"
	"github.com/learn-demo/agent-go/internal/agents/pptx"
	"github.com/learn-demo/agent-go/internal/agents/skills"
	"github.com/learn-demo/agent-go/internal/agents/websearch"
	httpapi "github.com/learn-demo/agent-go/internal/api/http"
	"github.com/learn-demo/agent-go/internal/infra/config"
	"github.com/learn-demo/agent-go/internal/runtime/agent"
	"github.com/learn-demo/agent-go/internal/runtime/contextx"
	mcpruntime "github.com/learn-demo/agent-go/internal/runtime/mcp"
	"github.com/learn-demo/agent-go/internal/runtime/memory"
	"github.com/learn-demo/agent-go/internal/runtime/model"
	"github.com/learn-demo/agent-go/internal/runtime/skill"
	"github.com/learn-demo/agent-go/internal/runtime/task"
	"github.com/learn-demo/agent-go/internal/runtime/tool"
	"github.com/learn-demo/agent-go/internal/runtime/trace"
)

func main() {
	configPath := flag.String("config", "application.yaml", "application yaml config path")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("\U0000274C 应用配置加载失败", "config", *configPath, "error", err)
		os.Exit(1)
	}
	logger.Info("\U0001F4C4 应用配置已加载",
		"config", *configPath,
		"server_port", cfg.Server.Port,
		"model", cfg.Model.Name,
		"model_base_url", cfg.Model.BaseURL,
		"agent_max_rounds", cfg.Agent.MaxRounds,
		"memory_enabled", cfg.Memory.Enabled,
		"memory_max_history_records", cfg.Memory.MaxHistoryRecords,
		"context_max_input_tokens", cfg.Context.MaxInputTokens,
		"context_max_history_tokens", cfg.Context.MaxHistoryTokens,
		"tavily_enabled", cfg.Tools.Tavily.APIKey != "",
		"tavily_endpoint", cfg.Tools.Tavily.Endpoint,
		"skills_enabled", len(cfg.Skills.Directories) > 0,
		"skills_directories", strings.Join(cfg.Skills.Directories, ","),
		"mcp_server_count", len(cfg.MCP.Servers),
		"trace_enabled", cfg.Observability.Trace.Enabled,
		"trace_directory", cfg.Observability.Trace.Directory,
	)

	chatModel, err := model.NewOpenAICompatible(model.OpenAIConfig{
		BaseURL: cfg.Model.BaseURL,
		APIKey:  cfg.Model.APIKey,
		Model:   cfg.Model.Name,
		Logger:  logger,
	})
	if err != nil {
		logger.Error("\U0000274C 模型运行时配置无效", "error", err)
		os.Exit(1)
	}

	skillManager := skill.NewManager(skill.Config{
		Directories: cfg.Skills.Directories,
		AutoReload:  cfg.Skills.AutoReload,
	}, logger)
	skillCount := 0
	if skillManager.Enabled() {
		loadedSkills, err := skillManager.List(context.Background())
		if err != nil {
			logger.Warn("\U000026A0 Skills Runtime 初始化扫描失败，将继续启动但技能不可用", "error", err)
		} else {
			skillCount = len(loadedSkills)
			logger.Info("\U0001F9E9 Skills Runtime 已启用",
				"skill_count", skillCount,
				"directories", strings.Join(cfg.Skills.Directories, ","),
				"auto_reload", cfg.Skills.AutoReload,
			)
		}
	} else {
		logger.Info("\U0001F9E9 Skills Runtime 未配置目录，read_skill 工具不会注册")
	}

	var readSkillTool tool.Tool
	if skillManager.Enabled() {
		readSkillTool = tool.NewReadSkillTool(skillManager, logger)
	}
	loadedMCP, err := mcpruntime.LoadTools(context.Background(), toMCPRuntimeConfig(cfg), logger)
	if err != nil {
		logger.Error("❌ MCP Runtime 初始化失败", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := loadedMCP.Close(); err != nil {
			logger.Warn("⚠ MCP Runtime 关闭失败", "error", err)
		}
	}()

	tools, err := tool.NewDefaultRegistry(tool.DefaultRegistryConfig{
		WebSearch: tool.WebSearchConfig{
			APIKey:      cfg.Tools.Tavily.APIKey,
			Endpoint:    cfg.Tools.Tavily.Endpoint,
			ProjectID:   cfg.Tools.Tavily.ProjectID,
			SearchDepth: cfg.Tools.Tavily.SearchDepth,
			MaxResults:  cfg.Tools.Tavily.MaxResults,
			Timeout:     time.Duration(cfg.Tools.Tavily.TimeoutSeconds) * time.Second,
		},
		ReadSkill:  readSkillTool,
		ExtraTools: loadedMCP.Tools,
	})
	if err != nil {
		logger.Error("\U0000274C 工具运行时配置无效", "error", err)
		os.Exit(1)
	}
	logger.Info("\U0001F9F0 Agent 运行时配置已加载",
		"model", cfg.Model.Name,
		"tool_count", len(tools.Names()),
		"tools", strings.Join(tools.Names(), ","),
		"skill_count", skillCount,
		"mcp_tool_count", len(loadedMCP.Tools),
	)

	memoryStore, err := newMemoryStore(cfg, logger)
	if err != nil {
		logger.Error("\U0000274C Memory Runtime 配置无效", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := memoryStore.Close(); err != nil {
			logger.Warn("\U000026A0 Memory Runtime 关闭失败", "error", err)
		}
	}()

	chatAgent := websearch.New(chatModel, tools, logger,
		websearch.WithMaxRounds(cfg.Agent.MaxRounds),
		websearch.WithMemory(memoryStore, cfg.Memory.MaxHistoryRecords),
		websearch.WithContextPolicy(contextx.Policy{
			MaxInputTokens:       cfg.Context.MaxInputTokens,
			ReservedOutputTokens: cfg.Context.ReservedOutputTokens,
			MaxHistoryTokens:     cfg.Context.MaxHistoryTokens,
			CharsPerToken:        cfg.Context.CharsPerToken,
		}),
	)
	deepAgent := planexecute.New(chatModel, tools, logger,
		planexecute.WithMaxRounds(cfg.Agent.MaxRounds),
		planexecute.WithMemory(memoryStore, cfg.Memory.MaxHistoryRecords),
		planexecute.WithContextPolicy(contextx.Policy{
			MaxInputTokens:       cfg.Context.MaxInputTokens,
			ReservedOutputTokens: cfg.Context.ReservedOutputTokens,
			MaxHistoryTokens:     cfg.Context.MaxHistoryTokens,
			CharsPerToken:        cfg.Context.CharsPerToken,
		}),
	)
	skillsAgent := skills.New(chatModel, tools, skillManager, logger,
		skills.WithMaxRounds(cfg.Agent.MaxRounds),
		skills.WithMemory(memoryStore, cfg.Memory.MaxHistoryRecords),
		skills.WithContextPolicy(contextx.Policy{
			MaxInputTokens:       cfg.Context.MaxInputTokens,
			ReservedOutputTokens: cfg.Context.ReservedOutputTokens,
			MaxHistoryTokens:     cfg.Context.MaxHistoryTokens,
			CharsPerToken:        cfg.Context.CharsPerToken,
		}),
	)
	pptxStore := pptx.NewMemoryStore()
	pptxAgent := pptx.New(chatModel, tools, logger,
		pptx.WithStore(pptxStore),
		pptx.WithMemory(memoryStore, cfg.Memory.MaxHistoryRecords),
	)
	agents := map[string]agent.Agent{
		"websearch":    chatAgent,
		"plan-execute": deepAgent,
		"skills":       skillsAgent,
		"pptx":         pptxAgent,
	}
	logger.Info("\U0001F916 Agent 注册完成",
		"agent_count", len(agents),
		"agents", "websearch,plan-execute,skills,pptx",
	)
	taskManager := task.NewManager(logger)
	traceStore, err := trace.NewFileStore(trace.Config{
		Enabled:              cfg.Observability.Trace.Enabled,
		Directory:            cfg.Observability.Trace.Directory,
		MaxEventContentChars: cfg.Observability.Trace.MaxEventContentChars,
	}, logger)
	if err != nil {
		logger.Error("❌ Trace Runtime 配置无效", "error", err)
		os.Exit(1)
	}
	if traceStore.Enabled() {
		logger.Info("🧭 Trace Runtime 已启用",
			"directory", cfg.Observability.Trace.Directory,
			"max_event_content_chars", cfg.Observability.Trace.MaxEventContentChars,
			"detail_endpoint", "/trace/detail?traceId=...",
			"replay_endpoint", "/trace/replay/stream?traceId=...",
		)
	} else {
		logger.Info("🧭 Trace Runtime 未启用，跳过 Agent Run 记录")
	}

	server := &http.Server{
		Addr:              ":" + cfg.Server.Port,
		Handler:           httpapi.NewRouterWithAgentsTraceAndPPTX(logger, agents, taskManager, memoryStore, traceStore, pptxStore),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go serveHTTP(server, logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	<-ctx.Done()
	logger.Info("\U0001F6D1 收到关闭信号")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("\U0000274C 服务关闭失败", "error", err)
		os.Exit(1)
	}

	logger.Info("\U00002705 服务已停止")
}

func toMCPRuntimeConfig(cfg config.Config) mcpruntime.Config {
	servers := make([]mcpruntime.ServerConfig, 0, len(cfg.MCP.Servers))
	for _, server := range cfg.MCP.Servers {
		servers = append(servers, mcpruntime.ServerConfig{
			Name:                 server.Name,
			Enabled:              server.Enabled,
			Transport:            server.Transport,
			URL:                  server.URL,
			Command:              server.Command,
			Args:                 server.Args,
			Headers:              server.Headers,
			ToolPrefix:           server.ToolPrefix,
			Timeout:              time.Duration(server.TimeoutSeconds) * time.Second,
			DisableStandaloneSSE: server.DisableStandaloneSSE,
		})
	}
	return mcpruntime.Config{Servers: servers}
}

func serveHTTP(server *http.Server, logger *slog.Logger) {
	logger.Info("\U0001F680 KimoAgent 服务启动中", "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("\U0000274C 服务启动失败", "error", err)
		os.Exit(1)
	}
}
func newMemoryStore(cfg config.Config, logger *slog.Logger) (memory.Store, error) {
	if !cfg.Memory.Enabled {
		logger.Info("\U0001F4DD Memory Runtime 未启用，跳过会话持久化")
		return memory.NoopStore{}, nil
	}

	store, err := memory.NewMySQLStore(memory.MySQLConfig{
		DSN:             cfg.Memory.DSN,
		MaxOpenConns:    cfg.Memory.MaxOpenConns,
		MaxIdleConns:    cfg.Memory.MaxIdleConns,
		ConnMaxLifetime: time.Duration(cfg.Memory.ConnMaxLifetimeSeconds) * time.Second,
	})
	if err != nil {
		return nil, err
	}

	if cfg.Memory.AutoMigrate {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := store.EnsureSchema(ctx); err != nil {
			_ = store.Close()
			return nil, err
		}
		logger.Info("\U0001F5C4 Memory Runtime 数据表已确认", "table", "ai_session")
	}

	logger.Info("\U0001F4DD Memory Runtime 已启用",
		"max_history_records", cfg.Memory.MaxHistoryRecords,
		"auto_migrate", cfg.Memory.AutoMigrate,
	)
	return store, nil
}

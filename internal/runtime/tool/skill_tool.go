package tool

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/learn-demo/agent-go/internal/runtime/skill"
)

// ReadSkillTool 按名称加载本地 SKILL.md 内容。
// Java dodo-agent 中对应 manual/tool/ReadSkillTool。
type ReadSkillTool struct {
	manager *skill.Manager
	logger  *slog.Logger
}

func NewReadSkillTool(manager *skill.Manager, logger *slog.Logger) *ReadSkillTool {
	if logger == nil {
		logger = slog.Default()
	}
	return &ReadSkillTool{manager: manager, logger: logger}
}

func (t *ReadSkillTool) Definition() Definition {
	return Definition{
		Name: "read_skill",
		Description: `加载指定技能的完整内容。当你需要某个技能的详细指令时使用此工具。
调用后请直接按照返回的技能指令完成任务，禁止再次重复调用同一个技能。`,
		Schema: objectSchema(map[string]any{
			"skill": map[string]any{
				"type":        "string",
				"description": "The name of the skill to load.",
			},
		}, []string{"skill"}),
	}
}

func (t *ReadSkillTool) Execute(ctx context.Context, input Input) (Result, error) {
	if t.manager == nil || !t.manager.Enabled() {
		return Result{}, fmt.Errorf("skills runtime is not configured")
	}
	skillName := StringArg(input.Arguments, "skill")
	if skillName == "" {
		return Result{}, fmt.Errorf("skill is required")
	}

	metadata, content, err := t.manager.Read(ctx, skillName)
	if err != nil {
		t.logger.Error("❌ 技能加载失败", "skill", skillName, "error", err)
		return Result{
			Name: "read_skill",
			Content: MustJSON(map[string]any{
				"skill":   skillName,
				"success": false,
				"error":   err.Error(),
			}),
		}, nil
	}

	cleaned := skill.StripFrontMatterForPrompt(content)
	t.logger.Info("🧩 技能已加载",
		"skill", metadata.Name,
		"content_chars", len(cleaned),
		"skill_file", metadata.SkillFile,
	)
	return Result{
		Name: "read_skill",
		Content: MustJSON(map[string]any{
			"skill":       metadata.Name,
			"description": metadata.Description,
			"content":     cleaned,
			"success":     true,
		}),
		Data: map[string]any{
			"skill":       metadata.Name,
			"description": metadata.Description,
			"skill_file":  metadata.SkillFile,
			"success":     true,
		},
	}, nil
}

var _ Tool = (*ReadSkillTool)(nil)

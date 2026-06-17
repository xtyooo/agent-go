package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentG/internal/runtime/skill"
)

func TestReadSkillToolExecute(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "review")
	if err := os.Mkdir(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\ndescription: 审查\n---\n\n# Review\n\n检查风险。"), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}

	manager := skill.NewManager(skill.Config{Directories: []string{root}}, nil)
	readSkill := NewReadSkillTool(manager, nil)
	result, err := readSkill.Execute(context.Background(), Input{Arguments: map[string]any{"skill": "review"}})
	if err != nil {
		t.Fatalf("execute read_skill: %v", err)
	}
	if result.Name != "read_skill" {
		t.Fatalf("unexpected tool name: %s", result.Name)
	}
	if !strings.Contains(result.Content, `"success":true`) {
		t.Fatalf("expected success result: %s", result.Content)
	}
	if strings.Contains(result.Content, "description: 审查") {
		t.Fatalf("expected front matter to be stripped from content: %s", result.Content)
	}
	if !strings.Contains(result.Content, "# Review") {
		t.Fatalf("expected skill body in result: %s", result.Content)
	}
}

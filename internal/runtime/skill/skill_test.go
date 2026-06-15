package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagerScansSkillDirectories(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "writer")
	if err := os.Mkdir(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	content := `---
description: 写作技能
allowedTools:
  - web_search
---

# Writer

按照清晰结构写作。`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}

	manager := NewManager(Config{Directories: []string{root}}, nil)
	skills, err := manager.List(context.Background())
	if err != nil {
		t.Fatalf("list skills: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("expected one skill, got %d", len(skills))
	}
	if skills[0].Name != "writer" || skills[0].Description != "写作技能" {
		t.Fatalf("unexpected skill metadata: %#v", skills[0])
	}
	if len(skills[0].AllowedTools) != 1 || skills[0].AllowedTools[0] != "web_search" {
		t.Fatalf("unexpected allowed tools: %#v", skills[0].AllowedTools)
	}

	_, raw, err := manager.Read(context.Background(), "writer")
	if err != nil {
		t.Fatalf("read skill: %v", err)
	}
	if !strings.Contains(raw, "# Writer") {
		t.Fatalf("unexpected skill content: %q", raw)
	}
}

func TestFormatPromptMentionsReadSkill(t *testing.T) {
	prompt := FormatPrompt([]Metadata{{Name: "writer", Description: "写作技能"}})
	if !strings.Contains(prompt, "read_skill") {
		t.Fatalf("expected prompt to mention read_skill: %s", prompt)
	}
	if !strings.Contains(prompt, "writer") {
		t.Fatalf("expected prompt to mention skill name: %s", prompt)
	}
}

func TestStripFrontMatterForPrompt(t *testing.T) {
	content := `---
description: demo
---

# Body`
	cleaned := StripFrontMatterForPrompt(content)
	if strings.Contains(cleaned, "description") {
		t.Fatalf("expected front matter removed: %q", cleaned)
	}
	if !strings.Contains(cleaned, "# Body") {
		t.Fatalf("expected body kept: %q", cleaned)
	}
}

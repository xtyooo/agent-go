package skill

import (
	"strings"
)

// FormatPrompt 把技能元数据格式化成系统提示词片段。
// 它刻意只暴露名称和描述，避免把所有 SKILL.md 一次性塞进上下文。
func FormatPrompt(skills []Metadata) string {
	if len(skills) == 0 {
		return ""
	}

	var list strings.Builder
	for _, current := range skills {
		list.WriteString("- **")
		list.WriteString(current.Name)
		list.WriteString("**：")
		list.WriteString(current.Description)
		if len(current.AllowedTools) > 0 {
			list.WriteString("；允许工具：")
			list.WriteString(strings.Join(current.AllowedTools, ", "))
		}
		list.WriteString("\n")
	}

	return `## 可用技能列表

【重要说明】技能不是工具。技能是使用指南和指令集合。
当你需要使用某个技能时，必须先调用 read_skill 工具加载技能内容。
技能内容加载后，按照技能中的指令来完成任务。

**可用技能：**
` + strings.TrimSpace(list.String()) + `

**正确的使用流程：**
1. 用户要求使用某个技能，或问题明显匹配某个技能。
2. 调用 read_skill("技能名称") 获取技能的完整指令。
3. 仔细阅读返回的技能内容。
4. 按照技能中的指令来完成任务。
5. 绝对不要把技能名称当作工具来调用。`
}

// StripFrontMatterForPrompt 去掉 SKILL.md 的 YAML front matter。
// 工具返回给模型时保留正文即可，减少重复元数据对上下文的干扰。
func StripFrontMatterForPrompt(content string) string {
	return stripFrontMatter(content)
}

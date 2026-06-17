package pptx

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

func requirementPrompt(now time.Time) string {
	return fmt.Sprintf(`# 角色
你是专业的 PPT 需求澄清助手，名字叫豆豆，英文名 dodo。

# 当前时间
%s

# 任务
分析用户需求，判断信息是否足够生成 PPT。
至少关注：
1. 主题
2. 页数
3. 风格建议
4. 受众群体

# 输出要求
如果信息不足，先简要说明缺少什么，并包含标记：【暂停生成PPT】。
如果信息完整，确认需求，并包含标记：【开始生成PPT】。
如果用户明确要求直接生成，可以直接开始，不需要反复追问。`, now.Format(time.RFC3339))
}

func outlinePrompt(requirement, templateSchema, templateName, searchInfo string) string {
	return fmt.Sprintf(`## 角色
你是专业的 PPT 内容大纲生成专家。

## PPT需求
%s

## 搜索相关信息
%s

## 选定模板
模板名称：%s

## 模板结构
%s

## 输出要求
只输出详细 PPT 大纲。
每页以 "--- Page X ---" 开头，每页包含：
1. 页面类型
2. 页面标题
3. 主要内容要点`, requirement, searchInfo, templateName, templateSchema)
}

func templateSelectionPrompt(requirement string, templates []Template) string {
	var b strings.Builder
	for _, tmpl := range templates {
		b.WriteString("--------------------------------\n")
		b.WriteString("template_code: " + tmpl.Code + "\n")
		b.WriteString("模板名称: " + tmpl.Name + "\n")
		b.WriteString("适用风格: " + tmpl.StyleTags + "\n")
		b.WriteString(fmt.Sprintf("模板页数: %d\n", tmpl.SlideCount))
		b.WriteString("模板说明: " + tmpl.Description + "\n")
	}
	return fmt.Sprintf(`## 角色
你是 PPT 模板选择专家。

## PPT需求
%s

## 可用模板
%s

## 输出要求
只输出 JSON：
{
  "templateCode": "选择的模板编码",
  "reason": "选择原因"
}`, requirement, b.String())
}

func schemaPrompt(templateSchema, outline string) string {
	return fmt.Sprintf(`## 角色
你是专业的 PPT Schema 生成专家。

## 模板Schema
%s

## PPT大纲
%s

## 输出要求
只输出 JSON，不要 Markdown 代码块，不要解释。
结构如下：
{
  "slides": [
    {
      "pageType": "COVER",
      "pageDesc": "封面页",
      "templatePageIndex": 1,
      "data": {
        "title": {"type": "text", "content": "标题", "fontLimit": 18}
      }
    }
  ]
}

要求：
1. pageType 使用大写。
2. 字段名尽量贴合模板Schema。
3. text 字段 content 不要超过 fontLimit。
4. image/background 字段使用 {"type":"image","content":"图片提示词","url":""}。`, templateSchema, outline)
}

func schemaModifyPrompt(userRequest, currentSchema string) string {
	return fmt.Sprintf(`## 角色
你是专业的 PPT Schema 修改专家。

## 用户修改需求
%s

## 当前PPT Schema
%s

## 输出要求
只输出修改后的完整 JSON，不要 Markdown 代码块，不要解释。
保持用户未要求修改的字段不变。`, userRequest, currentSchema)
}

func summaryPrompt(requirement, fileURL string, pageCount int, modify bool) string {
	if modify {
		return fmt.Sprintf(`你是专业的 PPT 修改助手。请用简洁中文告知用户 PPT 已修改完成，并说明下载链接。

修改需求：
%s

文件链接：
%s`, requirement, fileURL)
	}
	return fmt.Sprintf(`你是专业的 PPT 生成助手。请用简洁中文告知用户 PPT 已生成完成。

PPT需求：
%s

共生成 %d 页 PPT
文件链接：
%s`, requirement, pageCount, fileURL)
}

func parseTemplateSelection(raw string, fallback Template) (string, string) {
	var decoded struct {
		TemplateCode string `json:"templateCode"`
		Reason       string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(extractJSON(raw)), &decoded); err == nil && strings.TrimSpace(decoded.TemplateCode) != "" {
		return strings.TrimSpace(decoded.TemplateCode), strings.TrimSpace(decoded.Reason)
	}
	return fallback.Code, "模型未返回可解析模板编码，使用默认模板"
}

func extractJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "{") && strings.HasSuffix(raw, "}") {
		return raw
	}
	re := regexp.MustCompile(`(?s)\{.*\}`)
	if matched := re.FindString(raw); matched != "" {
		return matched
	}
	return raw
}

func stripThinkTags(raw string) string {
	re := regexp.MustCompile(`(?is)<think>.*?</think>`)
	return strings.TrimSpace(re.ReplaceAllString(raw, ""))
}

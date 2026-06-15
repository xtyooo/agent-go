package planexecute

import (
	"testing"

	"github.com/learn-demo/agent-go/internal/runtime/tool"
)

func TestParseTasksAcceptsMarkdownJSONAndStripsThinkTags(t *testing.T) {
	content := "<think>先判断要不要继续搜索</think>\n" +
		"```json\n" +
		"[\n" +
		`  {"id":"task-2","instruction":"搜索 B","order":2},` + "\n" +
		`  {"id":"task-1","instruction":"搜索 A","order":1}` + "\n" +
		"]\n" +
		"```"

	tasks, err := parseTasks(content)
	if err != nil {
		t.Fatalf("parse tasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected two tasks, got %d", len(tasks))
	}
	if tasks[0].ID != "task-2" || tasks[1].Instruction != "搜索 A" {
		t.Fatalf("unexpected tasks: %#v", tasks)
	}
}

func TestParseCritiqueExtractsObjectFromMixedText(t *testing.T) {
	critique, err := parseCritique(`前置说明
{"passed": false, "feedback": "缺少最近数据"}
后置说明`)
	if err != nil {
		t.Fatalf("parse critique: %v", err)
	}
	if critique.Passed {
		t.Fatal("expected critique to fail")
	}
	if critique.Feedback != "缺少最近数据" {
		t.Fatalf("unexpected feedback: %q", critique.Feedback)
	}
}

func TestPlanDoneAcceptsEmptyIDTasks(t *testing.T) {
	if !planDone([]Task{{ID: "", Instruction: "无需继续工具检索", Order: 0}}) {
		t.Fatal("expected empty id plan to be done")
	}
	if !planDone([]Task{{ID: " "}}) {
		t.Fatal("expected whitespace id plan to be done")
	}
	if planDone([]Task{{ID: ""}, {ID: "task-1"}}) {
		t.Fatal("did not expect mixed plan to be done")
	}
}

func TestParseThinkSegmentsAcrossChunks(t *testing.T) {
	inThink := false
	first := parseThinkSegments("正文A<think>思考", &inThink)
	if !inThink {
		t.Fatal("expected parser to remain inside think tag")
	}
	second := parseThinkSegments("继续</think>正文B", &inThink)
	if inThink {
		t.Fatal("expected parser to leave think tag")
	}

	combined := append(first, second...)
	if len(combined) != 4 {
		t.Fatalf("expected 4 segments, got %#v", combined)
	}
	if combined[0].thinking || combined[0].content != "正文A" {
		t.Fatalf("unexpected first segment: %#v", combined[0])
	}
	if !combined[1].thinking || combined[1].content != "思考" {
		t.Fatalf("unexpected second segment: %#v", combined[1])
	}
	if !combined[2].thinking || combined[2].content != "继续" {
		t.Fatalf("unexpected third segment: %#v", combined[2])
	}
	if combined[3].thinking || combined[3].content != "正文B" {
		t.Fatalf("unexpected fourth segment: %#v", combined[3])
	}
}

func TestSearchResultsFromAny(t *testing.T) {
	source := []any{
		map[string]any{
			"title":   "标题",
			"url":     "https://example.com",
			"content": "摘要",
		},
	}
	results := searchResultsFromAny(source)
	if len(results) != 1 {
		t.Fatalf("expected one result, got %d", len(results))
	}
	if results[0].Title != "标题" || results[0].URL != "https://example.com" {
		t.Fatalf("unexpected result: %#v", results[0])
	}

	typed := []tool.SearchResult{{Title: "typed"}}
	results = searchResultsFromAny(typed)
	if len(results) != 1 || results[0].Title != "typed" {
		t.Fatalf("unexpected typed result: %#v", results)
	}
}

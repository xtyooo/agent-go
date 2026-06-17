package pptx

import (
	"context"
	"strings"
	"testing"

	runtimeagent "github.com/learn-demo/agent-go/internal/runtime/agent"
	"github.com/learn-demo/agent-go/internal/runtime/event"
	"github.com/learn-demo/agent-go/internal/runtime/model"
)

type fakeModel struct {
	generate []string
	stream   []string
}

func (m *fakeModel) Generate(_ context.Context, _ model.Request) (model.Response, error) {
	if len(m.generate) == 0 {
		return model.Response{Content: "{}"}, nil
	}
	out := m.generate[0]
	m.generate = m.generate[1:]
	return model.Response{Content: out}, nil
}

func (m *fakeModel) Stream(_ context.Context, _ model.Request) (<-chan model.Chunk, error) {
	ch := make(chan model.Chunk, len(m.stream)+1)
	for _, content := range m.stream {
		ch <- model.Chunk{Content: content}
	}
	ch <- model.Chunk{Done: true}
	close(ch)
	return ch, nil
}

func TestMemoryStoreLifecycle(t *testing.T) {
	store := NewMemoryStore()
	inst, err := store.Create(context.Background(), "c1", "生成PPT")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if inst.Status != StatusInit || inst.ID == 0 {
		t.Fatalf("unexpected instance: %#v", inst)
	}

	updated, err := store.Update(context.Background(), inst.ID, func(current *Instance) {
		current.Status = StatusSearch
		current.Requirement = "需求"
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Status != StatusSearch || updated.Requirement != "需求" {
		t.Fatalf("unexpected updated instance: %#v", updated)
	}

	latest, ok, err := store.Latest(context.Background(), "c1")
	if err != nil || !ok {
		t.Fatalf("latest ok=%v err=%v", ok, err)
	}
	if latest.ID != inst.ID {
		t.Fatalf("unexpected latest id: %d", latest.ID)
	}
}

func TestIntentHelpers(t *testing.T) {
	if !looksLikeModify("请修改第一页标题") {
		t.Fatal("expected modify intent")
	}
	if !shouldResume(Instance{Status: StatusSchema}, "继续生成") {
		t.Fatal("expected resume intent")
	}
	if shouldResume(Instance{Status: StatusSchema}, "重新生成一份") {
		t.Fatal("expected create-new intent")
	}
}

func TestBuildPPTSearchQueryKeepsQueryShort(t *testing.T) {
	requirement := "【开始生成PPT】\n主题：2026 年中国新能源汽车产业趋势与竞争格局；页数：10；风格：商务科技；受众：管理层\n" + strings.Repeat("补充说明：需要包含市场规模、供应链、智能驾驶、出海竞争、风险建议。", 20)

	query := buildPPTSearchQuery(requirement, "生成一份新能源汽车 PPT")

	if len([]rune(query)) > maxPPTSearchChars {
		t.Fatalf("query length = %d, want <= %d", len([]rune(query)), maxPPTSearchChars)
	}
	if !strings.Contains(query, "新能源汽车") {
		t.Fatalf("expected topic in query, got %q", query)
	}
	if strings.Contains(query, "【开始生成PPT】") {
		t.Fatalf("expected marker to be removed, got %q", query)
	}
}

func TestAgentRunsCreateFlow(t *testing.T) {
	store := NewMemoryStore()
	model := &fakeModel{
		generate: []string{
			"【开始生成PPT】主题：AI Agent；页数：3；风格：商务；受众：研发团队",
			`{"templateCode":"business-simple","reason":"适合商务汇报"}`,
			"--- Page 1 ---\n类型：COVER\n标题：AI Agent\n--- Page 2 ---\n类型：CONTENT\n标题：架构\n--- Page 3 ---\n类型：END\n标题：总结",
			`{"slides":[{"pageType":"COVER","pageDesc":"封面页","templatePageIndex":1,"data":{"title":{"type":"text","content":"AI Agent","fontLimit":18}}}]}`,
		},
		stream: []string{"✅ PPT已成功生成完成！"},
	}
	agent := New(model, nil, nil, WithStore(store))

	events, err := agent.Run(context.Background(), agentInput("ppt-1", "生成一份 AI Agent 分享 PPT"))
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	var text strings.Builder
	complete := false
	for evt := range events {
		if evt.Type == event.TypeText {
			text.WriteString(evt.Content)
		}
		if evt.Type == event.TypeComplete {
			complete = true
		}
	}
	if !complete {
		t.Fatal("expected complete event")
	}
	if !strings.Contains(text.String(), "PPT已成功生成完成") {
		t.Fatalf("unexpected text: %s", text.String())
	}
	latest, ok, err := store.Latest(context.Background(), "ppt-1")
	if err != nil || !ok {
		t.Fatalf("latest ok=%v err=%v", ok, err)
	}
	if latest.Status != StatusSuccess || latest.FileURL == "" || latest.PPTSchema == "" {
		t.Fatalf("unexpected latest instance: %#v", latest)
	}
}

func agentInput(conversationID string, query string) runtimeagent.Input {
	return runtimeagent.Input{ConversationID: conversationID, Query: query}
}

var _ model.Model = (*fakeModel)(nil)

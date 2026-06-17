package contextx

import (
	"testing"

	"agentG/internal/runtime/model"
)

func TestTrimHistoryKeepsRecentMessages(t *testing.T) {
	policy := Policy{MaxInputTokens: 100, MaxHistoryTokens: 20, CharsPerToken: 4}.Normalize()
	messages := []model.Message{
		{Role: model.RoleUser, Content: "old user message with many characters"},
		{Role: model.RoleAssistant, Content: "old assistant message with many characters"},
		{Role: model.RoleUser, Content: "new"},
		{Role: model.RoleAssistant, Content: "answer"},
	}

	trimmed := TrimHistory(messages, 8, policy)
	if len(trimmed) == 0 {
		t.Fatal("expected recent history messages to be kept")
	}
	if trimmed[len(trimmed)-1].Content != "answer" {
		t.Fatalf("expected newest message to be kept, got %q", trimmed[len(trimmed)-1].Content)
	}
}

func TestBuildAddsHistoryLabelOnlyWhenHistoryKept(t *testing.T) {
	builder := NewBuilder(Policy{MaxInputTokens: 100, MaxHistoryTokens: 40, CharsPerToken: 4})
	result := builder.Build(
		Section{Name: SectionSystem, Messages: []model.Message{{Role: model.RoleSystem, Content: "system"}}},
		Section{Name: SectionHistory, Messages: []model.Message{{Role: model.RoleUser, Content: "history"}}},
		Section{Name: SectionCurrent, Messages: []model.Message{{Role: model.RoleUser, Content: "current"}}},
	)

	if len(result.Messages) != 4 {
		t.Fatalf("expected system + label + history + current, got %d", len(result.Messages))
	}
	if result.Messages[1].Content != "对话历史：" {
		t.Fatalf("expected history label, got %#v", result.Messages[1])
	}
	if result.Summary.HistoryMessageKept != 1 {
		t.Fatalf("expected one history message kept, got %d", result.Summary.HistoryMessageKept)
	}
}

func TestBuildDropsHistoryWhenNoBudget(t *testing.T) {
	builder := NewBuilder(Policy{MaxInputTokens: 10, MaxHistoryTokens: 10, CharsPerToken: 4})
	result := builder.Build(
		Section{Name: SectionSystem, Messages: []model.Message{{Role: model.RoleSystem, Content: "system message that uses budget"}}},
		Section{Name: SectionHistory, Messages: []model.Message{{Role: model.RoleUser, Content: "history"}}},
		Section{Name: SectionCurrent, Messages: []model.Message{{Role: model.RoleUser, Content: "current message that uses budget"}}},
	)

	if result.Summary.HistoryMessageKept != 0 {
		t.Fatalf("expected history to be dropped, got %d kept", result.Summary.HistoryMessageKept)
	}
	for _, msg := range result.Messages {
		if msg.Content == "对话历史：" {
			t.Fatal("did not expect history label when no history is kept")
		}
	}
}

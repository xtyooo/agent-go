package usage

import (
	"testing"
	"time"
)

func TestBuildOverviewAggregatesUsage(t *testing.T) {
	from := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)
	records := []Record{
		{
			Model:            "qwen",
			AgentType:        "websearch",
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
			CachedTokens:     20,
			ElapsedMs:        1200,
			Success:          true,
			CreatedAt:        from.Add(10 * time.Minute),
		},
		{
			Model:            "qwen",
			AgentType:        "pptx",
			PromptTokens:     60,
			CompletionTokens: 40,
			TotalTokens:      100,
			ElapsedMs:        800,
			Success:          false,
			CreatedAt:        from.Add(70 * time.Minute),
		},
	}

	overview := BuildOverview(records, Query{
		From:     from,
		To:       from.Add(2 * time.Hour),
		Interval: time.Hour,
		Limit:    10,
	})

	if overview.Summary.CallCount != 2 {
		t.Fatalf("call count = %d, want 2", overview.Summary.CallCount)
	}
	if overview.Summary.TotalTokens != 250 {
		t.Fatalf("total tokens = %d, want 250", overview.Summary.TotalTokens)
	}
	if overview.Summary.ErrorCount != 1 {
		t.Fatalf("error count = %d, want 1", overview.Summary.ErrorCount)
	}
	if overview.Summary.CacheHitRate != 0.125 {
		t.Fatalf("cache hit rate = %v, want 0.125", overview.Summary.CacheHitRate)
	}
	if len(overview.Series) != 3 {
		t.Fatalf("series length = %d, want 3", len(overview.Series))
	}
	if overview.Series[0].TotalTokens != 150 || overview.Series[1].TotalTokens != 100 {
		t.Fatalf("unexpected series totals: %+v", overview.Series)
	}
	if len(overview.Models) != 1 || overview.Models[0].Model != "qwen" || overview.Models[0].TotalTokens != 250 {
		t.Fatalf("unexpected model aggregates: %+v", overview.Models)
	}
}

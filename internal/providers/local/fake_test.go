package local

import (
	"context"
	"testing"

	"github.com/BrandonYaniz/yllmd/internal/protocol"
	"github.com/BrandonYaniz/yllmd/internal/providers"
)

func TestFakeProviderGenerateCompact(t *testing.T) {
	p := NewFakeProvider("fast")
	events, err := p.Generate(context.Background(), providers.GenerateRequest{
		ID:     "req-1",
		Model:  "fast",
		Stream: false,
		Input:  protocol.Input{Kind: "prompt", Prompt: "Reply with ready."},
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	var seenCompleted bool
	for event := range events {
		if event.Type == "completed" {
			seenCompleted = true
			if event.Text == "" {
				t.Fatal("expected compact completed text")
			}
			if event.Usage == nil {
				t.Fatal("expected usage")
			}
		}
	}
	if !seenCompleted {
		t.Fatal("expected completed event")
	}
}

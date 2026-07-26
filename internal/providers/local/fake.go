package local

import (
	"context"
	"strings"
	"time"

	"github.com/BrandonYaniz/yllmd/internal/protocol"
	"github.com/BrandonYaniz/yllmd/internal/providers"
)

type FakeProvider struct {
	model string
}

func NewFakeProvider(model string) *FakeProvider {
	return &FakeProvider{model: model}
}

func (p *FakeProvider) ID() string {
	return "local"
}

func (p *FakeProvider) Close(ctx context.Context) error {
	return nil
}

func (p *FakeProvider) Generate(ctx context.Context, request providers.GenerateRequest) (<-chan protocol.Event, error) {
	events := make(chan protocol.Event)
	go func() {
		defer close(events)
		send := func(event protocol.Event) bool {
			select {
			case <-ctx.Done():
				return false
			case events <- event:
				return true
			}
		}

		model := request.Model
		if model == "" {
			model = p.model
		}
		if !send(protocol.Event{Type: "started", ID: request.ID, Provider: "local", Target: request.Target, Model: model, Fallback: request.Fallback, FallbackFrom: request.FallbackFrom}) {
			return
		}
		text := fakeText(request.Input)
		if request.Stream {
			for _, part := range splitDeltas(text) {
				time.Sleep(15 * time.Millisecond)
				if !send(protocol.Event{Type: "delta", ID: request.ID, Text: part}) {
					return
				}
			}
		}
		send(protocol.Event{
			Type:         "completed",
			ID:           request.ID,
			FinishReason: "stop",
			Usage:        &protocol.Usage{InputTokens: 8, OutputTokens: len(strings.Fields(text)), TotalTokens: 8 + len(strings.Fields(text))},
			Text:         compactText(text, request.Stream),
		})
	}()
	return events, nil
}

func fakeText(input protocol.Input) string {
	switch input.Kind {
	case "prompt":
		if strings.TrimSpace(input.Prompt) != "" {
			return "fake local response: " + input.Prompt
		}
	case "messages":
		if len(input.Messages) > 0 {
			last := input.Messages[len(input.Messages)-1]
			return "fake local response: " + last.Content
		}
	}
	return "fake local response"
}

func splitDeltas(text string) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{text}
	}
	parts := make([]string, 0, len(words))
	for i, word := range words {
		if i == 0 {
			parts = append(parts, word)
			continue
		}
		parts = append(parts, " "+word)
	}
	return parts
}

func compactText(text string, stream bool) string {
	if stream {
		return ""
	}
	return text
}

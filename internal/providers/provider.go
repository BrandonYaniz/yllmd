package providers

import (
	"context"

	"github.com/BrandonYaniz/yllmd/internal/protocol"
)

type GenerateRequest struct {
	ID             string
	Provider       string
	Model          string
	Target         *protocol.ModelTarget
	Fallback       bool
	FallbackFrom   string
	FallbackModels []string
	Stream         bool
	Input          protocol.Input
	Settings       protocol.GenerationSettings
}

type Provider interface {
	ID() string
	Generate(ctx context.Context, request GenerateRequest) (<-chan protocol.Event, error)
}

type Closeable interface {
	Close(ctx context.Context) error
}

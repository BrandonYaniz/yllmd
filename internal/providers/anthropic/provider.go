package anthropic

import "fmt"

type Provider struct{}

func New() *Provider {
	return &Provider{}
}

func (p *Provider) ID() string {
	return "anthropic"
}

func ErrNotImplemented() error {
	return fmt.Errorf("anthropic provider is skeletoned but not implemented in v1")
}

package openai

import "fmt"

type Provider struct{}

func New() *Provider {
	return &Provider{}
}

func (p *Provider) ID() string {
	return "openai"
}

func ErrNotImplemented() error {
	return fmt.Errorf("openai provider is skeletoned but not implemented")
}

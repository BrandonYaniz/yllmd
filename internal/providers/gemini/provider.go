package gemini

import "fmt"

type Provider struct{}

func New() *Provider {
	return &Provider{}
}

func (p *Provider) ID() string {
	return "gemini"
}

func ErrNotImplemented() error {
	return fmt.Errorf("gemini provider is skeletoned but not implemented")
}

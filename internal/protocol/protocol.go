package protocol

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type MessageType string

const (
	MessageGenerate  MessageType = "generate"
	MessageCancel    MessageType = "cancel"
	MessageHealth    MessageType = "health"
	MessageStatus    MessageType = "status"
	MessageModels    MessageType = "models"
	MessageProviders MessageType = "providers"
)

type Request struct {
	Type         MessageType        `json:"type"`
	ID           string             `json:"id"`
	Action       string             `json:"action,omitempty"`
	Provider     string             `json:"provider,omitempty"`
	Model        string             `json:"model,omitempty"`
	ModelType    string             `json:"model_type,omitempty"`
	Level        string             `json:"level,omitempty"`
	Version      string             `json:"version,omitempty"`
	File         string             `json:"file,omitempty"`
	SHA256       string             `json:"sha256,omitempty"`
	Activate     *bool              `json:"activate,omitempty"`
	Stream       *bool              `json:"stream,omitempty"`
	OutputFormat string             `json:"output_format,omitempty"`
	Input        *Input             `json:"input,omitempty"`
	Settings     GenerationSettings `json:"settings,omitempty"`
	Queue        QueueOptions       `json:"queue,omitempty"`
}

type Input struct {
	Kind     string    `json:"kind"`
	Prompt   string    `json:"prompt,omitempty"`
	Messages []Message `json:"messages,omitempty"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type GenerationSettings struct {
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
	Stop        []string `json:"stop,omitempty"`
	Stream      *bool    `json:"stream,omitempty"`
	Output      *Output  `json:"output,omitempty"`
}

type Output struct {
	Format   string `json:"format"`
	Delivery string `json:"delivery"`
}

type QueueOptions struct {
	Policy    string `json:"policy,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type Event struct {
	Type          string            `json:"type"`
	ID            string            `json:"id,omitempty"`
	QueuePosition int               `json:"queue_position,omitempty"`
	Provider      string            `json:"provider,omitempty"`
	Model         string            `json:"model,omitempty"`
	Version       string            `json:"version,omitempty"`
	Versions      []ModelVersion    `json:"versions,omitempty"`
	Path          string            `json:"path,omitempty"`
	Models        []ModelDescriptor `json:"models,omitempty"`
	Text          string            `json:"text,omitempty"`
	FinishReason  string            `json:"finish_reason,omitempty"`
	Usage         *Usage            `json:"usage,omitempty"`
	Code          string            `json:"code,omitempty"`
	Message       string            `json:"message,omitempty"`
	Status        string            `json:"status,omitempty"`
	Daemon        *DaemonStatus     `json:"daemon,omitempty"`
	LoadedModel   string            `json:"loaded_model,omitempty"`
	QueueDepth    int               `json:"queue_depth,omitempty"`
}

func (r Request) ValidateModelInstall() error {
	if r.Type != MessageModels {
		return fmt.Errorf("request type must be models")
	}
	if r.Action != "install" {
		return fmt.Errorf("models request action must be install")
	}
	if strings.TrimSpace(r.Model) == "" {
		return fmt.Errorf("model is required")
	}
	if strings.TrimSpace(r.Version) == "" {
		return fmt.Errorf("version is required")
	}
	if strings.TrimSpace(r.File) == "" {
		return fmt.Errorf("file is required")
	}
	if strings.TrimSpace(r.SHA256) == "" {
		return fmt.Errorf("sha256 is required")
	}
	return nil
}

func (r Request) ValidateModelRollback() error {
	if r.Type != MessageModels {
		return fmt.Errorf("request type must be models")
	}
	if r.Action != "rollback" {
		return fmt.Errorf("models request action must be rollback")
	}
	if strings.TrimSpace(r.Model) == "" {
		return fmt.Errorf("model is required")
	}
	return nil
}

func (r Request) ValidateModelActivate() error {
	if r.Type != MessageModels {
		return fmt.Errorf("request type must be models")
	}
	if r.Action != "activate" {
		return fmt.Errorf("models request action must be activate")
	}
	if strings.TrimSpace(r.Model) == "" {
		return fmt.Errorf("model is required")
	}
	if strings.TrimSpace(r.Version) == "" {
		return fmt.Errorf("version is required")
	}
	return nil
}

func (r Request) ValidateModelVersions() error {
	if r.Type != MessageModels {
		return fmt.Errorf("request type must be models")
	}
	if r.Action != "versions" {
		return fmt.Errorf("models request action must be versions")
	}
	if strings.TrimSpace(r.Model) == "" {
		return fmt.Errorf("model is required")
	}
	return nil
}

type Usage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	TotalTokens  int `json:"total_tokens,omitempty"`
}

type DaemonStatus struct {
	Status        string `json:"status"`
	Provider      string `json:"provider"`
	LoadedModel   string `json:"loaded_model"`
	QueueDepth    int    `json:"queue_depth"`
	ModelCount    int    `json:"model_count"`
	RemoteEnabled bool   `json:"remote_enabled"`
}

type ModelDescriptor struct {
	ID               ModelID           `json:"id"`
	Name             string            `json:"name"`
	DisplayName      string            `json:"display_name"`
	ModelType        string            `json:"model_type"`
	Level            string            `json:"level"`
	Tier             string            `json:"tier"`
	Resident         bool              `json:"resident"`
	Capabilities     ModelCapabilities `json:"capabilities"`
	ProviderMetadata map[string]string `json:"provider_metadata,omitempty"`
}

type ModelVersion struct {
	Version      string `json:"version"`
	Active       bool   `json:"active,omitempty"`
	Path         string `json:"path,omitempty"`
	ManifestPath string `json:"manifest_path,omitempty"`
	ChecksumPath string `json:"checksum_path,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
	InstalledAt  string `json:"installed_at,omitempty"`
}

type ModelID struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
}

type ModelCapabilities struct {
	SupportsStreaming        bool `json:"supports_streaming"`
	SupportsLocalPreparation bool `json:"supports_local_preparation"`
	ContextWindow            int  `json:"context_window,omitempty"`
	MaxOutputTokens          int  `json:"max_output_tokens,omitempty"`
}

func DecodeRequest(line []byte) (Request, error) {
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		return Request{}, err
	}
	if req.Type == "" {
		return Request{}, fmt.Errorf("request type is required")
	}
	if req.ID == "" {
		return Request{}, fmt.Errorf("request id is required")
	}
	return req, nil
}

func (r Request) ValidateGenerate() error {
	if r.Type != MessageGenerate {
		return fmt.Errorf("request type must be generate")
	}
	if r.Input == nil {
		return fmt.Errorf("generate request requires input")
	}
	switch r.Input.Kind {
	case "prompt":
		if strings.TrimSpace(r.Input.Prompt) == "" {
			return fmt.Errorf("prompt input requires prompt")
		}
	case "messages":
		if len(r.Input.Messages) == 0 {
			return fmt.Errorf("messages input requires at least one message")
		}
		for i, message := range r.Input.Messages {
			if !validRole(message.Role) {
				return fmt.Errorf("messages[%d].role %q is not supported", i, message.Role)
			}
			if strings.TrimSpace(message.Content) == "" {
				return fmt.Errorf("messages[%d].content is required", i)
			}
		}
	default:
		return fmt.Errorf("unsupported input kind %q", r.Input.Kind)
	}
	if r.Settings.Temperature != nil && *r.Settings.Temperature < 0 {
		return fmt.Errorf("temperature must be greater than or equal to 0")
	}
	if r.Settings.TopP != nil && (*r.Settings.TopP <= 0 || *r.Settings.TopP > 1) {
		return fmt.Errorf("top_p must be in the range (0, 1]")
	}
	if r.Settings.MaxTokens != nil && *r.Settings.MaxTokens <= 0 {
		return fmt.Errorf("max_tokens must be positive")
	}
	for i, stop := range r.Settings.Stop {
		if stop == "" {
			return fmt.Errorf("stop[%d] must not be empty", i)
		}
	}
	if r.Settings.Output != nil {
		switch r.Settings.Output.Format {
		case "json", "text", "raw":
		default:
			return fmt.Errorf("settings.output.format %q is not supported", r.Settings.Output.Format)
		}
		switch r.Settings.Output.Delivery {
		case "stream", "complete":
		default:
			return fmt.Errorf("settings.output.delivery %q is not supported", r.Settings.Output.Delivery)
		}
	}
	if r.Queue.Policy != "" && r.Queue.Policy != "wait" {
		return fmt.Errorf("queue.policy %q is not supported", r.Queue.Policy)
	}
	if r.Queue.TimeoutMS < 0 {
		return fmt.Errorf("queue.timeout_ms must not be negative")
	}
	switch r.OutputFormat {
	case "", "json", "text", "raw":
	default:
		return fmt.Errorf("output_format %q is not supported", r.OutputFormat)
	}
	switch r.ModelType {
	case "", "llm", "code":
	default:
		return fmt.Errorf("model_type %q is not supported", r.ModelType)
	}
	return nil
}

func validRole(role string) bool {
	switch role {
	case "system", "user", "assistant":
		return true
	default:
		return false
	}
}

func WriteEvent(w io.Writer, event Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

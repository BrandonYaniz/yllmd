package protocol

import (
	"encoding/json"
	"fmt"
	"io"
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
	Type     MessageType        `json:"type"`
	ID       string             `json:"id"`
	Provider string             `json:"provider,omitempty"`
	Model    string             `json:"model,omitempty"`
	Stream   *bool              `json:"stream,omitempty"`
	Input    *Input             `json:"input,omitempty"`
	Settings GenerationSettings `json:"settings,omitempty"`
	Queue    QueueOptions       `json:"queue,omitempty"`
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
}

type QueueOptions struct {
	Policy    string `json:"policy,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type Event struct {
	Type          string `json:"type"`
	ID            string `json:"id,omitempty"`
	QueuePosition int    `json:"queue_position,omitempty"`
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	Text          string `json:"text,omitempty"`
	FinishReason  string `json:"finish_reason,omitempty"`
	Usage         *Usage `json:"usage,omitempty"`
	Code          string `json:"code,omitempty"`
	Message       string `json:"message,omitempty"`
	Status        string `json:"status,omitempty"`
	LoadedModel   string `json:"loaded_model,omitempty"`
	QueueDepth    int    `json:"queue_depth,omitempty"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	TotalTokens  int `json:"total_tokens,omitempty"`
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

func WriteEvent(w io.Writer, event Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

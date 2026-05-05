package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type Provider string

const (
	ProviderClaude  Provider = "claude"
	ProviderOllama  Provider = "ollama"
	ProviderOpenAI  Provider = "openai"
	ProviderBedrock Provider = "bedrock"
)

type Config struct {
	Provider Provider
	APIKey   string
	BaseURL  string
	Model    string
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Response struct {
	Content string
	Tokens  int
}

// Client is the unified LLM interface
type Client interface {
	Complete(ctx context.Context, system string, messages []Message) (*Response, error)
	Provider() Provider
}

func NewClient(cfg Config) (Client, error) {
	switch cfg.Provider {
	case ProviderClaude:
		return newClaudeClient(cfg), nil
	case ProviderOllama:
		return newOllamaClient(cfg), nil
	case ProviderOpenAI:
		return newOpenAIClient(cfg), nil
	case ProviderBedrock:
		return newBedrockClient(cfg), nil
	default:
		return nil, fmt.Errorf("unknown provider: %s", cfg.Provider)
	}
}

// ── Claude ────────────────────────────────────────────────────────────────────
type claudeClient struct{ cfg Config }

func newClaudeClient(cfg Config) *claudeClient {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.anthropic.com"
	}
	if cfg.Model == "" {
		cfg.Model = "claude-sonnet-4-20250514"
	}
	return &claudeClient{cfg}
}

func (c *claudeClient) Provider() Provider { return ProviderClaude }

func (c *claudeClient) Complete(ctx context.Context, system string, messages []Message) (*Response, error) {
	body := map[string]any{
		"model":      c.cfg.Model,
		"max_tokens": 1024,
		"system":     system,
		"messages":   messages,
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, "POST", c.cfg.BaseURL+"/v1/messages", bytes.NewReader(b))
	req.Header.Set("x-api-key", c.cfg.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var out struct {
		Content []struct{ Text string `json:"text"` } `json:"content"`
		Usage   struct{ OutputTokens int `json:"output_tokens"` } `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Content) == 0 {
		return nil, fmt.Errorf("empty response from Claude")
	}
	return &Response{Content: out.Content[0].Text, Tokens: out.Usage.OutputTokens}, nil
}

// ── Ollama (local) ────────────────────────────────────────────────────────────
type ollamaClient struct{ cfg Config }

func newOllamaClient(cfg Config) *ollamaClient {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://localhost:11434"
	}
	if cfg.Model == "" {
		cfg.Model = "llama3"
	}
	return &ollamaClient{cfg}
}

func (c *ollamaClient) Provider() Provider { return ProviderOllama }

func (c *ollamaClient) Complete(ctx context.Context, system string, messages []Message) (*Response, error) {
	// Build prompt combining system + messages
	prompt := system + "\n\n"
	for _, m := range messages {
		prompt += fmt.Sprintf("[%s]: %s\n", m.Role, m.Content)
	}

	body := map[string]any{
		"model":  c.cfg.Model,
		"prompt": prompt,
		"stream": false,
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, "POST", c.cfg.BaseURL+"/api/generate", bytes.NewReader(b))
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var out struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &Response{Content: out.Response}, nil
}

// ── OpenAI ────────────────────────────────────────────────────────────────────
type openAIClient struct{ cfg Config }

func newOpenAIClient(cfg Config) *openAIClient {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com"
	}
	if cfg.Model == "" {
		cfg.Model = "gpt-4o"
	}
	return &openAIClient{cfg}
}

func (c *openAIClient) Provider() Provider { return ProviderOpenAI }

func (c *openAIClient) Complete(ctx context.Context, system string, messages []Message) (*Response, error) {
	msgs := append([]Message{{Role: "system", Content: system}}, messages...)
	body := map[string]any{"model": c.cfg.Model, "messages": msgs}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, "POST", c.cfg.BaseURL+"/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var out struct {
		Choices []struct {
			Message struct{ Content string `json:"content"` } `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("empty response from OpenAI")
	}
	return &Response{Content: out.Choices[0].Message.Content}, nil
}

// ── Bedrock (stub — use aws-sdk-go-v2 in full impl) ──────────────────────────
type bedrockClient struct{ cfg Config }

func newBedrockClient(cfg Config) *bedrockClient {
	if cfg.Model == "" {
		cfg.Model = "anthropic.claude-3-5-sonnet-20241022-v2:0"
	}
	return &bedrockClient{cfg}
}

func (c *bedrockClient) Provider() Provider { return ProviderBedrock }

func (c *bedrockClient) Complete(ctx context.Context, system string, messages []Message) (*Response, error) {
	// Full implementation uses: github.com/aws/aws-sdk-go-v2/service/bedrockruntime
	// BedrockRuntimeClient.InvokeModel() with anthropic.claude payload
	return nil, fmt.Errorf("bedrock: use aws-sdk-go-v2 — see pkg/llm/bedrock_full.go")
}

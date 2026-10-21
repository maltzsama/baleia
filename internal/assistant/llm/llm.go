// Package llm is the assistant's provider boundary (#62). The turn engine only
// sees Provider; the adapters translate the vendor wire formats.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Message is one provider-visible turn. Tool results carry ToolCallID.
type Message struct {
	Role       string
	Content    string
	ToolCallID string
	ToolCalls  []ToolCall
}

// ToolSpec is a tool definition fetched from the MCP registry.
type ToolSpec struct {
	Name        string
	Description string
	Schema      map[string]any
}

// ToolCall is one tool invocation proposed by the model.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// Request is a single provider call.
type Request struct {
	System    string
	Messages  []Message
	Tools     []ToolSpec
	MaxTokens int
}

// Response is the model's answer for one call.
type Response struct {
	Text         string
	ToolCalls    []ToolCall
	StopReason   string
	InputTokens  int
	OutputTokens int
}

// Provider calls the configured model.
type Provider interface {
	Stream(ctx context.Context, req Request, onDelta func(string)) (Response, error)
}

// Fake is the deterministic provider used by the turn tests.
type Fake struct {
	Responses []Response
	Requests  []Request
	Err       error
}

// Stream records the request and returns the next scripted response.
func (f *Fake) Stream(_ context.Context, req Request, onDelta func(string)) (Response, error) {
	f.Requests = append(f.Requests, req)
	if f.Err != nil {
		return Response{}, f.Err
	}
	if len(f.Responses) == 0 {
		return Response{Text: "ok"}, nil
	}
	resp := f.Responses[0]
	f.Responses = f.Responses[1:]
	if onDelta != nil && resp.Text != "" {
		onDelta(resp.Text)
	}
	return resp, nil
}

// HTTPProvider speaks the Anthropic Messages API and the OpenAI-compatible
// Chat Completions API. Streaming is not required for the turn contract: the
// adapters deliver the final text once and the SSE layer forwards it.
type HTTPProvider struct {
	Provider string // anthropic | openai_compatible
	BaseURL  string
	APIKey   string
	Model    string
	Client   *http.Client
}

// ClientTimeout bounds one provider call; callers build the client with it.
const ClientTimeout = 120 * time.Second

// NewHTTPProvider builds a provider from the stored settings. client must dial
// through connections.Dialer (#378): the base URL is admin-configurable and
// may point at an internal address.
func NewHTTPProvider(client *http.Client, provider, baseURL, apiKey, model string) *HTTPProvider {
	if baseURL == "" {
		if provider == "anthropic" {
			baseURL = "https://api.anthropic.com"
		} else {
			baseURL = "https://api.openai.com/v1"
		}
	}
	return &HTTPProvider{
		Provider: provider, BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, Model: model,
		Client: client,
	}
}

// Stream implements Provider.
func (p *HTTPProvider) Stream(ctx context.Context, req Request, onDelta func(string)) (Response, error) {
	var (
		resp Response
		err  error
	)
	if p.Provider == "anthropic" {
		resp, err = p.anthropic(ctx, req)
	} else {
		resp, err = p.openai(ctx, req)
	}
	if err != nil {
		return Response{}, err
	}
	if onDelta != nil && resp.Text != "" {
		onDelta(resp.Text)
	}
	return resp, nil
}

func (p *HTTPProvider) post(ctx context.Context, path string, body any, headers map[string]string) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		httpReq.Header.Set(key, value)
	}
	res, err := p.Client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("provider %s: %s", res.Status, strings.TrimSpace(string(payload)))
	}
	return payload, nil
}

func (p *HTTPProvider) anthropic(ctx context.Context, req Request) (Response, error) {
	type block struct {
		Type  string          `json:"type"`
		Text  string          `json:"text,omitempty"`
		ID    string          `json:"id,omitempty"`
		Name  string          `json:"name,omitempty"`
		Input json.RawMessage `json:"input,omitempty"`
	}
	messages := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch {
		case m.Role == "tool":
			messages = append(messages, map[string]any{
				"role": "user",
				"content": []map[string]any{{
					"type": "tool_result", "tool_use_id": m.ToolCallID, "content": m.Content,
				}},
			})
		case len(m.ToolCalls) > 0:
			content := make([]map[string]any, 0, len(m.ToolCalls)+1)
			if m.Content != "" {
				content = append(content, map[string]any{"type": "text", "text": m.Content})
			}
			for _, call := range m.ToolCalls {
				var input any
				_ = json.Unmarshal(call.Arguments, &input)
				content = append(content, map[string]any{
					"type": "tool_use", "id": call.ID, "name": call.Name, "input": input,
				})
			}
			messages = append(messages, map[string]any{"role": "assistant", "content": content})
		default:
			messages = append(messages, map[string]any{"role": m.Role, "content": m.Content})
		}
	}
	tools := make([]map[string]any, 0, len(req.Tools))
	for _, tool := range req.Tools {
		tools = append(tools, map[string]any{
			"name": tool.Name, "description": tool.Description, "input_schema": tool.Schema,
		})
	}
	body := map[string]any{
		"model": p.Model, "max_tokens": req.MaxTokens, "system": req.System, "messages": messages,
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	raw, err := p.post(ctx, "/v1/messages", body, map[string]string{
		"x-api-key": p.APIKey, "anthropic-version": "2023-06-01",
	})
	if err != nil {
		return Response{}, err
	}
	var out struct {
		Content []block `json:"content"`
		Stop    string  `json:"stop_reason"`
		Usage   struct {
			Input  int `json:"input_tokens"`
			Output int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Response{}, err
	}
	resp := Response{StopReason: out.Stop, InputTokens: out.Usage.Input, OutputTokens: out.Usage.Output}
	for _, item := range out.Content {
		switch item.Type {
		case "text":
			resp.Text += item.Text
		case "tool_use":
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: item.ID, Name: item.Name, Arguments: item.Input})
		}
	}
	return resp, nil
}

func (p *HTTPProvider) openai(ctx context.Context, req Request) (Response, error) {
	messages := make([]map[string]any, 0, len(req.Messages)+1)
	if req.System != "" {
		messages = append(messages, map[string]any{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		if m.Role == "tool" {
			messages = append(messages, map[string]any{
				"role": "tool", "tool_call_id": m.ToolCallID, "content": m.Content,
			})
			continue
		}
		entry := map[string]any{"role": m.Role, "content": m.Content}
		if len(m.ToolCalls) > 0 {
			calls := make([]map[string]any, 0, len(m.ToolCalls))
			for _, call := range m.ToolCalls {
				calls = append(calls, map[string]any{
					"id": call.ID, "type": "function",
					"function": map[string]any{"name": call.Name, "arguments": string(call.Arguments)},
				})
			}
			entry["tool_calls"] = calls
		}
		messages = append(messages, entry)
	}
	tools := make([]map[string]any, 0, len(req.Tools))
	for _, tool := range req.Tools {
		tools = append(tools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": tool.Name, "description": tool.Description, "parameters": tool.Schema,
			},
		})
	}
	body := map[string]any{"model": p.Model, "max_tokens": req.MaxTokens, "messages": messages}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	raw, err := p.post(ctx, "/chat/completions", body, map[string]string{
		"Authorization": "Bearer " + p.APIKey,
	})
	if err != nil {
		return Response{}, err
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			Prompt     int `json:"prompt_tokens"`
			Completion int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Response{}, err
	}
	resp := Response{InputTokens: out.Usage.Prompt, OutputTokens: out.Usage.Completion}
	if len(out.Choices) > 0 {
		choice := out.Choices[0]
		resp.Text = choice.Message.Content
		resp.StopReason = choice.FinishReason
		for _, call := range choice.Message.ToolCalls {
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{
				ID: call.ID, Name: call.Function.Name, Arguments: json.RawMessage(call.Function.Arguments),
			})
		}
	}
	return resp, nil
}

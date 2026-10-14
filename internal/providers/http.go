package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func normalizedHTTPError(provider string, status int, body io.Reader) error {
	messageBytes, _ := io.ReadAll(io.LimitReader(body, 4096))
	message := strings.TrimSpace(string(messageBytes))
	if message == "" {
		message = http.StatusText(status)
	}
	class, retryable := ErrorInternal, false
	switch {
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		class = ErrorInvalid
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		class = ErrorAuth
	case status == http.StatusTooManyRequests:
		class, retryable = ErrorRateLimited, true
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout:
		class, retryable = ErrorTimeout, true
	case status >= 500:
		class, retryable = ErrorTransient, true
	}
	return &Error{Class: class, Provider: provider, StatusCode: status, Retryable: retryable, Message: message}
}

type OpenAICompatibleConfig struct {
	ProviderName, BaseURL, APIKey string
	Client                        *http.Client
}
type OpenAICompatible struct {
	name, baseURL, apiKey string
	client                *http.Client
}

func NewOpenAICompatible(cfg OpenAICompatibleConfig) *OpenAICompatible {
	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}
	return &OpenAICompatible{name: cfg.ProviderName, baseURL: strings.TrimRight(cfg.BaseURL, "/"), apiKey: cfg.APIKey, client: client}
}
func (p *OpenAICompatible) Name() string { return p.name }

type openAIRequest struct {
	Model         string          `json:"model"`
	Messages      []openAIMessage `json:"messages"`
	MaxTokens     int             `json:"max_tokens,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
	StreamOptions map[string]bool `json:"stream_options,omitempty"`
}
type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type openAIResponse struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
		Delta   struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func (p *OpenAICompatible) makeRequest(ctx context.Context, req Request, stream bool) (*http.Response, error) {
	payload := openAIRequest{Model: req.Model, Messages: []openAIMessage{{Role: "user", Content: req.Input}}, MaxTokens: req.MaxOutputTokens, Stream: stream}
	if stream {
		payload.StreamOptions = map[string]bool{"include_usage": true}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	response, err := p.client.Do(httpReq)
	if err != nil {
		return nil, &Error{Class: ErrorUnavailable, Provider: p.name, Retryable: true, Message: "provider request failed", Cause: err}
	}
	if response.StatusCode >= 300 {
		defer response.Body.Close()
		return nil, normalizedHTTPError(p.name, response.StatusCode, response.Body)
	}
	return response, nil
}
func (p *OpenAICompatible) Complete(ctx context.Context, req Request) (Response, error) {
	response, err := p.makeRequest(ctx, req, false)
	if err != nil {
		return Response{}, err
	}
	defer response.Body.Close()
	var decoded openAIResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&decoded); err != nil {
		return Response{}, &Error{Class: ErrorInternal, Provider: p.name, Message: "decode provider response", Cause: err}
	}
	if len(decoded.Choices) == 0 {
		return Response{}, &Error{Class: ErrorInternal, Provider: p.name, Message: "provider returned no choices"}
	}
	return Response{Text: decoded.Choices[0].Message.Content, Usage: Usage{InputTokens: decoded.Usage.PromptTokens, OutputTokens: decoded.Usage.CompletionTokens, TotalTokens: decoded.Usage.TotalTokens}}, nil
}
func (p *OpenAICompatible) Stream(ctx context.Context, req Request) (<-chan Chunk, <-chan error) {
	chunks := make(chan Chunk)
	errs := make(chan error, 1)
	go func() {
		defer close(chunks)
		defer close(errs)
		response, err := p.makeRequest(ctx, req, true)
		if err != nil {
			errs <- err
			return
		}
		defer response.Body.Close()
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				chunks <- Chunk{Done: true}
				return
			}
			var event openAIResponse
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				errs <- &Error{Class: ErrorInternal, Provider: p.name, Message: "decode provider stream", Cause: err}
				return
			}
			usage := event.Usage
			if usage.TotalTokens > 0 {
				u := Usage{InputTokens: usage.PromptTokens, OutputTokens: usage.CompletionTokens, TotalTokens: usage.TotalTokens}
				chunks <- Chunk{Usage: &u}
			}
			if len(event.Choices) > 0 && event.Choices[0].Delta.Content != "" {
				chunks <- Chunk{Text: event.Choices[0].Delta.Content}
			}
		}
		if err := scanner.Err(); err != nil {
			errs <- &Error{Class: ErrorUnavailable, Provider: p.name, Retryable: true, Message: "read provider stream", Cause: err}
		}
	}()
	return chunks, errs
}
func (p *OpenAICompatible) Health(ctx context.Context) Health {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/v1/models", nil)
	if err != nil {
		return Health{Detail: err.Error()}
	}
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	response, err := p.client.Do(req)
	if err != nil {
		return Health{Detail: err.Error()}
	}
	response.Body.Close()
	return Health{Healthy: response.StatusCode < 500, Detail: response.Status}
}

type GeminiConfig struct {
	ProviderName, BaseURL, APIKey string
	Client                        *http.Client
}
type Gemini struct {
	name, baseURL, apiKey string
	client                *http.Client
}

func NewGemini(cfg GeminiConfig) *Gemini {
	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}
	base := cfg.BaseURL
	if base == "" {
		base = "https://generativelanguage.googleapis.com"
	}
	return &Gemini{name: cfg.ProviderName, baseURL: strings.TrimRight(base, "/"), apiKey: cfg.APIKey, client: client}
}
func (p *Gemini) Name() string { return p.name }

type geminiRequest struct {
	Contents []struct {
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"contents"`
	GenerationConfig map[string]int `json:"generationConfig,omitempty"`
}
type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	Usage struct {
		Prompt     int `json:"promptTokenCount"`
		Candidates int `json:"candidatesTokenCount"`
		Total      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
}

func (p *Gemini) makeRequest(ctx context.Context, req Request, stream bool) (*http.Response, error) {
	var payload geminiRequest
	payload.Contents = append(payload.Contents, struct {
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	}{Parts: []struct {
		Text string `json:"text"`
	}{{Text: req.Input}}})
	if req.MaxOutputTokens > 0 {
		payload.GenerationConfig = map[string]int{"maxOutputTokens": req.MaxOutputTokens}
	}
	body, _ := json.Marshal(payload)
	method := "generateContent"
	suffix := ""
	if stream {
		method = "streamGenerateContent"
		suffix = "?alt=sse"
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/v1beta/models/%s:%s%s", p.baseURL, req.Model, method, suffix), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", p.apiKey)
	response, err := p.client.Do(httpReq)
	if err != nil {
		return nil, &Error{Class: ErrorUnavailable, Provider: p.name, Retryable: true, Message: "provider request failed", Cause: err}
	}
	if response.StatusCode >= 300 {
		defer response.Body.Close()
		return nil, normalizedHTTPError(p.name, response.StatusCode, response.Body)
	}
	return response, nil
}
func decodeGemini(response geminiResponse) (Response, error) {
	if len(response.Candidates) == 0 || len(response.Candidates[0].Content.Parts) == 0 {
		return Response{}, errorsNew("provider returned no candidates")
	}
	var text strings.Builder
	for _, part := range response.Candidates[0].Content.Parts {
		text.WriteString(part.Text)
	}
	return Response{Text: text.String(), Usage: Usage{InputTokens: response.Usage.Prompt, OutputTokens: response.Usage.Candidates, TotalTokens: response.Usage.Total}}, nil
}
func errorsNew(message string) error { return &Error{Class: ErrorInternal, Message: message} }
func (p *Gemini) Complete(ctx context.Context, req Request) (Response, error) {
	response, err := p.makeRequest(ctx, req, false)
	if err != nil {
		return Response{}, err
	}
	defer response.Body.Close()
	var decoded geminiResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&decoded); err != nil {
		return Response{}, &Error{Class: ErrorInternal, Provider: p.name, Message: "decode provider response", Cause: err}
	}
	result, err := decodeGemini(decoded)
	if providerErr, ok := AsError(err); ok {
		providerErr.Provider = p.name
	}
	return result, err
}
func (p *Gemini) Stream(ctx context.Context, req Request) (<-chan Chunk, <-chan error) {
	chunks := make(chan Chunk)
	errs := make(chan error, 1)
	go func() {
		defer close(chunks)
		defer close(errs)
		response, err := p.makeRequest(ctx, req, true)
		if err != nil {
			errs <- err
			return
		}
		defer response.Body.Close()
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		var usage Usage
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			var event geminiResponse
			if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event); err != nil {
				errs <- &Error{Class: ErrorInternal, Provider: p.name, Message: "decode provider stream", Cause: err}
				return
			}
			decoded, err := decodeGemini(event)
			if err == nil && decoded.Text != "" {
				chunks <- Chunk{Text: decoded.Text}
				usage = decoded.Usage
			}
		}
		if err := scanner.Err(); err != nil {
			errs <- &Error{Class: ErrorUnavailable, Provider: p.name, Retryable: true, Message: "read provider stream", Cause: err}
			return
		}
		chunks <- Chunk{Done: true, Usage: &usage}
	}()
	return chunks, errs
}
func (p *Gemini) Health(context.Context) Health {
	if p.apiKey == "" {
		return Health{Detail: "API key not configured"}
	}
	return Health{Healthy: true, Detail: "credentials configured; passive health mode"}
}

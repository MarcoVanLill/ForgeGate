package providers

import (
	"context"
	"errors"
)

type ErrorClass string

const (
	ErrorInvalid     ErrorClass = "invalid_request"
	ErrorAuth        ErrorClass = "authentication"
	ErrorRateLimited ErrorClass = "rate_limited"
	ErrorTransient   ErrorClass = "transient"
	ErrorTimeout     ErrorClass = "timeout"
	ErrorUnavailable ErrorClass = "unavailable"
	ErrorInternal    ErrorClass = "internal"
)

type Error struct {
	Class      ErrorClass
	Provider   string
	StatusCode int
	Retryable  bool
	Message    string
	Cause      error
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return string(e.Class)
}
func (e *Error) Unwrap() error { return e.Cause }
func AsError(err error) (*Error, bool) {
	var providerErr *Error
	return providerErr, errors.As(err, &providerErr)
}

type Request struct {
	Model           string `json:"model"`
	Input           string `json:"input"`
	MaxOutputTokens int    `json:"max_output_tokens,omitempty"`
}
type Usage struct {
	InputTokens  int   `json:"input_tokens"`
	OutputTokens int   `json:"output_tokens"`
	TotalTokens  int   `json:"total_tokens"`
	CostMicros   int64 `json:"cost_micros"`
}
type Response struct {
	Text  string `json:"text"`
	Usage Usage  `json:"usage"`
}
type Chunk struct {
	Text  string
	Usage *Usage
	Done  bool
}
type Health struct {
	Healthy bool   `json:"healthy"`
	Detail  string `json:"detail,omitempty"`
}
type Provider interface {
	Name() string
	Complete(context.Context, Request) (Response, error)
	Stream(context.Context, Request) (<-chan Chunk, <-chan error)
	Health(context.Context) Health
}

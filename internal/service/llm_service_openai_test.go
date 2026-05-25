package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// newOpenAIServiceForTest builds an *LLMService with only the dependencies
// streamChatOpenAI relies on (logger + tool registry). The auth/workspace
// dependencies are not exercised by streamChatOpenAI directly.
func newOpenAIServiceForTest() *LLMService {
	log := logger.NewLoggerWithLevel("disabled")
	firecrawl := NewFirecrawlService(log)
	registry := NewServerSideToolRegistry(firecrawl, log)
	return NewLLMService(LLMServiceConfig{
		Logger:       log,
		ToolRegistry: registry,
	})
}

// TestStreamChatOpenAI_EmptyAPIKey verifies that an empty API key short-circuits
// before any network call. Without this guard, the SDK would issue an
// unauthenticated request and leak attempts to the provider.
func TestStreamChatOpenAI_EmptyAPIKey(t *testing.T) {
	svc := newOpenAIServiceForTest()

	req := &domain.LLMChatRequest{
		Messages: []domain.LLMMessage{{Role: "user", Content: "hi"}},
	}
	settings := &domain.OpenAISettings{APIKey: ""}

	err := svc.streamChatOpenAI(context.Background(), req, settings, nil, func(domain.LLMChatEvent) error {
		t.Fatal("onEvent must not be called when API key is empty")
		return nil
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "API key is not configured")
}

// TestStreamChatOpenAI_InvalidToolSchema verifies that an unparseable
// tool InputSchema fails before any network request. The bug to catch: the
// SDK is built first (line ~46), tools are parsed after (line ~85); if parse
// fails, no HTTP call must happen and the error must surface to the caller.
func TestStreamChatOpenAI_InvalidToolSchema(t *testing.T) {
	svc := newOpenAIServiceForTest()

	called := int32(0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&called, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	req := &domain.LLMChatRequest{
		Messages: []domain.LLMMessage{{Role: "user", Content: "hi"}},
		Tools: []domain.LLMTool{
			{
				Name:        "broken_tool",
				Description: "tool with malformed schema",
				// Not valid JSON object schema — should fail json.Unmarshal into FunctionParameters
				InputSchema: json.RawMessage(`not-a-json-object`),
			},
		},
	}
	settings := &domain.OpenAISettings{APIKey: "sk-test", BaseURL: server.URL}

	err := svc.streamChatOpenAI(context.Background(), req, settings, nil, func(domain.LLMChatEvent) error {
		return nil
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse tool input schema")
	assert.Equal(t, int32(0), atomic.LoadInt32(&called),
		"no HTTP request should be issued when tool schema parsing fails")
}

// TestStreamChatOpenAI_SuccessfulStream exercises the happy path against a
// httptest server that mimics the OpenAI Chat Completions SSE protocol.
// It verifies:
//   - default model "gpt-4.1" is used when settings.Model == ""
//   - default max tokens 2048 is applied when req.MaxTokens == 0
//   - text deltas are emitted as domain.LLMChatEvent{Type:"text"}
//   - final "done" event carries usage tokens and computed costs
//   - system prompt and user messages are forwarded in the request body
func TestStreamChatOpenAI_SuccessfulStream(t *testing.T) {
	var capturedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&capturedBody))

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		require.True(t, ok)

		writeChunk := func(payload string) {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		}

		// 2 text delta chunks, then a final chunk with usage, then [DONE]
		writeChunk(`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"gpt-4.1","choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"},"finish_reason":null}]}`)
		writeChunk(`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"gpt-4.1","choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":null}]}`)
		writeChunk(`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"gpt-4.1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`)
		writeChunk(`[DONE]`)
	}))
	defer server.Close()

	svc := newOpenAIServiceForTest()

	req := &domain.LLMChatRequest{
		SystemPrompt: "you are a helper",
		Messages: []domain.LLMMessage{
			{Role: "user", Content: "say hi"},
		},
		// MaxTokens omitted → must default to 2048
	}
	settings := &domain.OpenAISettings{
		APIKey:  "sk-test",
		BaseURL: server.URL,
		// Model omitted → must default to "gpt-4.1"
	}

	var events []domain.LLMChatEvent
	err := svc.streamChatOpenAI(context.Background(), req, settings, nil, func(e domain.LLMChatEvent) error {
		events = append(events, e)
		return nil
	})
	require.NoError(t, err)

	// Validate captured request body
	require.NotNil(t, capturedBody, "server must have received the streaming request")
	assert.Equal(t, "gpt-4.1", capturedBody["model"], "default model must be gpt-4.1 when settings.Model is empty")
	assert.EqualValues(t, 2048, capturedBody["max_completion_tokens"], "default max tokens must be 2048")
	messages, ok := capturedBody["messages"].([]interface{})
	require.True(t, ok)
	require.Len(t, messages, 2, "system prompt + user message")
	sysMsg := messages[0].(map[string]interface{})
	assert.Equal(t, "system", sysMsg["role"])
	userMsg := messages[1].(map[string]interface{})
	assert.Equal(t, "user", userMsg["role"])

	// Validate event sequence: 2 text deltas + final done with cost & model
	var textChunks []string
	var done *domain.LLMChatEvent
	for i := range events {
		switch events[i].Type {
		case "text":
			textChunks = append(textChunks, events[i].Content)
		case "done":
			done = &events[i]
		}
	}
	assert.Equal(t, "Hello", strings.Join(textChunks, ""), "text deltas must concatenate to full content")
	require.NotNil(t, done, "stream must end with a 'done' event")
	require.NotNil(t, done.InputTokens)
	require.NotNil(t, done.OutputTokens)
	assert.EqualValues(t, 7, *done.InputTokens)
	assert.EqualValues(t, 3, *done.OutputTokens)
	assert.Equal(t, "gpt-4.1", done.Model, "done event must report the model actually used")
	require.NotNil(t, done.TotalCost, "done event must include cost (even if zero for unknown models)")
}

// TestStreamChatOpenAI_StreamError verifies that an HTTP error from the
// provider surfaces as a wrapped "stream error" returned by streamChatOpenAI.
// This protects callers (SSE handler in http layer) from silent failures
// when the upstream API is rate-limited or down.
func TestStreamChatOpenAI_StreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limit exceeded","type":"rate_limit_error"}}`))
	}))
	defer server.Close()

	svc := newOpenAIServiceForTest()

	req := &domain.LLMChatRequest{
		Messages: []domain.LLMMessage{{Role: "user", Content: "hi"}},
	}
	settings := &domain.OpenAISettings{
		APIKey:  "sk-test",
		BaseURL: server.URL,
		Model:   "gpt-4o-mini",
	}

	err := svc.streamChatOpenAI(context.Background(), req, settings, nil, func(domain.LLMChatEvent) error {
		return nil
	})
	require.Error(t, err)
}

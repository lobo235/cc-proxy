package codex

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lobo235/cc-proxy/internal/provider"
)

func TestProviderCountTokensUsesTranslatorCounter(t *testing.T) {
	p := Provider{}
	resp, err := p.CountTokens(context.Background(), provider.CountTokensCall{
		Request: provider.AnthropicMessagesRequest{
			Model: "gpt-5.4",
			Raw: json.RawMessage(`{
				"model":"gpt-5.4",
				"messages":[{"role":"user","content":"hello world"}]
			}`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.InputTokens <= 0 {
		t.Fatalf("input tokens = %d, want positive", resp.InputTokens)
	}
}

func TestProviderMessagesIsExplicitlyNotImplemented(t *testing.T) {
	p := Provider{}
	err := p.Messages(context.Background(), provider.MessagesCall{}, nil)
	if err == nil {
		t.Fatal("expected not implemented error")
	}
}

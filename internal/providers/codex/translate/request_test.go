package translate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lobo235/cc-proxy/internal/provider"
)

func TestTranslateUserTextMessage(t *testing.T) {
	req := provider.AnthropicMessagesRequest{
		Model:    "gpt-5.4",
		Messages: json.RawMessage(`[{"role":"user","content":"hello"}]`),
		Raw:      json.RawMessage(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}]}`),
	}
	got, err := Translate(req, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "gpt-5.4" {
		t.Fatalf("model = %q, want gpt-5.4", got.Model)
	}
	if !got.Stream {
		t.Fatal("stream = false, want true")
	}
	if got.Store {
		t.Fatal("store = true, want false")
	}
	if got.Text.Verbosity != "low" {
		t.Fatalf("verbosity = %q, want low", got.Text.Verbosity)
	}
	if len(got.Input) != 1 {
		t.Fatalf("input len = %d, want 1", len(got.Input))
	}
	item := got.Input[0]
	if item.Type != "message" || item.Role != "user" {
		t.Fatalf("input[0] = %+v, want user message", item)
	}
	if len(item.Content) != 1 || item.Content[0].Type != "input_text" || item.Content[0].Text != "hello" {
		t.Fatalf("content = %+v, want input_text hello", item.Content)
	}
}

func TestTranslateSystemInstructionsDropsBillingHeader(t *testing.T) {
	req := provider.AnthropicMessagesRequest{
		Model: "gpt-5.4",
		Raw: json.RawMessage(`{
			"model":"gpt-5.4",
			"system":[
				{"type":"text","text":"x-anthropic-billing-header: ignore me"},
				{"type":"text","text":"follow instructions"},
				{"type":"text","text":"be concise"}
			],
			"messages":[{"role":"user","content":"hello"}]
		}`),
	}
	got, err := Translate(req, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Instructions != "follow instructions\n\nbe concise" {
		t.Fatalf("instructions = %q", got.Instructions)
	}
}

func TestTranslateToolUseAndToolResult(t *testing.T) {
	req := provider.AnthropicMessagesRequest{
		Model: "gpt-5.4",
		Raw: json.RawMessage(`{
			"model":"gpt-5.4",
			"messages":[
				{"role":"assistant","content":[
					{"type":"text","text":"checking"},
					{"type":"tool_use","id":"toolu_123","name":"lookup_weather","input":{"city":"Denver"}}
				]},
				{"role":"user","content":[
					{"type":"tool_result","tool_use_id":"toolu_123","content":[{"type":"text","text":"sunny"}]}
				]}
			]
		}`),
	}
	got, err := Translate(req, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Input) != 3 {
		t.Fatalf("input len = %d, want 3: %+v", len(got.Input), got.Input)
	}
	if got.Input[0].Type != "message" || got.Input[0].Role != "assistant" || got.Input[0].Content[0].Type != "output_text" {
		t.Fatalf("assistant text item = %+v", got.Input[0])
	}
	if got.Input[1].Type != "function_call" || got.Input[1].CallID != "toolu_123" || got.Input[1].Name != "lookup_weather" {
		t.Fatalf("function call item = %+v", got.Input[1])
	}
	if got.Input[1].Arguments != `{"city":"Denver"}` {
		t.Fatalf("arguments = %q", got.Input[1].Arguments)
	}
	if got.Input[2].Type != "function_call_output" || got.Input[2].CallID != "toolu_123" || got.Input[2].Output != "sunny" {
		t.Fatalf("function output item = %+v", got.Input[2])
	}
}

func TestTranslateAddsPropertiesToEmptyObjectToolSchemas(t *testing.T) {
	req := provider.AnthropicMessagesRequest{
		Model: "gpt-5.5",
		Raw: json.RawMessage(`{
			"model":"gpt-5.5",
			"tools":[{"name":"mcp__homelab__adguard_rewrite_list","input_schema":{"type":"object"}}],
			"messages":[{"role":"user","content":"list rewrites"}]
		}`),
	}
	got, err := Translate(req, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(got.Tools))
	}
	var params map[string]any
	if err := json.Unmarshal(got.Tools[0].Parameters, &params); err != nil {
		t.Fatal(err)
	}
	if params["type"] != "object" {
		t.Fatalf("parameters.type = %v, want object", params["type"])
	}
	properties, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("parameters.properties = %#v, want empty object", params["properties"])
	}
	if len(properties) != 0 {
		t.Fatalf("properties len = %d, want 0", len(properties))
	}
}

func TestTranslateAddsItemsToArrayToolSchemas(t *testing.T) {
	req := provider.AnthropicMessagesRequest{
		Model: "gpt-5.5",
		Raw: json.RawMessage(`{
			"model":"gpt-5.5",
			"tools":[{
				"name":"mcp__homelab__vault_kv_put_shared",
				"input_schema":{
					"type":"object",
					"properties":{
						"auto_generate_keys":{"type":"array"}
					}
				}
			}],
			"messages":[{"role":"user","content":"write secret"}]
		}`),
	}
	got, err := Translate(req, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var params struct {
		Properties map[string]struct {
			Type  string         `json:"type"`
			Items map[string]any `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(got.Tools[0].Parameters, &params); err != nil {
		t.Fatal(err)
	}
	autoGenerateKeys := params.Properties["auto_generate_keys"]
	if autoGenerateKeys.Type != "array" {
		t.Fatalf("auto_generate_keys.type = %q, want array", autoGenerateKeys.Type)
	}
	if autoGenerateKeys.Items == nil {
		t.Fatal("auto_generate_keys.items = nil, want empty schema object")
	}
	if len(autoGenerateKeys.Items) != 0 {
		t.Fatalf("auto_generate_keys.items len = %d, want 0", len(autoGenerateKeys.Items))
	}
}

func TestTranslateAddsSkillToolDisabledSkillInstructions(t *testing.T) {
	req := provider.AnthropicMessagesRequest{
		Model: "gpt-5.5",
		Raw: json.RawMessage(`{
			"model":"gpt-5.5",
			"system":"base instructions",
			"messages":[{"role":"user","content":"hello"}],
			"tools":[{"name":"Skill","description":"run a skill","input_schema":{"type":"object","properties":{"skill":{"type":"string"}}}}]
		}`),
	}
	got, err := Translate(req, Options{DisabledSkillToolSkills: []string{"domain-model", "ubiquitous-language"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"base instructions",
		"Do not call the Skill tool",
		"domain-model",
		"ubiquitous-language",
		"disable-model-invocation: true",
	} {
		if !strings.Contains(got.Instructions, want) {
			t.Fatalf("instructions missing %q:\n%s", want, got.Instructions)
		}
	}
}

func TestTranslateAddsClaudeCodeFileToolInstructions(t *testing.T) {
	req := provider.AnthropicMessagesRequest{
		Model: "gpt-5.5",
		Raw: json.RawMessage(`{
			"model":"gpt-5.5",
			"system":"base instructions",
			"messages":[{"role":"user","content":"read a file"}],
			"tools":[{"name":"Read","description":"read file","input_schema":{"type":"object","properties":{"file_path":{"type":"string"},"offset":{"type":"number"},"limit":{"type":"number"},"pages":{"type":"string"}}}}]
		}`),
	}
	got, err := Translate(req, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"base instructions",
		"Claude Code file tools are line-oriented",
		"offset and limit refer to source-file line numbers/counts",
		"use pages only for PDF files",
	} {
		if !strings.Contains(got.Instructions, want) {
			t.Fatalf("instructions missing %q:\n%s", want, got.Instructions)
		}
	}
}

func TestTranslateEffortMapping(t *testing.T) {
	tests := []struct {
		name        string
		effort      string
		wantEffort  string
		wantInclude bool
	}{
		{name: "absent"},
		{name: "none", effort: "none", wantEffort: "none"},
		{name: "minimal", effort: "minimal", wantEffort: "minimal", wantInclude: true},
		{name: "low", effort: "low", wantEffort: "low", wantInclude: true},
		{name: "medium", effort: "medium", wantEffort: "medium", wantInclude: true},
		{name: "high", effort: "high", wantEffort: "high", wantInclude: true},
		{name: "xhigh", effort: "xhigh", wantEffort: "xhigh", wantInclude: true},
		{name: "max maps to xhigh", effort: "max", wantEffort: "xhigh", wantInclude: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outputConfig := ""
			if tt.effort != "" {
				outputConfig = `,"output_config":{"effort":"` + tt.effort + `"}`
			}
			req := provider.AnthropicMessagesRequest{
				Model: "gpt-5.4",
				Raw:   json.RawMessage(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}]` + outputConfig + `}`),
			}
			got, err := Translate(req, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantEffort == "" {
				if got.Reasoning != nil {
					t.Fatalf("reasoning = %+v, want nil", got.Reasoning)
				}
				if len(got.Include) != 0 {
					t.Fatalf("include = %+v, want empty", got.Include)
				}
				return
			}
			if got.Reasoning == nil || got.Reasoning.Effort != tt.wantEffort {
				t.Fatalf("reasoning = %+v, want effort %q", got.Reasoning, tt.wantEffort)
			}
			if tt.wantInclude && !containsString(got.Include, "reasoning.encrypted_content") {
				t.Fatalf("include = %+v, want reasoning.encrypted_content", got.Include)
			}
		})
	}
}

func TestTranslateEffortOverrideTakesPrecedence(t *testing.T) {
	req := provider.AnthropicMessagesRequest{
		Model: "gpt-5.4",
		Raw:   json.RawMessage(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"output_config":{"effort":"low"}}`),
	}
	got, err := Translate(req, Options{Effort: "max"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Reasoning == nil || got.Reasoning.Effort != "xhigh" {
		t.Fatalf("reasoning = %+v, want override mapped to xhigh", got.Reasoning)
	}
}

func TestTranslateEffortOverrideNoneForcesNoReasoning(t *testing.T) {
	req := provider.AnthropicMessagesRequest{
		Model: "gpt-5.4",
		Raw:   json.RawMessage(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"output_config":{"effort":"high"}}`),
	}
	got, err := Translate(req, Options{Effort: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Reasoning == nil || got.Reasoning.Effort != "none" {
		t.Fatalf("reasoning = %+v, want effort none", got.Reasoning)
	}
	if len(got.Include) != 0 {
		t.Fatalf("include = %+v, want empty", got.Include)
	}
}

func TestTranslateRejectsInvalidEffortOverride(t *testing.T) {
	req := provider.AnthropicMessagesRequest{
		Model: "gpt-5.4",
		Raw:   json.RawMessage(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}]}`),
	}
	_, err := Translate(req, Options{Effort: "extreme"})
	if err == nil {
		t.Fatal("expected invalid override effort error")
	}
}

func TestTranslateRejectsInvalidEffort(t *testing.T) {
	req := provider.AnthropicMessagesRequest{
		Model: "gpt-5.4",
		Raw:   json.RawMessage(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"output_config":{"effort":"extreme"}}`),
	}
	_, err := Translate(req, Options{})
	if err == nil {
		t.Fatal("expected invalid effort error")
	}
}

func TestTranslateCacheKeyDefaultsToSessionID(t *testing.T) {
	req := provider.AnthropicMessagesRequest{
		Model: "gpt-5.5",
		Raw:   json.RawMessage(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}]}`),
	}
	got, err := Translate(req, Options{SessionID: "sess-abc"})
	if err != nil {
		t.Fatal(err)
	}
	if got.PromptCacheKey != "sess-abc" {
		t.Fatalf("PromptCacheKey = %q, want session id", got.PromptCacheKey)
	}
}

func TestTranslateStableCacheKeyIsConstantAcrossSessions(t *testing.T) {
	req := provider.AnthropicMessagesRequest{
		Model: "gpt-5.5",
		Raw:   json.RawMessage(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}]}`),
	}
	a, err := Translate(req, Options{SessionID: "sess-aaa", Model: "gpt-5.5", CacheKeyStrategy: CacheKeyStrategyStable})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Translate(req, Options{SessionID: "sess-zzz", Model: "gpt-5.5", CacheKeyStrategy: CacheKeyStrategyStable})
	if err != nil {
		t.Fatal(err)
	}
	if a.PromptCacheKey == "" {
		t.Fatal("stable PromptCacheKey is empty")
	}
	if a.PromptCacheKey != b.PromptCacheKey {
		t.Fatalf("stable key not stable: %q vs %q", a.PromptCacheKey, b.PromptCacheKey)
	}
	if a.PromptCacheKey == "sess-aaa" {
		t.Fatal("stable key should not equal session id")
	}
}

func TestTranslateStableCacheKeyDiffersByModel(t *testing.T) {
	req := provider.AnthropicMessagesRequest{
		Model: "gpt-5.5",
		Raw:   json.RawMessage(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}]}`),
	}
	a, err := Translate(req, Options{SessionID: "s", Model: "gpt-5.5", CacheKeyStrategy: CacheKeyStrategyStable})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Translate(req, Options{SessionID: "s", Model: "gpt-5.4", CacheKeyStrategy: CacheKeyStrategyStable})
	if err != nil {
		t.Fatal(err)
	}
	if a.PromptCacheKey == b.PromptCacheKey {
		t.Fatalf("expected different stable keys per model, both = %q", a.PromptCacheKey)
	}
}

func TestTranslatePrefixIsByteStableAcrossInvocations(t *testing.T) {
	raw := json.RawMessage(`{
		"model":"gpt-5.5",
		"system":[{"type":"text","text":"alpha"},{"type":"text","text":"beta"}],
		"tools":[
			{"name":"Read","description":"read file","input_schema":{"type":"object","properties":{"file_path":{"type":"string"}}}},
			{"name":"Skill","description":"run skill","input_schema":{"type":"object","properties":{"skill":{"type":"string"}}}}
		],
		"messages":[{"role":"user","content":"hi"}]
	}`)
	mkReq := func() provider.AnthropicMessagesRequest {
		// copy raw to avoid any aliasing surprises across translate calls
		buf := make(json.RawMessage, len(raw))
		copy(buf, raw)
		return provider.AnthropicMessagesRequest{Model: "gpt-5.5", Raw: buf}
	}
	opts := Options{
		SessionID:               "sess-xyz",
		Model:                   "gpt-5.5",
		DisabledSkillToolSkills: []string{"domain-model", "ubiquitous-language"},
	}
	a, err := Translate(mkReq(), opts)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Translate(mkReq(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if a.Instructions != b.Instructions {
		t.Fatalf("instructions drifted between calls:\n  a=%q\n  b=%q", a.Instructions, b.Instructions)
	}
	aTools, err := json.Marshal(a.Tools)
	if err != nil {
		t.Fatal(err)
	}
	bTools, err := json.Marshal(b.Tools)
	if err != nil {
		t.Fatal(err)
	}
	if string(aTools) != string(bTools) {
		t.Fatalf("tools JSON drifted between calls:\n  a=%s\n  b=%s", aTools, bTools)
	}
}

func TestCountTokensIncludesTextToolsAndImages(t *testing.T) {
	req := provider.AnthropicMessagesRequest{
		Model: "gpt-5.4",
		Raw: json.RawMessage(`{
			"model":"gpt-5.4",
			"system":"follow instructions",
			"tools":[{"name":"lookup_weather","description":"Look up weather","input_schema":{"type":"object"}}],
			"messages":[
				{"role":"user","content":[
					{"type":"text","text":"hello world"},
					{"type":"image","source":{"type":"base64","media_type":"image/png","data":"abc"}}
				]},
				{"role":"assistant","content":[
					{"type":"tool_use","id":"toolu_123","name":"lookup_weather","input":{"city":"Denver"}}
				]}
			]
		}`),
	}
	got, err := CountTokens(req)
	if err != nil {
		t.Fatal(err)
	}
	if got < 2000 {
		t.Fatalf("tokens = %d, want image estimate included", got)
	}
}

func containsString(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}

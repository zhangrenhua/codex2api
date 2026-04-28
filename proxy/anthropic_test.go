package proxy

import (
	"encoding/json"
	"testing"
)

func TestTranslateAnthropicToCodexCanonicalizesDynamicMappedModelAlias(t *testing.T) {
	raw := []byte(`{
		"model":"claude-haiku-4-5-20251001",
		"max_tokens":1024,
		"messages":[{"role":"user","content":"hello"}]
	}`)

	body, originalModel, err := TranslateAnthropicToCodexWithModels(raw, `{"claude-haiku-4-5-20251001":"gpt5-4"}`, []string{"gpt-5.4", "gpt-5.4-mini"})
	if err != nil {
		t.Fatalf("TranslateAnthropicToCodexWithModels returned error: %v", err)
	}
	if originalModel != "claude-haiku-4-5-20251001" {
		t.Fatalf("originalModel = %q, want claude-haiku-4-5-20251001", originalModel)
	}

	var out struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal translated body: %v", err)
	}
	if out.Model != "gpt-5.4" {
		t.Fatalf("translated model = %q, want gpt-5.4", out.Model)
	}
}

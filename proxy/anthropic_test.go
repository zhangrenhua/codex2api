package proxy

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestTranslateAnthropicToCodex_OutputConfigEffortTakesPrecedence(t *testing.T) {
	raw := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"hello"}],
		"thinking":{"type":"enabled","budget_tokens":512},
		"output_config":{"effort":"max"}
	}`)

	got, _, err := TranslateAnthropicToCodexWithModels(raw, "", []string{"gpt-5.4", "gpt-5.4-mini"})
	if err != nil {
		t.Fatalf("TranslateAnthropicToCodexWithModels returned error: %v", err)
	}

	if effort := gjson.GetBytes(got, "reasoning.effort").String(); effort != "xhigh" {
		t.Fatalf("reasoning.effort = %q, want xhigh; body=%s", effort, got)
	}
	if summary := gjson.GetBytes(got, "reasoning.summary").String(); summary != "auto" {
		t.Fatalf("reasoning.summary = %q, want auto; body=%s", summary, got)
	}
}

func TestTranslateAnthropicToCodex_OutputConfigHighIsExplicit(t *testing.T) {
	raw := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"hello"}],
		"output_config":{"effort":"high"}
	}`)

	got, _, err := TranslateAnthropicToCodexWithModels(raw, "", []string{"gpt-5.4"})
	if err != nil {
		t.Fatalf("TranslateAnthropicToCodexWithModels returned error: %v", err)
	}

	if effort := gjson.GetBytes(got, "reasoning.effort").String(); effort != "high" {
		t.Fatalf("reasoning.effort = %q, want high; body=%s", effort, got)
	}
}

func TestTranslateAnthropicToCodex_DefaultsReasoningHighWithSummary(t *testing.T) {
	raw := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"hello"}]
	}`)

	got, _, err := TranslateAnthropicToCodexWithModels(raw, "", []string{"gpt-5.4"})
	if err != nil {
		t.Fatalf("TranslateAnthropicToCodexWithModels returned error: %v", err)
	}

	if effort := gjson.GetBytes(got, "reasoning.effort").String(); effort != "high" {
		t.Fatalf("reasoning.effort = %q, want high; body=%s", effort, got)
	}
	if summary := gjson.GetBytes(got, "reasoning.summary").String(); summary != "auto" {
		t.Fatalf("reasoning.summary = %q, want auto; body=%s", summary, got)
	}
}

func TestTranslateAnthropicToCodex_ThinkingBudgetDoesNotControlEffort(t *testing.T) {
	raw := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"hello"}],
		"thinking":{"type":"enabled","budget_tokens":4096}
	}`)

	got, _, err := TranslateAnthropicToCodexWithModels(raw, "", []string{"gpt-5.4"})
	if err != nil {
		t.Fatalf("TranslateAnthropicToCodexWithModels returned error: %v", err)
	}

	if effort := gjson.GetBytes(got, "reasoning.effort").String(); effort != "high" {
		t.Fatalf("reasoning.effort = %q, want high; body=%s", effort, got)
	}
}

package proxy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func msg(role, text string) map[string]any {
	contentType := "input_text"
	if role == "assistant" {
		contentType = "output_text"
	}
	return map[string]any{
		"type": "message",
		"role": role,
		"content": []any{
			map[string]any{"type": contentType, "text": text},
		},
	}
}

func bodyWithInput(items []any) []byte {
	b, _ := json.Marshal(map[string]any{
		"model": "gpt-5.4",
		"input": items,
	})
	return b
}

func inputItemsFromBody(t *testing.T, body []byte) []any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	items, ok := m["input"].([]any)
	if !ok {
		t.Fatalf("body has no input array")
	}
	return items
}

func neverSummarize(context.Context, string) (string, error) {
	return "SUMMARY", nil
}

func TestApplyContextWindow_UnderThreshold_NoOp(t *testing.T) {
	items := []any{msg("user", "hello world")}
	body := bodyWithInput(items)
	res := ApplyContextWindow(context.Background(), body, 100000, neverSummarize)
	if res.Applied {
		t.Fatalf("expected not applied for small input")
	}
	if res.OriginalInputTokens != 0 {
		t.Fatalf("expected 0 original tokens, got %d", res.OriginalInputTokens)
	}
}

func TestApplyContextWindow_SummaryThenFits(t *testing.T) {
	long := strings.Repeat("a", 4000) // ~1000 tokens each
	items := []any{
		msg("developer", "system instructions"),
		msg("user", "old question 1 "+long),
		msg("assistant", "old answer 1 "+long),
		msg("user", "old question 2 "+long),
		msg("assistant", "old answer 2 "+long),
		msg("user", "recent question"),
	}
	body := bodyWithInput(items)
	threshold := 600
	orig := EstimateResponsesInputTokens(body)
	if orig <= threshold {
		t.Fatalf("test setup: orig %d should exceed threshold %d", orig, threshold)
	}

	res := ApplyContextWindow(context.Background(), body, threshold, func(_ context.Context, transcript string) (string, error) {
		if transcript == "" {
			t.Fatalf("summarizer got empty transcript")
		}
		return "concise summary of earlier turns", nil
	})
	if !res.Applied {
		t.Fatalf("expected applied")
	}
	if res.OriginalInputTokens != orig {
		t.Fatalf("expected original tokens %d, got %d", orig, res.OriginalInputTokens)
	}
	out := inputItemsFromBody(t, res.Body)
	if got := estimateItemsTokens(out); got > threshold {
		t.Fatalf("result %d exceeds threshold %d", got, threshold)
	}
	// recent question must be preserved verbatim.
	if !strings.Contains(string(res.Body), "recent question") {
		t.Fatalf("recent user message was dropped")
	}
}

func TestApplyContextWindow_SummaryFails_FallsBackToTruncation(t *testing.T) {
	long := strings.Repeat("b", 4000)
	items := []any{
		msg("developer", "instructions"),
		msg("user", "q1 "+long),
		msg("assistant", "a1 "+long),
		msg("user", "q2 "+long),
		msg("assistant", "a2 "+long),
		msg("user", "latest"),
	}
	body := bodyWithInput(items)
	threshold := 600
	res := ApplyContextWindow(context.Background(), body, threshold, func(context.Context, string) (string, error) {
		return "", context.DeadlineExceeded
	})
	if !res.Applied {
		t.Fatalf("expected applied via truncation")
	}
	out := inputItemsFromBody(t, res.Body)
	if got := estimateItemsTokens(out); got > threshold {
		t.Fatalf("truncation result %d exceeds threshold %d", got, threshold)
	}
	if !strings.Contains(string(res.Body), "latest") {
		t.Fatalf("latest user message must be kept")
	}
}

func TestApplyContextWindow_NilSummarize_StructuredTruncation(t *testing.T) {
	long := strings.Repeat("c", 4000)
	items := []any{
		msg("user", "q1 "+long),
		msg("assistant", "a1 "+long),
		msg("user", "q2 "+long),
		msg("assistant", "a2 "+long),
		msg("user", "final"),
	}
	body := bodyWithInput(items)
	threshold := 500
	res := ApplyContextWindow(context.Background(), body, threshold, nil)
	if !res.Applied {
		t.Fatalf("expected applied")
	}
	out := inputItemsFromBody(t, res.Body)
	if got := estimateItemsTokens(out); got > threshold {
		t.Fatalf("result %d exceeds threshold %d", got, threshold)
	}
}

func TestApplyContextWindow_SingleHugeMessageGetsTruncated(t *testing.T) {
	huge := strings.Repeat("d", 40000) // ~10000 tokens, single message
	items := []any{msg("user", huge)}
	body := bodyWithInput(items)
	threshold := 500
	res := ApplyContextWindow(context.Background(), body, threshold, nil)
	if !res.Applied {
		t.Fatalf("expected applied")
	}
	out := inputItemsFromBody(t, res.Body)
	if got := estimateItemsTokens(out); got > threshold {
		t.Fatalf("result %d exceeds threshold %d", got, threshold)
	}
	if !strings.Contains(string(res.Body), strings.TrimSpace(truncationMarker)) {
		t.Fatalf("expected truncation marker in single-message truncation")
	}
}

func TestSplitOldestForSummary_AlignsToUserBoundary(t *testing.T) {
	long := strings.Repeat("e", 4000)
	items := []any{
		msg("user", "u1 "+long),
		msg("assistant", "a1 "+long),
		msg("user", "u2 "+long),
		msg("assistant", "a2 "+long),
		msg("user", "u3"),
	}
	// threshold large enough to include the first turn (user+assistant ≈ 2008
	// tokens) but not all turns.
	split := splitOldestForSummary(items, 0, 2500)
	if split <= 0 {
		t.Fatalf("expected a positive split, got %d", split)
	}
	if !isUserMessage(items[split]) {
		t.Fatalf("split point %d is not a user-message boundary", split)
	}
}

func TestStructuredTruncate_KeepsLeadingDeveloperAndLastTurn(t *testing.T) {
	long := strings.Repeat("f", 4000)
	items := []any{
		msg("developer", "pinned instructions"),
		msg("user", "old "+long),
		msg("assistant", "old reply "+long),
		msg("user", "current ask"),
	}
	out := structuredTruncate(items, 400)
	joined := mustJSON(t, out)
	if !strings.Contains(joined, "pinned instructions") {
		t.Fatalf("leading developer message must be kept")
	}
	if !strings.Contains(joined, "current ask") {
		t.Fatalf("last user turn must be kept")
	}
	if estimateItemsTokens(out) > 400 {
		t.Fatalf("structuredTruncate did not enforce threshold")
	}
}

func TestCompressViaSummary_PinsLeadingInstructions(t *testing.T) {
	long := strings.Repeat("g", 4000)
	sysPrompt := "SYSTEM PROMPT: always answer in JSON. UNIQUE_MARKER_42"
	items := []any{
		msg("developer", sysPrompt),
		msg("user", "q1 "+long),
		msg("assistant", "a1 "+long),
		msg("user", "q2 "+long),
		msg("assistant", "a2 "+long),
		msg("user", "recent"),
	}
	out, ok := compressViaSummary(context.Background(), items, 3000, func(_ context.Context, transcript string) (string, error) {
		// the system prompt must NOT be handed to the summarizer.
		if strings.Contains(transcript, "UNIQUE_MARKER_42") {
			t.Fatalf("leading instruction leaked into summarizer transcript")
		}
		return "SUMMARY_TEXT", nil
	})
	if !ok {
		t.Fatalf("expected summary to apply")
	}
	joined := mustJSON(t, out)
	// system prompt preserved verbatim, summary present, head before summary.
	if !strings.Contains(joined, "UNIQUE_MARKER_42") {
		t.Fatalf("leading instruction must be preserved verbatim")
	}
	if !strings.Contains(joined, "SUMMARY_TEXT") {
		t.Fatalf("summary must be inserted")
	}
	if !isDeveloperOrSystem(out[0]) || messageText(out[0]) != sysPrompt {
		t.Fatalf("first item must remain the original system prompt, got %v", out[0])
	}
}

func TestTruncate_DoesNotCorruptEncryptedContent(t *testing.T) {
	encrypted := strings.Repeat("Z", 40000) // huge, must never be truncated
	// Put the huge encrypted reasoning in the SAME (only) turn as the current user
	// query, so it cannot be dropped by turn-removal — forcing the text-truncation path.
	items := []any{
		msg("user", strings.Repeat("u", 3000)),
		map[string]any{"type": "reasoning", "encrypted_content": encrypted, "summary": []any{}},
	}
	out := structuredTruncate(items, 500)
	joined := mustJSON(t, out)
	// encrypted_content must survive intact (full length, never truncated).
	if !strings.Contains(joined, encrypted) {
		t.Fatalf("encrypted_content was truncated/corrupted")
	}
}

func TestFindLongestString_OnlyTruncatesFreeText(t *testing.T) {
	item := map[string]any{
		"type":              "reasoning",               // structural, never truncated
		"encrypted_content": strings.Repeat("E", 9999), // protected, never truncated
		"content": []any{
			map[string]any{"type": "input_text", "text": "the actual free text body"},
		},
	}
	s, set := findLongestString(item)
	if set == nil {
		t.Fatalf("expected a truncatable string")
	}
	if s != "the actual free text body" {
		t.Fatalf("expected the text field, got %q", s)
	}
}

func TestApplyOriginalInputTokens(t *testing.T) {
	u := &UsageInfo{InputTokens: 50, PromptTokens: 50, OutputTokens: 10, TotalTokens: 60}
	u.applyOriginalInputTokens(1000)
	if u.InputTokens != 1000 || u.PromptTokens != 1000 {
		t.Fatalf("input/prompt not overridden: %+v", u)
	}
	if u.TotalTokens != 1010 {
		t.Fatalf("total tokens = %d, want 1010", u.TotalTokens)
	}
	// nil / zero are no-ops.
	var nilU *UsageInfo
	nilU.applyOriginalInputTokens(5) // must not panic
	u.applyOriginalInputTokens(0)
	if u.InputTokens != 1000 {
		t.Fatalf("zero override should be no-op")
	}
}

func TestRewriteCompletedUsageInputTokens(t *testing.T) {
	data := []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":7,"total_tokens":17}}}`)
	out := rewriteCompletedUsageInputTokens(data, 999)
	if got := gjson.GetBytes(out, "response.usage.input_tokens").Int(); got != 999 {
		t.Fatalf("input_tokens = %d, want 999", got)
	}
	if got := gjson.GetBytes(out, "response.usage.total_tokens").Int(); got != 1006 {
		t.Fatalf("total_tokens = %d, want 1006", got)
	}
	// no usage present → unchanged
	noUsage := []byte(`{"type":"response.completed","response":{}}`)
	if string(rewriteCompletedUsageInputTokens(noUsage, 5)) != string(noUsage) {
		t.Fatalf("expected unchanged when no usage")
	}
}

func TestRewriteResponseObjectUsageInputTokens(t *testing.T) {
	data := []byte(`{"id":"resp_1","usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7}}`)
	out := rewriteResponseObjectUsageInputTokens(data, 500)
	if got := gjson.GetBytes(out, "usage.input_tokens").Int(); got != 500 {
		t.Fatalf("input_tokens = %d, want 500", got)
	}
	if got := gjson.GetBytes(out, "usage.total_tokens").Int(); got != 504 {
		t.Fatalf("total_tokens = %d, want 504", got)
	}
}

func TestCopyResponsesInput(t *testing.T) {
	src := bodyWithInput([]any{msg("developer", "summary"), msg("user", "kept")})
	dst := []byte(`{"model":"gpt-5.4","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"OLD AND LONG"}]}],"reasoning":{"effort":"low"}}`)
	out := copyResponsesInput(dst, src)

	// dst's input must now match src's input (compacted), other fields preserved.
	if gjson.GetBytes(out, "reasoning.effort").String() != "low" {
		t.Fatalf("non-input fields must be preserved")
	}
	if n := gjson.GetBytes(out, "input.#").Int(); n != 2 {
		t.Fatalf("input length = %d, want 2", n)
	}
	if !strings.Contains(string(out), "kept") || strings.Contains(string(out), "OLD AND LONG") {
		t.Fatalf("dst input was not replaced by src input: %s", out)
	}

	// src without input → dst unchanged.
	noInput := []byte(`{"model":"x"}`)
	if string(copyResponsesInput(dst, noInput)) != string(dst) {
		t.Fatalf("expected dst unchanged when src has no input")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

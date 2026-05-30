package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/codex2api/auth"
	"github.com/tidwall/gjson"
)

// 算法 1M：摘要的真实 LLM 调用与编排。

const (
	// summarizeTimeout 单次摘要调用的最长等待时间。
	summarizeTimeout = 90 * time.Second

	summarizerInstructions = "You are a context-compression assistant. Summarize the earlier portion of a conversation so it can replace the original turns while preserving everything needed to continue. Be faithful and concise."

	summarizeUserPrompt = "Summarize the following earlier conversation turns. Preserve: the user's goals and constraints, key decisions, important facts, file/identifier names, and any unresolved questions or TODOs. Use compact bullet points. Do not add commentary, do not answer the conversation — only summarize.\n\n===== CONVERSATION START =====\n"
)

// applyContextWindow 对 Codex responses 请求体执行算法 1M（若已启用且超阈值）。
// 返回处理后的请求体与"原始 input_tokens"（未触发时为入参 body 与 0）。
// 摘要会临时借用账号池中的一个账号；任何失败都会安全退化为纯结构化截断。
func (h *Handler) applyContextWindow(ctx context.Context, affinityKey string, apiKeyID int64, codexBody []byte) ([]byte, int) {
	cfg := CurrentRuntimeSettings()
	if !cfg.ContextWindowEnabled {
		return codexBody, 0
	}
	threshold := cfg.ContextWindowThreshold
	if threshold <= 0 {
		return codexBody, 0
	}
	if EstimateResponsesInputTokens(codexBody) <= threshold {
		return codexBody, 0
	}

	if ctx == nil {
		ctx = context.Background()
	}

	// 借一个账号用于摘要调用；借不到则跳过摘要，只做结构化截断。
	var summarize SummarizeFunc
	if account, _ := h.nextAccountForSession(affinityKey, apiKeyID, nil); account != nil {
		defer h.store.Release(account)
		model := cfg.ContextSummaryModel
		summarize = func(sctx context.Context, transcript string) (string, error) {
			return h.summarizeTranscript(sctx, account, model, transcript)
		}
	}

	res := ApplyContextWindow(ctx, codexBody, threshold, summarize)
	if !res.Applied {
		return codexBody, 0
	}
	log.Printf("算法1M: input 估算 %d tokens 超过阈值 %d，已压缩（摘要可用=%t）", res.OriginalInputTokens, threshold, summarize != nil)
	return res.Body, res.OriginalInputTokens
}

// summarizeTranscript 用指定账号 + 摘要模型对一段会话转写发起一次非流式（内部 drain）
// Codex responses 调用，返回摘要正文。
func (h *Handler) summarizeTranscript(ctx context.Context, account *auth.Account, model, transcript string) (string, error) {
	if account == nil {
		return "", errors.New("no account for summarization")
	}
	if strings.TrimSpace(model) == "" {
		model = defaultContextSummaryModel
	}

	body := map[string]any{
		"model":        model,
		"stream":       true,
		"store":        false,
		"instructions": summarizerInstructions,
		"reasoning":    map[string]any{"effort": "low"},
		"input": []any{
			map[string]any{
				"type": "message",
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_text", "text": summarizeUserPrompt + transcript},
				},
			},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	callCtx, cancel := context.WithTimeout(ctx, summarizeTimeout)
	defer cancel()

	deviceCfg := h.deviceCfg
	if deviceCfg == nil {
		deviceCfg = &DeviceProfileConfig{StabilizeDeviceProfile: false}
	}

	resp, err := ExecuteRequest(callCtx, account, raw, "", "", "", deviceCfg, http.Header{})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New("summarizer upstream status " + resp.Status)
	}

	var sb strings.Builder
	failed := false
	readErr := ReadSSEStream(resp.Body, func(data []byte) bool {
		parsed := gjson.ParseBytes(data)
		switch parsed.Get("type").String() {
		case "response.output_text.delta":
			sb.WriteString(parsed.Get("delta").String())
		case "response.completed":
			return false
		case "response.failed":
			failed = true
			return false
		}
		return true
	})
	if readErr != nil {
		return "", readErr
	}
	if failed {
		return "", errors.New("summarizer upstream returned response.failed")
	}
	return sb.String(), nil
}

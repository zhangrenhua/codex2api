package proxy

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// 算法 1M：上下文窗口管理。
//
// 目标：让上游不支持 1M 上下文的 gpt 模型也能接收超长上下文。一句话：
//
//	先把最旧的 ≤阈值 内容做一次摘要，摘要 + 剩余较新内容若仍超阈值，
//	就结构化截断剩余部分（丢最旧、保 user 边界、必要时截单条），强制 ≤ 阈值；
//	截断时对下游仍上报原始 input_tokens。
//
// 本文件只包含纯逻辑（不发网络请求、不读运行时配置），便于单测。摘要的真实
// LLM 调用与配置开关在 summarize.go / handler 中完成。

const (
	// summaryItemPrefix 与 normalizeResponsesCompactionItems 中的提示前缀保持一致，
	// 让摘要在下游/上游看起来与客户端 compaction 摘要同源。
	summaryItemPrefix = "[Conversation summary from earlier turns]\n"
	// truncationMarker 标记被结构化截断的单条消息正文。
	truncationMarker = " …[truncated by 1M context manager]"
	// perItemTokenOverhead 每个 input 项的结构性 token 估算开销。
	perItemTokenOverhead = 4
	// minKeptStringBytes 截断单条正文时保留的最小字节数。
	minKeptStringBytes = 16
)

// SummarizeFunc 把一段（最旧的）会话转写文本摘要成一段较短文本。
type SummarizeFunc func(ctx context.Context, transcript string) (string, error)

// ContextWindowResult 描述一次上下文窗口管理的结果。
type ContextWindowResult struct {
	Applied bool   // 是否对请求体做了压缩/截断
	Body    []byte // 处理后的请求体（Applied 为 false 时与入参一致）
	// OriginalInputTokens 为压缩/截断前对 input 的原始 token 估算，
	// 用于对下游上报与计费（即客户端按完整上下文计费）。
	OriginalInputTokens int
}

// EstimateResponsesInputTokens 估算 Codex responses 请求体中 input 的 token 数。
// 解析失败或没有 input 时返回 0。
func EstimateResponsesInputTokens(body []byte) int {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return 0
	}
	items, ok := m["input"].([]any)
	if !ok {
		return 0
	}
	return estimateItemsTokens(items)
}

// ApplyContextWindow 对 Codex responses 请求体执行算法 1M。
// threshold 为强制上限（同时是长上下文计费阈值）；summarize 为 nil 时跳过摘要，
// 直接走结构化截断。返回的 Result.Body 在 Applied 为 false 时即原始 body。
func ApplyContextWindow(ctx context.Context, body []byte, threshold int, summarize SummarizeFunc) ContextWindowResult {
	noop := ContextWindowResult{Applied: false, Body: body}
	if threshold <= 0 {
		return noop
	}

	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return noop
	}
	items, ok := m["input"].([]any)
	if !ok || len(items) == 0 {
		return noop
	}

	orig := estimateItemsTokens(items)
	if orig <= threshold {
		return noop
	}

	// 1. 先对最旧的 ≤阈值 内容做一次摘要。
	newItems := items
	if summarize != nil {
		if summarized, ok := compressViaSummary(ctx, items, threshold, summarize); ok {
			newItems = summarized
		}
	}

	// 2. 摘要 + 剩余较新内容若仍超阈值，结构化截断，强制 ≤ 阈值。
	if estimateItemsTokens(newItems) > threshold {
		newItems = structuredTruncate(newItems, threshold)
	}

	m["input"] = newItems
	nb, err := json.Marshal(m)
	if err != nil {
		// 序列化失败：放弃改写，但仍上报原始 token（计费用），由调用方决定。
		return ContextWindowResult{Applied: false, Body: body, OriginalInputTokens: orig}
	}
	return ContextWindowResult{Applied: true, Body: nb, OriginalInputTokens: orig}
}

// compressViaSummary 取最旧的、累计 ≤阈值 的一段（对齐到 user 边界）做摘要，
// 用一条 developer 摘要消息替换它，保留较新部分。前置的 developer/system 指令
// （系统提示）被原样固定保留，不进入摘要。成功时返回新 items 与 true。
func compressViaSummary(ctx context.Context, items []any, threshold int, summarize SummarizeFunc) ([]any, bool) {
	headEnd := leadingDeveloperCount(items)
	split := splitOldestForSummary(items, headEnd, threshold)
	if split <= headEnd || split >= len(items) {
		return items, false
	}
	head := items[:headEnd]
	oldest := items[headEnd:split]
	remainder := items[split:]

	transcript := itemsToTranscript(oldest)
	if strings.TrimSpace(transcript) == "" {
		return items, false
	}
	summary, err := summarize(ctx, transcript)
	if err != nil || strings.TrimSpace(summary) == "" {
		return items, false
	}

	out := make([]any, 0, len(head)+len(remainder)+1)
	out = append(out, head...)
	out = append(out, buildSummaryItem(summary))
	out = append(out, remainder...)
	return out, true
}

// splitOldestForSummary 返回切分点 split：待摘要段 items[start:split] 是从 start 开始、
// 累计 token ≤阈值 且尽量大的一段，且 items[split] 落在 user 消息边界上
// （remainder 以一个完整 user 轮次开头）。无法干净切分时返回 0。
func splitOldestForSummary(items []any, start, threshold int) int {
	best := 0
	cum := 0
	for i := start; i < len(items); i++ {
		// 只在 user 边界处记录候选切分点（i>start 才有意义）。
		if i > start && isUserMessage(items[i]) && cum <= threshold {
			best = i
		}
		cum += estimateItemTokens(items[i])
	}
	return best
}

// structuredTruncate 强制 items 的估算 token ≤ 阈值：
// 丢最旧（按 user 边界整轮丢弃），保留前置 developer/system（含摘要）与最近若干轮，
// 必要时截断单条消息正文。
func structuredTruncate(items []any, threshold int) []any {
	if estimateItemsTokens(items) <= threshold {
		return items
	}

	headEnd := leadingDeveloperCount(items)
	head := items[:headEnd]
	body := items[headEnd:]
	starts := turnStarts(body)

	// 从最旧开始整轮丢弃，保留尽量大的、能放下的后缀。
	for i := 0; i < len(starts); i++ {
		candidate := joinItems(head, body[starts[i]:])
		if estimateItemsTokens(candidate) <= threshold {
			return candidate
		}
	}

	// 连"只留最后一轮"都放不下：保留最后一轮，再截断单条正文。
	lastStart := 0
	if len(starts) > 0 {
		lastStart = starts[len(starts)-1]
	}
	result := joinItems(head, body[lastStart:])
	return truncateItemsTextToFit(result, threshold)
}

// truncateItemsTextToFit 通过截断最长的字符串正文，把 items 压到 ≤阈值。
func truncateItemsTextToFit(items []any, threshold int) []any {
	const guardLimit = 100000
	prev := -1
	for guard := 0; guard < guardLimit; guard++ {
		cur := estimateItemsTokens(items)
		if cur <= threshold {
			break
		}
		if cur == prev {
			break // 无法继续缩小（剩余都是结构性开销），避免死循环
		}
		prev = cur

		var bestStr string
		var bestSet func(string)
		for _, it := range items {
			if s, set := findLongestString(it); set != nil && len(s) > len(bestStr) {
				bestStr = s
				bestSet = set
			}
		}
		if bestSet == nil || len(bestStr) == 0 {
			break
		}

		overBytes := (cur-threshold)*4 + len(truncationMarker) + 8
		keep := len(bestStr) - overBytes
		if keep < minKeptStringBytes {
			keep = minKeptStringBytes
		}
		if keep >= len(bestStr) {
			// 最长串也已很短：直接替换为标记以推进
			bestSet(truncationMarker)
			continue
		}
		bestSet(cutStringToBytes(bestStr, keep) + truncationMarker)
	}
	return items
}

// ==================== 估算 ====================

// estimateItemsTokens 估算一组 input 项的 token 数。
func estimateItemsTokens(items []any) int {
	total := 0
	for _, it := range items {
		total += estimateItemTokens(it)
	}
	return total
}

// estimateItemTokens 估算单个 input 项的 token 数：所有字符串叶子字节数 / 4 +
// 每项结构开销。这是与 tiktoken 无关的启发式，仅用于阈值判定与原始量上报。
func estimateItemTokens(item any) int {
	return sumStringBytes(item)/4 + perItemTokenOverhead
}

// sumStringBytes 递归累计结构中所有字符串叶子的字节数。
func sumStringBytes(v any) int {
	switch c := v.(type) {
	case string:
		return len(c)
	case map[string]any:
		n := 0
		for _, val := range c {
			n += sumStringBytes(val)
		}
		return n
	case []any:
		n := 0
		for _, val := range c {
			n += sumStringBytes(val)
		}
		return n
	default:
		return 0
	}
}

// ==================== 项 / 边界辅助 ====================

func itemTypeRole(item any) (typ, role string) {
	m, ok := item.(map[string]any)
	if !ok {
		return "", ""
	}
	typ, _ = m["type"].(string)
	role, _ = m["role"].(string)
	return typ, role
}

func isUserMessage(item any) bool {
	t, r := itemTypeRole(item)
	return t == "message" && r == "user"
}

func isDeveloperOrSystem(item any) bool {
	t, r := itemTypeRole(item)
	return t == "message" && (r == "developer" || r == "system")
}

// leadingDeveloperCount 返回开头连续的 developer/system 消息数量（指令 + 摘要），
// 这些项在截断时被固定保留。
func leadingDeveloperCount(items []any) int {
	n := 0
	for _, it := range items {
		if isDeveloperOrSystem(it) {
			n++
			continue
		}
		break
	}
	return n
}

// turnStarts 返回 body 中各轮次的起始下标（user 消息处）。若 body 非空且不以
// user 消息开头，则把下标 0 也视为一个起点，避免丢失开头的非 user 项。
func turnStarts(body []any) []int {
	var starts []int
	for i := range body {
		if isUserMessage(body[i]) {
			starts = append(starts, i)
		}
	}
	if len(body) > 0 && (len(starts) == 0 || starts[0] != 0) {
		starts = append([]int{0}, starts...)
	}
	return starts
}

func joinItems(head, tail []any) []any {
	out := make([]any, 0, len(head)+len(tail))
	out = append(out, head...)
	out = append(out, tail...)
	return out
}

func buildSummaryItem(summary string) map[string]any {
	return map[string]any{
		"type": "message",
		"role": "developer",
		"content": []any{
			map[string]any{"type": "input_text", "text": summaryItemPrefix + strings.TrimSpace(summary)},
		},
	}
}

// ==================== 转写（用于摘要输入） ====================

// itemsToTranscript 把一组 input 项转为可读的纯文本转写，供摘要模型阅读。
func itemsToTranscript(items []any) string {
	var b strings.Builder
	for _, it := range items {
		typ, role := itemTypeRole(it)
		switch typ {
		case "message":
			txt := messageText(it)
			if strings.TrimSpace(txt) == "" {
				continue
			}
			label := strings.ToUpper(role)
			if label == "" {
				label = "MESSAGE"
			}
			b.WriteString(label)
			b.WriteString(": ")
			b.WriteString(txt)
			b.WriteString("\n\n")
		case "function_call":
			m, _ := it.(map[string]any)
			name, _ := m["name"].(string)
			args, _ := m["arguments"].(string)
			b.WriteString("TOOL_CALL ")
			b.WriteString(name)
			b.WriteString(": ")
			b.WriteString(args)
			b.WriteString("\n\n")
		case "function_call_output":
			m, _ := it.(map[string]any)
			out, _ := m["output"].(string)
			b.WriteString("TOOL_RESULT: ")
			b.WriteString(out)
			b.WriteString("\n\n")
		}
	}
	return b.String()
}

// messageText 提取一条 message 项的全部文本（拼接 content 各文本块）。
func messageText(item any) string {
	m, ok := item.(map[string]any)
	if !ok {
		return ""
	}
	switch content := m["content"].(type) {
	case string:
		return content
	case []any:
		var b strings.Builder
		for _, part := range content {
			pm, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if txt, ok := pm["text"].(string); ok && txt != "" {
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				b.WriteString(txt)
			}
		}
		return b.String()
	}
	return ""
}

// ==================== 字符串截断辅助 ====================

// truncatableKeys 列出可安全截断的自由文本字段。只有直接挂在这些键下的字符串
// 才会被截断；结构性字段（type/role/name/id/call_id）与完整性敏感字段
// （encrypted_content 加密 reasoning、image_url 图片 data URL）一律不动——
// 这类内容过大时应整轮丢弃而非截断，截断会破坏数据导致上游报错。
var truncatableKeys = map[string]bool{
	"text":      true, // message content part / reasoning summary
	"output":    true, // function_call_output
	"arguments": true, // function_call
	"content":   true, // message content（字符串形态）
}

// findLongestString 在结构中找到最长的、可安全截断的自由文本字符串叶子，
// 返回其值与一个就地替换它的闭包。只挑选 key ∈ truncatableKeys 的字符串，
// 但会递归进入所有子结构以便找到深层的可截断文本。
func findLongestString(v any) (string, func(string)) {
	var best string
	var bestSet func(string)
	switch c := v.(type) {
	case map[string]any:
		for k := range c {
			key := k
			if s, ok := c[key].(string); ok {
				if truncatableKeys[key] && len(s) > len(best) {
					best = s
					bestSet = func(ns string) { c[key] = ns }
				}
				continue
			}
			if s, set := findLongestString(c[key]); set != nil && len(s) > len(best) {
				best = s
				bestSet = set
			}
		}
	case []any:
		for i := range c {
			// 数组里的裸字符串没有键上下文，不截断；仅递归进入容器元素。
			if _, ok := c[i].(string); ok {
				continue
			}
			if s, set := findLongestString(c[i]); set != nil && len(s) > len(best) {
				best = s
				bestSet = set
			}
		}
	}
	return best, bestSet
}

// cutStringToBytes 把字符串截到不超过 maxBytes 字节，且不切碎 UTF-8 字符。
func cutStringToBytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}

// ==================== 下游 usage 改写 ====================

// applyOriginalInputTokens 把 usage 的 input/prompt token 覆盖为原始（截断前）估算值，
// 并同步 total。用于非流式响应与计费/usage_logs 上报原始量。
func (u *UsageInfo) applyOriginalInputTokens(originalInputTokens int) {
	if u == nil || originalInputTokens <= 0 {
		return
	}
	u.InputTokens = originalInputTokens
	u.PromptTokens = originalInputTokens
	u.TotalTokens = originalInputTokens + u.OutputTokens
}

// copyResponsesInput 把 src 请求体里的 input 数组复制到 dst（用于让算法 1M 压缩后的
// input 同步到原生 OpenAI Responses 请求体）。任一侧无 input 时返回 dst 原样。
func copyResponsesInput(dst, src []byte) []byte {
	input := gjson.GetBytes(src, "input")
	if !input.Exists() {
		return dst
	}
	out, err := sjson.SetRawBytes(dst, "input", []byte(input.Raw))
	if err != nil {
		return dst
	}
	return out
}

// rewriteUsageInputTokensAt 把 base+".input_tokens" 改写为原始（截断前）估算值，
// 并同步 base+".total_tokens"。base 为 usage 对象在 JSON 中的 gjson 路径前缀。
func rewriteUsageInputTokensAt(data []byte, base string, originalInputTokens int) []byte {
	if originalInputTokens <= 0 {
		return data
	}
	if !gjson.GetBytes(data, base).Exists() {
		return data
	}
	out, err := sjson.SetBytes(data, base+".input_tokens", originalInputTokens)
	if err != nil {
		return data
	}
	outputTokens := int(gjson.GetBytes(out, base+".output_tokens").Int())
	if next, err := sjson.SetBytes(out, base+".total_tokens", originalInputTokens+outputTokens); err == nil {
		out = next
	}
	return out
}

// rewriteCompletedUsageInputTokens 改写 response.completed 事件（含 response 包裹）里的 usage。
// 用于流式透传时对下游上报原始量。
func rewriteCompletedUsageInputTokens(data []byte, originalInputTokens int) []byte {
	return rewriteUsageInputTokensAt(data, "response.usage", originalInputTokens)
}

// rewriteResponseObjectUsageInputTokens 改写裸 response 对象（非流式 /v1/responses 响应体）里的 usage。
func rewriteResponseObjectUsageInputTokens(responseJSON []byte, originalInputTokens int) []byte {
	return rewriteUsageInputTokensAt(responseJSON, "usage", originalInputTokens)
}

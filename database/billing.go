package database

// ModelPricing 模型价格配置（每百万 token 的价格，单位：美元）
type ModelPricing struct {
	InputPricePerMToken  float64 // 输入价格（美元/百万token）
	OutputPricePerMToken float64 // 输出价格（美元/百万token）
}

// getModelPricing 获取模型价格配置
// 价格参考 OpenAI 官方定价：https://openai.com/api/pricing/
func getModelPricing(model string) *ModelPricing {
	// 硬编码价格表（简化版，仅包含常用模型）
	pricingTable := map[string]*ModelPricing{
		// GPT-4 系列
		"gpt-4":             {InputPricePerMToken: 30.0, OutputPricePerMToken: 60.0},
		"gpt-4-turbo":       {InputPricePerMToken: 10.0, OutputPricePerMToken: 30.0},
		"gpt-4o":            {InputPricePerMToken: 2.5, OutputPricePerMToken: 10.0},
		"gpt-4o-mini":       {InputPricePerMToken: 0.15, OutputPricePerMToken: 0.6},

		// GPT-3.5 系列
		"gpt-3.5-turbo":     {InputPricePerMToken: 0.5, OutputPricePerMToken: 1.5},

		// Claude 系列（Anthropic 定价）
		"claude-opus-4":     {InputPricePerMToken: 15.0, OutputPricePerMToken: 75.0},
		"claude-sonnet-4":   {InputPricePerMToken: 3.0, OutputPricePerMToken: 15.0},
		"claude-haiku-4":    {InputPricePerMToken: 0.25, OutputPricePerMToken: 1.25},

		// 默认价格（未知模型）
		"default":           {InputPricePerMToken: 1.0, OutputPricePerMToken: 2.0},
	}

	// 查找模型价格
	if pricing, ok := pricingTable[model]; ok {
		return pricing
	}

	// 模糊匹配（处理带版本号的模型名）
	for key, pricing := range pricingTable {
		if len(model) > len(key) && model[:len(key)] == key {
			return pricing
		}
	}

	// 返回默认价格
	return pricingTable["default"]
}

// calculateCost 计算使用费用
// inputTokens: 输入 token 数量
// outputTokens: 输出 token 数量
// model: 模型名称
// 返回：账号计费金额（美元）
func calculateCost(inputTokens, outputTokens int, model string) float64 {
	pricing := getModelPricing(model)

	// 计算费用（token 数量 / 1,000,000 * 单价）
	inputCost := float64(inputTokens) / 1000000.0 * pricing.InputPricePerMToken
	outputCost := float64(outputTokens) / 1000000.0 * pricing.OutputPricePerMToken

	return inputCost + outputCost
}

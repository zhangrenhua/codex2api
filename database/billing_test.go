package database

import (
	"math"
	"testing"
)

func TestGetModelPricingUsesMostSpecificOpenAIPrefix(t *testing.T) {
	tests := []struct {
		model      string
		wantInput  float64
		wantOutput float64
	}{
		{model: "gpt-4o-mini-2024-07-18", wantInput: 0.15, wantOutput: 0.6},
		{model: "gpt-4o-2024-08-06", wantInput: 2.5, wantOutput: 10.0},
		{model: "gpt-4-0613", wantInput: 30.0, wantOutput: 60.0},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := getModelPricing(tt.model)
			assertPricing(t, got, tt.wantInput, tt.wantOutput)
		})
	}
}

func TestGetModelPricingUsesSub2APICodexFallbacks(t *testing.T) {
	tests := []struct {
		model      string
		wantInput  float64
		wantOutput float64
	}{
		{model: "gpt-5.4-mini-20260401", wantInput: 0.75, wantOutput: 4.5},
		{model: "gpt-5.3-codex-spark", wantInput: 1.5, wantOutput: 12.0},
		{model: "gpt-5.5", wantInput: 2.5, wantOutput: 15.0},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := getModelPricing(tt.model)
			assertPricing(t, got, tt.wantInput, tt.wantOutput)
		})
	}
}

func TestGetModelPricingUsesSub2APIClaudeFamilies(t *testing.T) {
	tests := []struct {
		model      string
		wantInput  float64
		wantOutput float64
	}{
		{model: "claude-opus-4-7-20260401", wantInput: 5.0, wantOutput: 25.0},
		{model: "claude-opus-4-20250514", wantInput: 15.0, wantOutput: 75.0},
		{model: "claude-sonnet-4-5-20250929", wantInput: 3.0, wantOutput: 15.0},
		{model: "claude-3-5-haiku-20241022", wantInput: 1.0, wantOutput: 5.0},
		{model: "claude-unknown-model", wantInput: 3.0, wantOutput: 15.0},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := getModelPricing(tt.model)
			assertPricing(t, got, tt.wantInput, tt.wantOutput)
		})
	}
}

func TestCalculateCostHandlesCachedTokensAndServiceTier(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		serviceTier  string
		inputTokens  int
		outputTokens int
		cachedTokens int
		want         float64
	}{
		{
			name:         "discounts cached tokens when cache pricing exists",
			model:        "gpt-5.4",
			inputTokens:  1000,
			outputTokens: 500,
			cachedTokens: 200,
			want:         0.00955,
		},
		{
			name:         "keeps legacy input price when cache pricing is unavailable",
			model:        "gpt-4o",
			inputTokens:  1000,
			outputTokens: 500,
			cachedTokens: 200,
			want:         0.0075,
		},
		{
			name:         "uses priority prices when available",
			model:        "gpt-5.4",
			serviceTier:  "priority",
			inputTokens:  1000,
			outputTokens: 500,
			cachedTokens: 200,
			want:         0.0191,
		},
		{
			name:         "applies flex multiplier",
			model:        "gpt-5.4",
			serviceTier:  "flex",
			inputTokens:  1000,
			outputTokens: 500,
			cachedTokens: 200,
			want:         0.004775,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateCost(tt.inputTokens, tt.outputTokens, tt.cachedTokens, tt.model, tt.serviceTier)
			if math.Abs(got-tt.want) > 1e-12 {
				t.Fatalf("calculateCost() = %.12f, want %.12f", got, tt.want)
			}
		})
	}
}

func assertPricing(t *testing.T, got *ModelPricing, wantInput, wantOutput float64) {
	t.Helper()
	if got == nil {
		t.Fatal("getModelPricing returned nil")
	}
	if math.Abs(got.InputPricePerMToken-wantInput) > 1e-12 || math.Abs(got.OutputPricePerMToken-wantOutput) > 1e-12 {
		t.Fatalf("pricing = input %.12f output %.12f, want input %.12f output %.12f",
			got.InputPricePerMToken, got.OutputPricePerMToken, wantInput, wantOutput)
	}
}

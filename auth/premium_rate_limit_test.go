package auth

import (
	"context"
	"testing"
	"time"
)

func newPremium5hTestAccount(plan string, resetAt time.Time) *Account {
	return &Account{
		DBID:                1,
		AccessToken:         "token",
		PlanType:            plan,
		Status:              StatusReady,
		HealthTier:          HealthTierHealthy,
		UsagePercent5h:      100,
		UsagePercent5hValid: true,
		Reset5hAt:           resetAt,
		UsageUpdatedAt:      time.Now().Add(-20 * time.Minute),
	}
}

func TestPremium5hRateLimitedAccountNotSchedulable(t *testing.T) {
	acc := newPremium5hTestAccount("plus", time.Now().Add(45*time.Minute))

	if got := acc.RuntimeStatus(); got != "rate_limited" {
		t.Fatalf("RuntimeStatus() = %q, want rate_limited", got)
	}
	if acc.IsAvailable() {
		t.Fatal("IsAvailable() = true, want false during premium 5h rate limit window")
	}
}

func TestPremium5hRateLimitExpiresAndUsageProbeResumes(t *testing.T) {
	// guard buffer 最大 89s，用 -3min 保证无论 DBID 取值都已过 guard
	resetAt := time.Now().Add(-3 * time.Minute)
	acc := newPremium5hTestAccount("team", resetAt)
	// 模拟 usage probe 已在 reset 后刷新过用量（仍是 100，但 UsageUpdatedAt 是新的）
	acc.UsageUpdatedAt = time.Now()

	snapshot := acc.GetSchedulerDebugSnapshot(4)
	if got := acc.RuntimeStatus(); got != "active" {
		t.Fatalf("RuntimeStatus() = %q, want active after reset expires", got)
	}
	if !acc.IsAvailable() {
		t.Fatal("IsAvailable() = false, want true after reset expires and usage confirmed")
	}
	if snapshot.HealthTier != string(HealthTierHealthy) {
		t.Fatalf("HealthTier = %q, want %q", snapshot.HealthTier, HealthTierHealthy)
	}
	if snapshot.DynamicConcurrencyLimit != 4 {
		t.Fatalf("DynamicConcurrencyLimit = %d, want 4", snapshot.DynamicConcurrencyLimit)
	}
}

func TestPremium5hRateLimitExpiresButProbePendingStaysBlocked(t *testing.T) {
	// 窗口已过，但 UsageUpdatedAt 仍是 20 分钟前（probe 还没跑）→ 继续拦住
	resetAt := time.Now().Add(-3 * time.Minute)
	acc := newPremium5hTestAccount("plus", resetAt)
	acc.UsageUpdatedAt = resetAt.Add(-17 * time.Minute)

	if acc.IsAvailable() {
		t.Fatal("IsAvailable() = true, want false until a usage probe refreshes UsageUpdatedAt")
	}
	// probe 必须能运行（premium5hRateLimitedLocked 已返回 false，NeedsUsageProbe 不会跳过）
	if !acc.NeedsUsageProbe(10 * time.Minute) {
		t.Fatal("NeedsUsageProbe() = false, want true — probe must run to unlock post-window account")
	}
}

func TestPremium5hRateLimitGuardBufferBlocksRecoveryAtBoundary(t *testing.T) {
	// Reset5hAt 刚刚过期 1 秒，guard buffer 会继续拦住账号，避免时钟漂移/雷群
	acc := newPremium5hTestAccount("plus", time.Now().Add(-time.Second))

	if acc.IsAvailable() {
		t.Fatal("IsAvailable() = true at reset boundary, want false due to guard buffer")
	}
}

func TestRateLimitResetGuardJitterDeterministic(t *testing.T) {
	// guard = 30s 基线 + DBID%60 秒 jitter；不同账号应错开
	if got := rateLimitResetGuard(0); got != 30*time.Second {
		t.Fatalf("rateLimitResetGuard(0) = %v, want 30s", got)
	}
	if got := rateLimitResetGuard(10); got != 40*time.Second {
		t.Fatalf("rateLimitResetGuard(10) = %v, want 40s", got)
	}
	if got := rateLimitResetGuard(60); got != 30*time.Second {
		t.Fatalf("rateLimitResetGuard(60) = %v, want 30s (wrap-around)", got)
	}
	if rateLimitResetGuard(5) == rateLimitResetGuard(6) {
		t.Fatal("adjacent DBIDs should produce different guard values")
	}
}

func TestPremium5hRateLimitedSkipsUsageProbeBeforeReset(t *testing.T) {
	acc := newPremium5hTestAccount("pro", time.Now().Add(30*time.Minute))

	if acc.NeedsUsageProbe(10 * time.Minute) {
		t.Fatal("NeedsUsageProbe() = true, want false before premium 5h reset time")
	}
}

func TestCleanByRuntimeStatusSkipsPremium5hRateLimitedAccount(t *testing.T) {
	acc := newPremium5hTestAccount("plus", time.Now().Add(20*time.Minute))
	store := &Store{
		accounts: []*Account{acc},
	}

	if cleaned := store.CleanByRuntimeStatus(context.Background(), "rate_limited"); cleaned != 0 {
		t.Fatalf("CleanByRuntimeStatus() cleaned = %d, want 0", cleaned)
	}
	if store.AccountCount() != 1 {
		t.Fatalf("AccountCount() = %d, want 1", store.AccountCount())
	}
}

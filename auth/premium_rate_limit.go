package auth

import (
	"context"
	"log"
	"strings"
	"sync/atomic"
	"time"
)

const premium5hFallbackWindow = 5 * time.Hour

// rateLimitResetGuard 返回一个基于 DBID 的确定性 guard buffer，用于延后 Reset5hAt/Reset7dAt
// 的生效判定，兼顾时钟漂移与雷群效应：基础 30s，外加 0~59s jitter，不同账号自然错开恢复时刻。
func rateLimitResetGuard(dbID int64) time.Duration {
	const baseGuard = 30 * time.Second
	const jitterRange = 60 * time.Second
	if dbID < 0 {
		dbID = -dbID
	}
	jitter := time.Duration(dbID%int64(jitterRange/time.Second)) * time.Second
	return baseGuard + jitter
}

func normalizePlanType(plan string) string {
	return strings.ToLower(strings.TrimSpace(plan))
}

func isPremium5hPlan(plan string) bool {
	switch normalizePlanType(plan) {
	case "plus", "pro", "team":
		return true
	default:
		return false
	}
}

// IsPremium5hPlan 判断当前账号是否属于 premium 5h 限流语义范围。
func (a *Account) IsPremium5hPlan() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return isPremium5hPlan(a.PlanType)
}

// premium5hRateLimitedLocked 仅表示账号仍位于 premium 5h 限流窗口内（含 guard buffer）。
// 用于状态展示、probe 跳过、cooldown 抑制、health tier 降级等"是否仍受限"语义。
func (a *Account) premium5hRateLimitedLocked(now time.Time) bool {
	if !isPremium5hPlan(a.PlanType) {
		return false
	}
	if !a.UsagePercent5hValid || a.UsagePercent5h < 100 {
		return false
	}
	if a.Reset5hAt.IsZero() {
		return false
	}
	return a.Reset5hAt.Add(rateLimitResetGuard(a.DBID)).After(now)
}

// premium5hBlocksSchedulingLocked 判断 5h 限流是否应阻止调度：
//  1. 仍在窗口内（含 guard buffer） → 阻止
//  2. 窗口已过，但 usage probe 尚未刷新过用量（UsageUpdatedAt <= Reset5hAt） → 继续阻止，
//     直到一次成功的 probe 返回新的用量头部，确认账号真正恢复后再放开调度。
//
// 与 premium5hRateLimitedLocked 的区别仅在第 2 点；probe/状态显示等场景仍使用前者，
// 避免 probe 自身被"限流中"误判而无法执行导致死锁。
func (a *Account) premium5hBlocksSchedulingLocked(now time.Time) bool {
	if a.premium5hRateLimitedLocked(now) {
		return true
	}
	if !isPremium5hPlan(a.PlanType) || !a.UsagePercent5hValid || a.UsagePercent5h < 100 {
		return false
	}
	if a.Reset5hAt.IsZero() {
		return false
	}
	return !a.UsageUpdatedAt.After(a.Reset5hAt)
}

func (a *Account) premium5hRateLimitWindowLocked(now time.Time) (bool, time.Time) {
	if !a.premium5hRateLimitedLocked(now) {
		return false, time.Time{}
	}
	return true, a.Reset5hAt
}

func (a *Account) premium5hCooldownSuppressedLocked(now time.Time) bool {
	if a.Status != StatusCooldown || a.CooldownReason != "rate_limited" {
		return false
	}
	active, _ := a.premium5hRateLimitWindowLocked(now)
	return active
}

// IsPremium5hRateLimited 判断账号当前是否处于 premium 5h 限流态。
func (a *Account) IsPremium5hRateLimited() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.premium5hRateLimitedLocked(time.Now())
}

// GetUsageSnapshot5h 返回当前 5h 用量快照。
func (a *Account) GetUsageSnapshot5h() (pct float64, resetAt time.Time, ok bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.UsagePercent5hValid {
		return 0, time.Time{}, false
	}
	return a.UsagePercent5h, a.Reset5hAt, true
}

// PersistUsageSnapshot5hOnly 持久化仅包含 5h 数据的用量快照。
func (s *Store) PersistUsageSnapshot5hOnly(acc *Account) {
	if acc == nil || s == nil || s.db == nil {
		return
	}

	pct5h, reset5hAt, ok := acc.GetUsageSnapshot5h()
	if !ok {
		return
	}

	updatedAt := time.Now()
	acc.mu.Lock()
	acc.UsageUpdatedAt = updatedAt
	acc.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.db.UpdateUsageSnapshot5h(ctx, acc.DBID, pct5h, reset5hAt, updatedAt); err != nil {
		log.Printf("[账号 %d] 持久化 5h 用量快照失败: %v", acc.DBID, err)
	}
}

// MarkPremium5hRateLimited 将账号标记为 premium 5h 限流态，并按 resetAt 驱动恢复。
func (s *Store) MarkPremium5hRateLimited(acc *Account, resetAt time.Time) {
	if acc == nil || s == nil {
		return
	}

	now := time.Now()
	if resetAt.IsZero() || !resetAt.After(now) {
		resetAt = now.Add(premium5hFallbackWindow)
	}

	acc.mu.Lock()
	acc.UsagePercent5h = 100
	acc.UsagePercent5hValid = true
	acc.Reset5hAt = resetAt
	acc.UsageUpdatedAt = now
	acc.LastRateLimitedAt = now
	if acc.Status == StatusCooldown && acc.CooldownReason == "rate_limited" {
		acc.Status = StatusReady
		acc.CooldownUtil = time.Time{}
		acc.CooldownReason = ""
	}
	if acc.HealthTier != HealthTierBanned {
		acc.HealthTier = HealthTierRisky
	}
	acc.recomputeSchedulerLocked(atomic.LoadInt64(&s.maxConcurrency))
	acc.mu.Unlock()

	s.fastSchedulerUpdate(acc)

	if s.db == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.db.ClearCooldown(ctx, acc.DBID); err != nil {
		log.Printf("[账号 %d] 清理 premium 5h 限流冷却状态失败: %v", acc.DBID, err)
	}
	if err := s.db.UpdateUsageSnapshot5h(ctx, acc.DBID, 100, resetAt, now); err != nil {
		log.Printf("[账号 %d] 持久化 premium 5h 限流快照失败: %v", acc.DBID, err)
	}
}

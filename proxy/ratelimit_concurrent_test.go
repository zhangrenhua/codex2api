package proxy

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestAccountRPM_ConcurrentPerAccount 验证并发场景下，每个账号的 RPM 独立计数。
// 设 RPM=20，开 100 个 goroutine 集中打同一账号，应该正好 20 个通过。
func TestAccountRPM_ConcurrentPerAccount(t *testing.T) {
	erl := NewEnhancedRateLimiter(nil, 0, 20, 0)

	const goroutines = 100
	var allowed, denied int64
	var wg sync.WaitGroup
	wg.Add(goroutines)

	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			<-start
			if erl.AllowAccountModel("acc-1", "") {
				atomic.AddInt64(&allowed, 1)
			} else {
				atomic.AddInt64(&denied, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if allowed != 20 {
		t.Errorf("expected exactly 20 allowed (burst capacity), got %d (denied=%d)", allowed, denied)
	}
	if denied != 80 {
		t.Errorf("expected 80 denied, got %d", denied)
	}
}

// TestAccountRPM_IndependentBuckets 验证多个账号的 RPM 桶相互独立，不互相消耗。
func TestAccountRPM_IndependentBuckets(t *testing.T) {
	erl := NewEnhancedRateLimiter(nil, 0, 5, 0)

	const accountCount = 10
	const perAccountReq = 5
	var allowed int64
	var wg sync.WaitGroup
	wg.Add(accountCount * perAccountReq)

	start := make(chan struct{})
	for a := 0; a < accountCount; a++ {
		accID := fmt.Sprintf("acc-%d", a)
		for i := 0; i < perAccountReq; i++ {
			go func(id string) {
				defer wg.Done()
				<-start
				if erl.AllowAccountModel(id, "") {
					atomic.AddInt64(&allowed, 1)
				}
			}(accID)
		}
	}
	close(start)
	wg.Wait()

	expected := int64(accountCount * perAccountReq) // 每账号 5 个请求 = 5 个桶容量，都该通过
	if allowed != expected {
		t.Errorf("expected %d allowed (independent buckets), got %d", expected, allowed)
	}
}

// TestModelRPM_AggregateAcrossAccounts 验证 ModelRPM 跨账号聚合：
// 即使来自不同账号的请求，只要 model 一致就共享同一个桶。
func TestModelRPM_AggregateAcrossAccounts(t *testing.T) {
	erl := NewEnhancedRateLimiter(nil, 0, 0, 30)

	const accountCount = 50
	const reqPerAccount = 2 // 总共 100 个请求，但 model RPM 只让 30 个通过
	var allowed int64
	var wg sync.WaitGroup
	wg.Add(accountCount * reqPerAccount)

	start := make(chan struct{})
	for a := 0; a < accountCount; a++ {
		accID := fmt.Sprintf("acc-%d", a)
		for i := 0; i < reqPerAccount; i++ {
			go func(id string) {
				defer wg.Done()
				<-start
				if erl.AllowAccountModel(id, "gpt-5.4") {
					atomic.AddInt64(&allowed, 1)
				}
			}(accID)
		}
	}
	close(start)
	wg.Wait()

	if allowed != 30 {
		t.Errorf("expected exactly 30 allowed (model bucket aggregates across accounts), got %d", allowed)
	}
}

// TestAccountAndModelRPM_LimitKind 验证拒绝 kind 的语义：
// - 当 ModelRPM 用尽，应返回 Model
// - 当 AccountRPM 用尽（Model 还有余量），应返回 Account
func TestAccountAndModelRPM_LimitKind(t *testing.T) {
	// model rpm = 100 (充裕), account rpm = 3 (易耗尽)
	erl := NewEnhancedRateLimiter(nil, 0, 3, 100)

	// 前 3 次同账号，预期 None
	for i := 0; i < 3; i++ {
		if got := erl.AllowAccountModelKind("acc-1", "gpt-5.4"); got != AccountModelLimitNone {
			t.Errorf("req %d: expected None, got %d", i, got)
		}
	}
	// 第 4 次同账号，account 桶空，但 model 还有；预期 Account
	if got := erl.AllowAccountModelKind("acc-1", "gpt-5.4"); got != AccountModelLimitAccount {
		t.Errorf("expected Account limit, got %d", got)
	}

	// 验证 Model 优先级：先耗尽 Model
	erl2 := NewEnhancedRateLimiter(nil, 0, 100, 2)
	if got := erl2.AllowAccountModelKind("acc-a", "gpt-5.4"); got != AccountModelLimitNone {
		t.Errorf("req 1: %d", got)
	}
	if got := erl2.AllowAccountModelKind("acc-b", "gpt-5.4"); got != AccountModelLimitNone {
		t.Errorf("req 2: %d", got)
	}
	// 第 3 次任意账号但同 model：model 桶空
	if got := erl2.AllowAccountModelKind("acc-c", "gpt-5.4"); got != AccountModelLimitModel {
		t.Errorf("expected Model limit, got %d", got)
	}
}

// TestRateLimiter_NoExponentialBackoffOnExhaustion 验证修复后的行为：
// 桶用尽后不会进入指数退避冷却，等待补充时间后能继续通过。
// 这是核心 bug 修复的回归测试。
func TestRateLimiter_NoExponentialBackoffOnExhaustion(t *testing.T) {
	// RPM=60 => 1 token/秒补充
	ll := newLevelLimiter("test", LevelAccount, 60)

	// 一口气耗光 60 个令牌
	for i := 0; i < 60; i++ {
		if !ll.allow() {
			t.Fatalf("token %d should have passed during burst", i)
		}
	}
	// 第 61 个应该失败
	if ll.allow() {
		t.Fatal("token 61 should be denied (bucket empty)")
	}

	// 关键断言：不应该进入指数退避冷却
	if ll.cooldown.isInCooldown() {
		t.Fatal("must not enter exponential backoff cooldown")
	}

	// 等待 ~1.1 秒，桶应该补充至少 1 个令牌
	time.Sleep(1100 * time.Millisecond)
	if !ll.allow() {
		t.Fatal("after 1.1s refill, should allow at least one request")
	}
}

// TestRateLimiter_DynamicUpdateUnderLoad 高并发下动态修改 RPM，
// 验证不死锁、不 panic、不会出现读到 nil bucket 的崩溃。
func TestRateLimiter_DynamicUpdateUnderLoad(t *testing.T) {
	erl := NewEnhancedRateLimiter(nil, 0, 100, 100)

	stop := make(chan struct{})
	var requestCount, panics int64
	var wg sync.WaitGroup

	// 启动 20 个并发 worker 不停做 check
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					atomic.AddInt64(&panics, 1)
					t.Errorf("worker %d panicked: %v", workerID, r)
				}
			}()
			for {
				select {
				case <-stop:
					return
				default:
				}
				accID := fmt.Sprintf("acc-%d", workerID%5)
				erl.AllowAccountModel(accID, "gpt-5.4")
				atomic.AddInt64(&requestCount, 1)
			}
		}(i)
	}

	// 同时启动 1 个 updater 在 worker 跑的过程中频繁改 RPM
	wg.Add(1)
	go func() {
		defer wg.Done()
		rpms := []int{50, 200, 0, 100, 500, 10}
		for i := 0; i < 50; i++ {
			select {
			case <-stop:
				return
			default:
			}
			rpm := rpms[i%len(rpms)]
			erl.UpdateAccountRPM(rpm)
			erl.UpdateModelRPM(rpm)
			time.Sleep(5 * time.Millisecond)
		}
	}()

	time.Sleep(500 * time.Millisecond)
	close(stop)
	wg.Wait()

	if panics > 0 {
		t.Errorf("got %d panics", panics)
	}
	if requestCount < 1000 {
		t.Errorf("workers ran too few requests (%d), suggesting blocking/deadlock", requestCount)
	}
}

// TestRateLimiter_RefillFairness 桶按 rpm/60 token/秒线性补充，10 秒内应能放过
// 约 (rpm + rpm/60 * 10) 个请求。
func TestRateLimiter_RefillFairness(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping refill timing test in -short mode")
	}
	rpm := 60
	ll := newLevelLimiter("test", LevelAccount, rpm)

	// 先耗光 burst
	for i := 0; i < rpm; i++ {
		ll.allow()
	}
	// 在 6 秒内每 100ms 尝试一次，统计通过数。
	// 6 秒 ≈ 6 个 token 补充（refill=1/秒）
	var passed int
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if ll.allow() {
			passed++
		}
		time.Sleep(100 * time.Millisecond)
	}
	// 容忍 ±2 的抖动（系统调度精度）
	if passed < 4 || passed > 8 {
		t.Errorf("expected ~6 refilled tokens in 6s, got %d", passed)
	}
}

// TestGlobalRPM_SharedAcrossEndpoints 验证 GlobalRPM 是全局共享的
// （所有调用方共用同一个 globalLimiter）。
func TestGlobalRPM_SharedAcrossEndpoints(t *testing.T) {
	erl := NewEnhancedRateLimiter(nil, 5, 0, 0)

	var allowed int64
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if erl.Allow() {
				atomic.AddInt64(&allowed, 1)
			}
		}()
	}
	wg.Wait()

	if allowed != 5 {
		t.Errorf("expected exactly 5 allowed (global bucket), got %d", allowed)
	}
}

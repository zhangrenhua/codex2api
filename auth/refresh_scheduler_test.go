package auth

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func newTestAccountWithExpiry(id int64, expiresIn time.Duration) *Account {
	acc := &Account{
		DBID:           id,
		AccessToken:    "test_token",
		RefreshToken:   "test_refresh_token",
		Status:         StatusReady,
		HealthTier:     HealthTierHealthy,
		SchedulerScore: 100,
	}
	if expiresIn > 0 {
		acc.ExpiresAt = time.Now().Add(expiresIn)
	}
	return acc
}

func TestNewRefreshScheduler(t *testing.T) {
	config := DefaultRefreshConfig()
	config.MaxConcurrency = 5

	store := &Store{}
	scheduler := NewRefreshScheduler(store, config)

	if scheduler == nil {
		t.Fatal("NewRefreshScheduler returned nil")
	}
	if scheduler.config.MaxConcurrency != 5 {
		t.Fatalf("MaxConcurrency = %d, want 5", scheduler.config.MaxConcurrency)
	}
	if scheduler.store != store {
		t.Fatal("store mismatch")
	}
}

func TestRefreshSchedulerSchedule(t *testing.T) {
	config := DefaultRefreshConfig()
	store := &Store{}
	scheduler := NewRefreshScheduler(store, config)

	acc := newTestAccountWithExpiry(1, 10*time.Minute)
	scheduler.Schedule(acc)

	// 检查任务是否被创建
	scheduler.tasksMu.RLock()
	task, ok := scheduler.tasks[1]
	scheduler.tasksMu.RUnlock()

	if !ok {
		t.Fatal("task not found after Schedule")
	}

	task.mu.RLock()
	if task.State != RefreshStatePending {
		t.Fatalf("task.State = %d, want RefreshStatePending", task.State)
	}
	task.mu.RUnlock()

	// 检查指标
	if scheduler.metrics.TotalTasks.Load() != 1 {
		t.Fatalf("TotalTasks = %d, want 1", scheduler.metrics.TotalTasks.Load())
	}
}

func TestRefreshSchedulerCalculateOptimalTime(t *testing.T) {
	config := DefaultRefreshConfig()
	config.PreExpireWindow = 5 * time.Minute
	scheduler := NewRefreshScheduler(&Store{}, config)

	// 测试即将过期的账号
	acc1 := newTestAccountWithExpiry(1, 1*time.Minute)
	optimal1 := scheduler.calculateOptimalTime(acc1)
	if time.Until(optimal1) > 30*time.Second {
		t.Fatalf("imminent expiry should schedule soon, got %v", time.Until(optimal1))
	}

	// 测试距离过期时间较长的账号
	acc2 := newTestAccountWithExpiry(2, 30*time.Minute)
	optimal2 := scheduler.calculateOptimalTime(acc2)
	timeUntilExpire := acc2.ExpiresAt.Sub(optimal2)
	// 应该在过期前 PreExpireWindow 左右
	if timeUntilExpire < 4*time.Minute || timeUntilExpire > 6*time.Minute {
		t.Fatalf("expected refresh near pre-expire window, time until expire: %v", timeUntilExpire)
	}
}

func TestRefreshSchedulerCalculatePriority(t *testing.T) {
	config := DefaultRefreshConfig()
	scheduler := NewRefreshScheduler(&Store{}, config)

	// 健康账号，时间充足
	acc1 := newTestAccountWithExpiry(1, 30*time.Minute)
	acc1.HealthTier = HealthTierHealthy
	priority1 := scheduler.calculatePriority(acc1)

	// 风险账号
	acc2 := newTestAccountWithExpiry(2, 30*time.Minute)
	acc2.HealthTier = HealthTierRisky
	priority2 := scheduler.calculatePriority(acc2)

	// 健康账号优先级应高于风险账号
	if priority1 <= priority2 {
		t.Fatalf("healthy priority %d should be > risky priority %d", priority1, priority2)
	}

	// 即将过期的健康账号应有更高优先级
	acc3 := newTestAccountWithExpiry(3, 10*time.Second)
	acc3.HealthTier = HealthTierHealthy
	priority3 := scheduler.calculatePriority(acc3)
	if priority3 <= priority1 {
		t.Fatalf("imminent expiry priority %d should be > normal priority %d", priority3, priority1)
	}
}

func TestRefreshSchedulerRandomJitter(t *testing.T) {
	config := DefaultRefreshConfig()
	config.JitterPercent = 0.1
	scheduler := NewRefreshScheduler(&Store{}, config)

	base := 100 * time.Millisecond
	samples := make([]time.Duration, 100)
	for i := 0; i < 100; i++ {
		samples[i] = scheduler.randomJitter(base)
	}

	// 检查抖动范围
	minJitter := base * 0.9
	maxJitter := base * 1.1
	for i, s := range samples {
		if s < minJitter || s > maxJitter {
			t.Fatalf("sample %d out of range: %v", i, s)
		}
	}

	// 检查有变化（不是固定值）
	allSame := true
	for i := 1; i < 100; i++ {
		if samples[i] != samples[0] {
			allSame = false
			break
		}
	}
	if allSame {
		t.Fatal("jitter should vary")
	}
}

func TestRefreshSchedulerCalculateRetryTime(t *testing.T) {
	config := DefaultRefreshConfig()
	config.RetryBackoffBase = 1 * time.Second
	scheduler := NewRefreshScheduler(&Store{}, config)

	// 第一次重试
	r1 := scheduler.calculateRetryTime(1)
	delay1 := r1.Sub(time.Now())
	if delay1 < 900*time.Millisecond || delay1 > 1100*time.Millisecond {
		t.Fatalf("first retry delay = %v, want ~1s", delay1)
	}

	// 第二次重试（指数退避）
	r2 := scheduler.calculateRetryTime(2)
	delay2 := r2.Sub(time.Now())
	if delay2 < 1800*time.Millisecond || delay2 > 2200*time.Millisecond {
		t.Fatalf("second retry delay = %v, want ~2s", delay2)
	}

	// 多次重试（上限检查）
	r5 := scheduler.calculateRetryTime(10)
	delay5 := r5.Sub(time.Now())
	if delay5 > 6*time.Minute {
		t.Fatalf("retry delay should be capped, got %v", delay5)
	}
}

func TestRefreshSchedulerScheduleImmediate(t *testing.T) {
	config := DefaultRefreshConfig()
	scheduler := NewRefreshScheduler(&Store{}, config)

	acc := newTestAccountWithExpiry(1, 10*time.Minute)
	scheduler.ScheduleImmediate(acc)

	// 检查任务是否被创建
	scheduler.tasksMu.RLock()
	task, ok := scheduler.tasks[1]
	scheduler.tasksMu.RUnlock()

	if !ok {
		t.Fatal("task not found after ScheduleImmediate")
	}

	task.mu.RLock()
	if task.Priority != 1000 {
		t.Fatalf("immediate task priority = %d, want 1000", task.Priority)
	}
	task.mu.RUnlock()
}

func TestRefreshSchedulerCancelTask(t *testing.T) {
	config := DefaultRefreshConfig()
	scheduler := NewRefreshScheduler(&Store{}, config)

	acc := newTestAccountWithExpiry(1, 10*time.Minute)
	scheduler.Schedule(acc)

	// 取消任务
	cancelled := scheduler.CancelTask(1)
	if !cancelled {
		t.Fatal("CancelTask should return true for pending task")
	}

	// 检查任务状态
	state, _, _ := scheduler.GetTaskStatus(1)
	if state != RefreshStateSuccess {
		t.Fatalf("cancelled task state = %d, want RefreshStateSuccess", state)
	}

	// 再次取消应该失败
	cancelled = scheduler.CancelTask(1)
	if cancelled {
		t.Fatal("CancelTask should return false for non-existent task")
	}
}

func TestRefreshSchedulerGetTaskStatus(t *testing.T) {
	config := DefaultRefreshConfig()
	scheduler := NewRefreshScheduler(&Store{}, config)

	// 不存在的任务
	state, attempts, err := scheduler.GetTaskStatus(999)
	if state != RefreshStateSuccess {
		t.Fatalf("non-existent task state = %d, want RefreshStateSuccess", state)
	}
	if attempts != 0 {
		t.Fatalf("attempts = %d, want 0", attempts)
	}
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}

	// 存在的任务
	acc := newTestAccountWithExpiry(1, 10*time.Minute)
	scheduler.Schedule(acc)

	state, attempts, err = scheduler.GetTaskStatus(1)
	if state != RefreshStatePending {
		t.Fatalf("new task state = %d, want RefreshStatePending", state)
	}
}

func TestRefreshSchedulerScheduleBatch(t *testing.T) {
	config := DefaultRefreshConfig()
	config.BatchSize = 2
	config.MinInterval = 100 * time.Millisecond
	scheduler := NewRefreshScheduler(&Store{}, config)

	// 批量调度
	accounts := make([]*Account, 5)
	for i := 0; i < 5; i++ {
		accounts[i] = newTestAccountWithExpiry(int64(i+1), 10*time.Minute)
	}

	scheduler.ScheduleBatch(accounts)

	// 检查是否创建了所有任务
	scheduler.tasksMu.RLock()
	taskCount := len(scheduler.tasks)
	scheduler.tasksMu.RUnlock()

	if taskCount != 5 {
		t.Fatalf("task count = %d, want 5", taskCount)
	}

	// 检查指标
	if scheduler.metrics.TotalTasks.Load() != 5 {
		t.Fatalf("TotalTasks = %d, want 5", scheduler.metrics.TotalTasks.Load())
	}
}

func TestRefreshSchedulerPriorityQueue(t *testing.T) {
	config := DefaultRefreshConfig()
	scheduler := NewRefreshScheduler(&Store{}, config)

	// 创建不同优先级的任务
	acc1 := newTestAccountWithExpiry(1, 30*time.Second) // 高优先级（即将过期）
	acc2 := newTestAccountWithExpiry(2, 30*time.Minute) // 低优先级
	acc3 := newTestAccountWithExpiry(3, 10*time.Second) // 最高优先级

	scheduler.Schedule(acc1)
	scheduler.Schedule(acc2)
	scheduler.Schedule(acc3)

	// 检查优先队列顺序
	task1 := scheduler.popFromQueue()
	task2 := scheduler.popFromQueue()
	task3 := scheduler.popFromQueue()

	// 最高优先级的应该先被弹出
	if task3 == nil || task3.Account.DBID != 3 {
		t.Fatal("highest priority task should be popped first")
	}
	if task1 == nil || task1.Account.DBID != 1 {
		t.Fatal("second priority task should be popped second")
	}
	if task2 == nil || task2.Account.DBID != 2 {
		t.Fatal("lowest priority task should be popped last")
	}
}

func TestRefreshSchedulerMetrics(t *testing.T) {
	config := DefaultRefreshConfig()
	scheduler := NewRefreshScheduler(&Store{}, config)

	// 初始指标
	metrics := scheduler.GetMetrics()
	if metrics.TotalTasks != 0 {
		t.Fatalf("initial TotalTasks = %d, want 0", metrics.TotalTasks)
	}

	// 添加任务
	acc := newTestAccountWithExpiry(1, 10*time.Minute)
	scheduler.Schedule(acc)

	metrics = scheduler.GetMetrics()
	if metrics.TotalTasks != 1 {
		t.Fatalf("after schedule TotalTasks = %d, want 1", metrics.TotalTasks)
	}
}

func TestRefreshSchedulerUpdateAvgDuration(t *testing.T) {
	config := DefaultRefreshConfig()
	scheduler := NewRefreshScheduler(&Store{}, config)

	// 第一次更新
	scheduler.updateAvgDuration(100 * time.Millisecond)
	if scheduler.metrics.AvgDurationMs.Load() != 100 {
		t.Fatalf("avg duration = %d, want 100", scheduler.metrics.AvgDurationMs.Load())
	}

	// 第二次更新（指数移动平均）
	scheduler.updateAvgDuration(200 * time.Millisecond)
	avg := scheduler.metrics.AvgDurationMs.Load()
	// (100*9 + 200) / 10 = 110
	if avg != 110 {
		t.Fatalf("avg duration = %d, want 110", avg)
	}
}

func BenchmarkRefreshSchedulerSchedule(b *testing.B) {
	config := DefaultRefreshConfig()
	scheduler := NewRefreshScheduler(&Store{}, config)
	accounts := make([]*Account, b.N)
	for i := 0; i < b.N; i++ {
		accounts[i] = newTestAccountWithExpiry(int64(i+1), 10*time.Minute)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scheduler.Schedule(accounts[i])
	}
}

func BenchmarkRefreshSchedulerCalculateOptimalTime(b *testing.B) {
	config := DefaultRefreshConfig()
	scheduler := NewRefreshScheduler(&Store{}, config)
	acc := newTestAccountWithExpiry(1, 10*time.Minute)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scheduler.calculateOptimalTime(acc)
	}
}

func BenchmarkRefreshSchedulerCalculatePriority(b *testing.B) {
	config := DefaultRefreshConfig()
	scheduler := NewRefreshScheduler(&Store{}, config)
	acc := newTestAccountWithExpiry(1, 10*time.Minute)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scheduler.calculatePriority(acc)
	}
}

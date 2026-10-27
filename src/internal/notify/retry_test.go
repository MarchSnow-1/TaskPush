package notify

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/config"
	"github.com/MarchSnow-1/TaskPush/internal/settings"
)

// settingsDuration 把标准库时长包装成配置模型的 Duration, 供渠道级覆盖测试使用
//
// settingsDuration wraps a standard-library duration into the configuration model type, for the
// channel-level override tests
func settingsDuration(d time.Duration) settings.Duration {
	return settings.Duration(d)
}

// ---------------------------------------------------------------------------
// 重试测试: 0 次重试, N 次重试, 永久错误立即停止, 固定延迟, 等待期间取消
//
// Retry tests: zero retries, N retries, a permanent error stopping at once, the fixed delay, and a
// cancellation during the wait
// ---------------------------------------------------------------------------

// TestRetry_NoRetries 覆盖 retries=0 时只尝试一次
//
// TestRetry_NoRetries covers exactly one attempt when retries is 0
func TestRetry_NoRetries(t *testing.T) {
	resetFake()
	defaults := defaultTestDefaults()
	defaults.Retries = 0
	results, err := Send(context.Background(), defaults, []config.Channel{testChannel("example", fakeSettings{FailTimes: 3})}, Options{})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if results[0].Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", results[0].Attempts)
	}
	if results[0].Error == nil {
		t.Error("Error = nil, want the transient failure")
	}
	if got := recordOf("example").SendCnt; got != 1 {
		t.Errorf("Send call count = %d, want 1", got)
	}
}

// TestRetry_ExhaustsRetries 覆盖 retries=N 时总尝试次数为 N+1
//
// TestRetry_ExhaustsRetries covers retries plus one total attempts when retries is N
func TestRetry_ExhaustsRetries(t *testing.T) {
	resetFake()
	defaults := defaultTestDefaults()
	defaults.Retries = 2
	results, err := Send(context.Background(), defaults, []config.Channel{testChannel("example", fakeSettings{FailTimes: 99})}, Options{})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if results[0].Attempts != 3 {
		t.Errorf("Attempts = %d, want 3", results[0].Attempts)
	}
	if got := recordOf("example").SendCnt; got != 3 {
		t.Errorf("Send call count = %d, want 3", got)
	}
}

// TestRetry_SucceedsAfterTransient 覆盖瞬时错误后重试成功, Attempts 记录实际次数
//
// TestRetry_SucceedsAfterTransient covers a retry succeeding after transient errors, with Attempts
// counting the attempts actually made
func TestRetry_SucceedsAfterTransient(t *testing.T) {
	resetFake()
	defaults := defaultTestDefaults()
	defaults.Retries = 3
	results, err := Send(context.Background(), defaults, []config.Channel{testChannel("example", fakeSettings{FailTimes: 2})}, Options{})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if results[0].Attempts != 3 {
		t.Errorf("Attempts = %d, want 3", results[0].Attempts)
	}
	if results[0].Error != nil {
		t.Errorf("Error = %v, want nil", results[0].Error)
	}
}

// TestRetry_PermanentStops 覆盖永久错误立即停止, 不等待也不重试
//
// TestRetry_PermanentStops covers a permanent error stopping at once, with no wait and no retry
func TestRetry_PermanentStops(t *testing.T) {
	resetFake()
	defaults := defaultTestDefaults()
	defaults.Retries = 5
	results, err := Send(context.Background(), defaults, []config.Channel{testChannel("example", fakeSettings{Permanent: true})}, Options{})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if results[0].Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", results[0].Attempts)
	}
	if !apperr.IsPermanent(results[0].Error) {
		t.Errorf("Error = %v, want a permanent error", results[0].Error)
	}
}

// TestRetry_Delay 覆盖每次重试之间等待 retryDelay, 用 SendDelayMs 放大单次发送窗口
//
// TestRetry_Delay covers waiting retryDelay between attempts, with SendDelayMs widening one send
func TestRetry_Delay(t *testing.T) {
	resetFake()
	defaults := defaultTestDefaults()
	defaults.Retries = 1
	defaults.RetryDelay = 30 * time.Millisecond

	start := time.Now()
	if _, err := Send(context.Background(), defaults, []config.Channel{testChannel("example", fakeSettings{FailTimes: 1})}, Options{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 25*time.Millisecond {
		t.Errorf("elapsed = %s, want at least the 30ms retry delay", elapsed)
	}
	if got := recordOf("example").SendCnt; got != 2 {
		t.Errorf("Send call count = %d, want 2", got)
	}
}

// TestRetry_CanceledDuringSend 覆盖发送期间取消: 返回 ctx 错误, 不再重试
//
// TestRetry_CanceledDuringSend covers a cancellation during a send, returning the ctx error and
// making no further attempt
func TestRetry_CanceledDuringSend(t *testing.T) {
	resetFake()
	defaults := defaultTestDefaults()
	defaults.Retries = 5
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)

	results, err := Send(ctx, defaults, []config.Channel{testChannel("example", fakeSettings{FailTimes: 99, SendDelayMs: 120})}, Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Send error = %v, want context.Canceled", err)
	}
	if got := recordOf("example").SendCnt; got != 1 {
		t.Errorf("Send call count = %d, want 1", got)
	}
	if results[0].Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", results[0].Attempts)
	}
	if results[0].Error == nil {
		t.Error("the canceled send left Result.Error nil")
	}
}

// TestRetry_CanceledDuringWait 覆盖重试等待期间取消: 不再发起第二次尝试
//
// TestRetry_CanceledDuringWait covers a cancellation during the retry wait, which starts no second
// attempt
func TestRetry_CanceledDuringWait(t *testing.T) {
	resetFake()
	defaults := defaultTestDefaults()
	defaults.Retries = 3
	defaults.RetryDelay = 2 * time.Second
	// 取消要落在发送之后的等待窗口里, 因此这里的超时必须长于 fake 的单次发送
	//
	// The cancellation has to land in the wait window after the send, so this timeout has to outlast
	// one fake send
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	results, err := Send(ctx, defaults, []config.Channel{testChannel("example", fakeSettings{FailTimes: 99, SendDelayMs: 60})}, Options{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Send error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("elapsed = %s, want the wait to end with the context rather than the 2s delay", elapsed)
	}
	if got := recordOf("example").SendCnt; got != 1 {
		t.Errorf("Send call count = %d, want 1", got)
	}
	if results[0].Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", results[0].Attempts)
	}
}

// TestWaitRetry_ZeroDelayHonoursCanceledContext 覆盖零延迟不会吞掉已取消的 context
//
// TestWaitRetry_ZeroDelayHonoursCanceledContext covers a zero delay not swallowing an already
// cancelled context
func TestWaitRetry_ZeroDelayHonoursCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitRetry(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitRetry error = %v, want context.Canceled", err)
	}
}

// TestSend_CanceledBeforeStart 覆盖进入 Send 时已经取消: 不创建渠道, 槽位带取消错误
//
// TestSend_CanceledBeforeStart covers entering Send with a cancelled context: no channel is created
// and the slots carry the cancellation error
func TestSend_CanceledBeforeStart(t *testing.T) {
	resetFake()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	results, err := Send(ctx, defaultTestDefaults(), []config.Channel{testChannel("example", fakeSettings{})}, Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Send error = %v, want context.Canceled", err)
	}
	if got := recordOf("example").PrepareCnt; got != 0 {
		t.Errorf("Prepare call count = %d, want 0", got)
	}
	if len(results) != 1 || results[0].Error == nil {
		t.Fatalf("results = %+v, want one slot carrying the cancellation error", results)
	}
	var prob *apperr.Problem
	if !errors.As(results[0].Error, &prob) || prob.ID != keyErrorCanceled {
		t.Errorf("slot error = %v, want %s", results[0].Error, keyErrorCanceled)
	}
}

// TestEffectiveFailureLandsInResult 覆盖生效默认值非法时错误进 Result, 不返回全局错误
//
// TestEffectiveFailureLandsInResult covers an invalid effective default landing in Result rather
// than a global error
func TestEffectiveFailureLandsInResult(t *testing.T) {
	resetFake()
	spec := testChannel("example", fakeSettings{})
	negative := -1
	spec.Retries = &negative

	results, err := Send(context.Background(), defaultTestDefaults(), []config.Channel{spec}, Options{})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	var prob *apperr.Problem
	if !errors.As(results[0].Error, &prob) {
		t.Fatalf("result error = %v, want *apperr.Problem", results[0].Error)
	}
	if prob.ID != keyErrorClient {
		t.Errorf("ID = %q, want %q", prob.ID, keyErrorClient)
	}
	if got := recordOf("example").PrepareCnt; got != 0 {
		t.Errorf("Prepare call count = %d, want 0", got)
	}
}

// TestSend_ChannelOwnTimeoutDoesNotAbortOthers 覆盖第一个渠道自身超时 (错误链包着
// context.DeadlineExceeded, 父 context 未取消) 时, 第二个渠道仍被发送, 整体不返回全局错误
// http.Client.Timeout 触发的单渠道超时属于该渠道的失败, 不是调度层取消
//
// TestSend_ChannelOwnTimeoutDoesNotAbortOthers covers a channel's own timeout, an error chain
// wrapping context.DeadlineExceeded while the parent context stays alive: the second channel is
// still sent and no global error comes back
// A timeout raised by http.Client.Timeout is that channel's failure, not a scheduling cancellation
func TestSend_ChannelOwnTimeoutDoesNotAbortOthers(t *testing.T) {
	resetFake()
	defaults := defaultTestDefaults()
	defaults.Retries = 0
	specs := []config.Channel{
		testChannel("example.timeout", fakeSettings{Deadline: true}),
		testChannel("example.healthy", fakeSettings{}),
	}

	results, err := Send(context.Background(), defaults, specs, Options{Parallel: 1})
	if err != nil {
		t.Fatalf("Send error = %v, want nil: a channel-level timeout is not a scheduling cancellation", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].Error == nil {
		t.Fatal("the timed-out channel carries no error in Result")
	}
	if !errors.Is(results[0].Error, context.DeadlineExceeded) {
		t.Errorf("first result error = %v, want it to wrap context.DeadlineExceeded", results[0].Error)
	}
	if results[1].Error != nil {
		t.Errorf("second result error = %v, want nil: the later channel has to be dispatched", results[1].Error)
	}
	if got := recordOf("example.healthy").SendCnt; got != 1 {
		t.Errorf("second channel Send call count = %d, want 1", got)
	}
	if got := results[0].Attempts; got != 1 {
		t.Errorf("timed-out channel Attempts = %d, want 1", got)
	}
	if got := results[1].Attempts; got != 1 {
		t.Errorf("healthy channel Attempts = %d, want 1", got)
	}
}

// TestSend_ChannelOwnTimeoutConcurrent 覆盖并发分支上的同一判定: 一个渠道自身超时不影响其它渠道
//
// TestSend_ChannelOwnTimeoutConcurrent covers the same verdict on the concurrent branch: one
// channel's own timeout leaves the others untouched
func TestSend_ChannelOwnTimeoutConcurrent(t *testing.T) {
	resetFake()
	defaults := defaultTestDefaults()
	defaults.Retries = 0
	specs := []config.Channel{
		testChannel("example.timeout", fakeSettings{Deadline: true}),
		testChannel("example.healthy", fakeSettings{}),
		testChannel("example.also", fakeSettings{}),
	}

	results, err := Send(context.Background(), defaults, specs, Options{Parallel: 3})
	if err != nil {
		t.Fatalf("Send error = %v, want nil", err)
	}
	if results[0].Error == nil {
		t.Error("the timed-out channel carries no error in Result")
	}
	for _, index := range []int{1, 2} {
		if results[index].Error != nil {
			t.Errorf("results[%d].Error = %v, want nil", index, results[index].Error)
		}
	}
}

// TestSend_ParentDeadlineAbortsSequential 覆盖外层 context 超时仍然中止后续渠道
// 与渠道自身超时的区别正是这里要守住的边界
//
// TestSend_ParentDeadlineAbortsSequential covers an outer context deadline still stopping the later
// channels
// The difference from a channel's own timeout is exactly the boundary protected here
func TestSend_ParentDeadlineAbortsSequential(t *testing.T) {
	resetFake()
	defaults := defaultTestDefaults()
	defaults.Retries = 3
	defaults.RetryDelay = 2 * time.Second
	// 外层超时要落在第一个渠道的重试等待窗口里, 因此长于该渠道的单次发送
	//
	// The outer deadline has to land in the first channel's retry wait, so it outlasts one send of
	// that channel
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	results, err := Send(ctx, defaults, []config.Channel{
		testChannel("example.first", fakeSettings{FailTimes: 99, SendDelayMs: 50}),
		testChannel("example.later", fakeSettings{}),
	}, Options{Parallel: 1})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Send error = %v, want context.DeadlineExceeded", err)
	}
	if got := recordOf("example.later").SendCnt; got != 0 {
		t.Errorf("later channel Send call count = %d, want 0 after the outer deadline", got)
	}
	if results[1].Error == nil {
		t.Error("the undispatched slot carries no cancellation error")
	}
	var prob *apperr.Problem
	if !errors.As(results[1].Error, &prob) || prob.ID != keyErrorCanceled {
		t.Errorf("undispatched slot error = %v, want %s", results[1].Error, keyErrorCanceled)
	}
}

// TestSend_RetriesFromChannelOverride 覆盖渠道级 retries 覆盖全局默认值
//
// TestSend_RetriesFromChannelOverride covers a channel-level retries value overriding the global
// default
func TestSend_RetriesFromChannelOverride(t *testing.T) {
	resetFake()
	defaults := defaultTestDefaults()
	defaults.Retries = 0
	two := 2
	spec := testChannel("example", fakeSettings{FailTimes: 99})
	spec.Retries = &two

	results, err := Send(context.Background(), defaults, []config.Channel{spec}, Options{})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if results[0].Attempts != 3 {
		t.Errorf("Attempts = %d, want 3 from the channel-level retries", results[0].Attempts)
	}
}

// TestSend_ChanLevelTimeoutOverride 覆盖渠道级 timeout 用于客户端构造
//
// TestSend_ChanLevelTimeoutOverride covers the channel-level timeout reaching the client
func TestSend_ChanLevelTimeoutOverride(t *testing.T) {
	resetFake()
	timeout := settingsDuration(2 * time.Second)
	spec := testChannel("example", fakeSettings{})
	spec.Timeout = &timeout

	if _, err := Send(context.Background(), config.Defaults{Timeout: time.Second}, []config.Channel{spec}, Options{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := recordOf("example").SendCnt; got != 1 {
		t.Errorf("Send call count = %d, want 1", got)
	}
}

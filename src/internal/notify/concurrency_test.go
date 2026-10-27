package notify

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/config"
)

// ---------------------------------------------------------------------------
// 并发测试: 上限, 结果顺序, 失败隔离, 渠道数收敛, 取消时停止派发
//
// Concurrency tests: the cap, the result order, failure isolation, the cap collapsing to the
// channel count, and dispatch stopping on cancellation
// ---------------------------------------------------------------------------

// TestSend_ConcurrencyCap 覆盖 --parallel 上限: 峰值不超过给定并发数
//
// TestSend_ConcurrencyCap covers the --parallel cap: the peak never exceeds the given concurrency
func TestSend_ConcurrencyCap(t *testing.T) {
	resetFake()
	defaults := defaultTestDefaults()
	defaults.Retries = 0
	specs := []config.Channel{
		testChannel("example.a", fakeSettings{SendDelayMs: 60}),
		testChannel("example.b", fakeSettings{SendDelayMs: 60}),
		testChannel("example.c", fakeSettings{SendDelayMs: 60}),
		testChannel("example.d", fakeSettings{SendDelayMs: 60}),
	}
	if _, err := Send(context.Background(), defaults, specs, Options{Parallel: 2}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if peak := maxConcurrent(); peak > 2 {
		t.Errorf("concurrency peak = %d, want at most 2", peak)
	}
	if peak := maxConcurrent(); peak < 2 {
		t.Errorf("concurrency peak = %d, want the pool to reach 2", peak)
	}
}

// TestSend_ParallelAboveChannelCount 覆盖并发数大于渠道数时按渠道数收敛
//
// TestSend_ParallelAboveChannelCount covers the concurrency collapsing to the channel count when
// the request is higher
func TestSend_ParallelAboveChannelCount(t *testing.T) {
	resetFake()
	defaults := defaultTestDefaults()
	defaults.Retries = 0
	specs := []config.Channel{
		testChannel("example.a", fakeSettings{SendDelayMs: 40}),
		testChannel("example.b", fakeSettings{SendDelayMs: 40}),
	}
	if _, err := Send(context.Background(), defaults, specs, Options{Parallel: 8}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if peak := maxConcurrent(); peak > 2 {
		t.Errorf("concurrency peak = %d, want at most the channel count 2", peak)
	}
}

// TestSend_ParallelNonPositiveTreatedAsOne 覆盖 Parallel <= 0 按 1 处理
//
// TestSend_ParallelNonPositiveTreatedAsOne covers Parallel below or equal to zero being treated as 1
func TestSend_ParallelNonPositiveTreatedAsOne(t *testing.T) {
	resetFake()
	defaults := defaultTestDefaults()
	defaults.Retries = 0
	specs := []config.Channel{
		testChannel("example.a", fakeSettings{SendDelayMs: 40}),
		testChannel("example.b", fakeSettings{SendDelayMs: 40}),
	}
	if _, err := Send(context.Background(), defaults, specs, Options{Parallel: 0}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if peak := maxConcurrent(); peak != 1 {
		t.Errorf("concurrency peak = %d, want 1", peak)
	}
}

// TestSend_ResultOrderUnderConcurrency 覆盖并发完成顺序不同时结果仍按输入顺序
// 第一个渠道最慢, 若按完成顺序写结果它会被排到最后
//
// TestSend_ResultOrderUnderConcurrency covers results staying in the input order when the
// completion order differs
// The first channel is the slowest, so it would land last if results were written in completion order
func TestSend_ResultOrderUnderConcurrency(t *testing.T) {
	resetFake()
	defaults := defaultTestDefaults()
	defaults.Retries = 0
	specs := []config.Channel{
		testChannel("example.slow", fakeSettings{SendDelayMs: 90}),
		testChannel("example.quick", fakeSettings{SendDelayMs: 5}),
		testChannel("example.mid", fakeSettings{SendDelayMs: 40}),
	}
	results, err := Send(context.Background(), defaults, specs, Options{Parallel: 3})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	want := []string{"example.slow", "example.quick", "example.mid"}
	for i, id := range want {
		if results[i].ID != id {
			t.Errorf("results[%d].ID = %q, want %q", i, results[i].ID, id)
		}
	}
}

// TestSend_CanceledStopsDispatch 覆盖并发场景下取消后不再派发新渠道
//
// TestSend_CanceledStopsDispatch covers no further channel being dispatched after a cancellation
// under concurrency
func TestSend_CanceledStopsDispatch(t *testing.T) {
	resetFake()
	defaults := defaultTestDefaults()
	defaults.Retries = 0
	specs := []config.Channel{
		testChannel("example.a", fakeSettings{SendDelayMs: 200}),
		testChannel("example.b", fakeSettings{SendDelayMs: 200}),
		testChannel("example.c", fakeSettings{SendDelayMs: 200}),
		testChannel("example.d", fakeSettings{SendDelayMs: 200}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)

	results, err := Send(ctx, defaults, specs, Options{Parallel: 2})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Send error = %v, want context.Canceled", err)
	}
	if peak := maxConcurrent(); peak > 2 {
		t.Errorf("concurrency peak = %d, want at most 2", peak)
	}
	// 未派发的槽位带取消错误, 不能是零值: CLI 会把零值读成成功
	//
	// The undispatched slots carry the cancellation error rather than a zero value, which the CLI
	// would read as success
	for i, result := range results {
		if result.ID == "" {
			t.Errorf("results[%d] is a zero value", i)
			continue
		}
		if result.Error == nil {
			t.Errorf("results[%d] has no error despite the cancellation", i)
		}
	}
	if len(results) != len(specs) {
		t.Fatalf("results = %d, want %d slots", len(results), len(specs))
	}
}

// TestSend_ConcurrentFailureIsolation 覆盖并发下一个渠道永久失败不影响其它渠道
//
// TestSend_ConcurrentFailureIsolation covers one channel failing permanently without affecting the
// others under concurrency
func TestSend_ConcurrentFailureIsolation(t *testing.T) {
	resetFake()
	defaults := defaultTestDefaults()
	defaults.Retries = 0
	specs := []config.Channel{
		testChannel("example.a", fakeSettings{}),
		testChannel("example.b", fakeSettings{Permanent: true}),
		testChannel("example.c", fakeSettings{}),
	}
	results, err := Send(context.Background(), defaults, specs, Options{Parallel: 3})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if results[0].Error != nil || results[2].Error != nil {
		t.Errorf("healthy channels failed: %v / %v", results[0].Error, results[2].Error)
	}
	if results[1].Error == nil {
		t.Error("the permanent failure is missing from Result.Error")
	}
}

// TestCheck_PlaceholderAndLenient 覆盖 check 使用占位内容与宽松模式, 且不发送
//
// TestCheck_PlaceholderAndLenient covers check using placeholder content and the lenient mode
// without sending
func TestCheck_PlaceholderAndLenient(t *testing.T) {
	resetFake()
	results, err := Check(context.Background(), defaultTestDefaults(), []config.Channel{testChannel("example", fakeSettings{})}, CheckOptions{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	record := recordOf("example")
	if record.Title != checkContent || record.Body != checkContent {
		t.Errorf("check content = %q/%q, want %q", record.Title, record.Body, checkContent)
	}
	if record.Mode != channel.RenderCheck {
		t.Errorf("check mode = %v, want RenderCheck", record.Mode)
	}
	if record.SendCnt != 0 {
		t.Errorf("Send call count = %d, want 0", record.SendCnt)
	}
	if results[0].Preview == nil {
		t.Error("Preview is nil under check")
	}
	if results[0].Error != nil {
		t.Errorf("Error = %v, want nil", results[0].Error)
	}
}

// TestCheck_ResultOrderAndIsolation 覆盖 check 结果按输入顺序且失败隔离
//
// TestCheck_ResultOrderAndIsolation covers check results in the input order with failure isolation
func TestCheck_ResultOrderAndIsolation(t *testing.T) {
	resetFake()
	specs := []config.Channel{
		testChannel("example.a", fakeSettings{}),
		testChannel("example.b", fakeSettings{PrepareFail: true}),
		config.Channel{ID: "example.c", Type: "absent", Settings: []byte(`{}`)},
	}
	results, err := Check(context.Background(), defaultTestDefaults(), specs, CheckOptions{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	for i, want := range []string{"example.a", "example.b", "example.c"} {
		if results[i].ID != want {
			t.Errorf("results[%d].ID = %q, want %q", i, results[i].ID, want)
		}
	}
	if results[0].Error != nil {
		t.Errorf("the healthy channel failed: %v", results[0].Error)
	}
	if results[1].Error == nil || results[2].Error == nil {
		t.Error("an expected check failure is missing")
	}
}

// TestCheck_Warnings 覆盖 check 上送 Prepare 警告
//
// TestCheck_Warnings covers check passing on the Prepare warnings
func TestCheck_Warnings(t *testing.T) {
	resetFake()
	results, err := Check(context.Background(), defaultTestDefaults(), []config.Channel{testChannel("example", fakeSettings{PrepareWarn: true})}, CheckOptions{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(results[0].Warnings) != 1 || results[0].Warnings[0].ID != "channel.example.warn.prepare" {
		t.Errorf("Warnings = %+v, want the Prepare warning", results[0].Warnings)
	}
}

// TestCheck_ReservedVar 覆盖 check 与 send 共用变量解析, 保留名同样被拒
//
// TestCheck_ReservedVar covers check sharing the variable resolution and rejecting a reserved name
// the same way
func TestCheck_ReservedVar(t *testing.T) {
	_, err := Check(context.Background(), defaultTestDefaults(), []config.Channel{testChannel("example", fakeSettings{})}, CheckOptions{VarPairs: []string{"body=x"}})
	var prob *apperr.Problem
	if !errors.As(err, &prob) || prob.ID != "template.error.reserved_var" {
		t.Fatalf("Check error = %v, want template.error.reserved_var", err)
	}
}

// TestCheck_EmptyChannels 覆盖 check 空列表返回空结果, 与 send 的无渠道全局错误不同
//
// TestCheck_EmptyChannels covers check returning an empty result for an empty list, unlike the
// global no-channel error of send
func TestCheck_EmptyChannels(t *testing.T) {
	results, err := Check(context.Background(), defaultTestDefaults(), nil, CheckOptions{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("results = %d, want 0", len(results))
	}
}

package notify

import (
	"context"
	"net/http"
	"time"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
)

// ---------------------------------------------------------------------------
// 重试: 单个渠道按 retries/retryDelay 重复调用同一个 Prepared, 每次发送之间固定等待
// 总尝试次数为 retries+1; 永久错误立即停止; 等待响应 context, 取消后不再发起新的尝试
//
// Retries: one channel calls the same Prepared again according to retries and retryDelay, with a
// fixed wait between attempts
// The total number of attempts is retries plus one, a permanent error stops immediately, and the
// wait honours the context so a cancellation starts no further attempt
// ---------------------------------------------------------------------------

// sendWithRetry 反复调用 prepared.Send, 返回实际尝试次数与最后一次错误
// err 为 nil 表示成功; 重试期间 context 被取消时返回 ctx.Err(), Attempts 只统计已经发生的尝试
//
// sendWithRetry calls prepared.Send repeatedly and returns the attempts actually made together with
// the last error
// A nil error means success, while a context cancelled between attempts returns ctx.Err() and
// Attempts counts the attempts that already happened
func sendWithRetry(ctx context.Context, prepared channel.Prepared, client *http.Client, retries int, delay time.Duration) (int, error) {
	attempts := 0
	for {
		attempts++
		err := prepared.Send(ctx, client)
		if err == nil {
			return attempts, nil
		}
		// 永久错误与已用满重试次数的错误不再等待: 前者重发不会改变判定, 后者是配置允许的上限
		//
		// A permanent error and one that has used up the configured retries both stop without
		// waiting: a resend cannot change the former's verdict, and the latter is the configured cap
		if apperr.IsPermanent(err) || attempts > retries {
			return attempts, err
		}
		if err := waitRetry(ctx, delay); err != nil {
			return attempts, err
		}
	}
}

// waitRetry 在重试前等待 delay, 并响应 context 取消
// delay 为 0 时仍然走一次 select, 这样已经取消的 context 不会因为零延迟而被忽略
//
// waitRetry waits delay before a retry and honours a context cancellation
// A zero delay still goes through the select once, so an already cancelled context is not ignored
// just because there is nothing to wait for
func waitRetry(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

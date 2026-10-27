package cli

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/notify"
	"github.com/MarchSnow-1/TaskPush/internal/state"
)

// ---------------------------------------------------------------------------
// finish 命令: 读取计时起点 → 以迟滞变量渲染正文 → 发送 → 成功删除状态文件
// 失败与 dry-run 都保留状态文件, 让重试可以基于同一起点再次计算耗时
//
// The finish command: read the start time, render the content with the elapsed variables, send and
// remove the state file on success
// A failure and a dry run both keep the state file, which lets a retry recompute the elapsed time
// from the same origin
// ---------------------------------------------------------------------------

// runFinish 执行一次计时收尾发送
// version 一路传到 notify.Options.Version, 与 send 走同一个版本来源
//
// runFinish performs one timed finish send
// version travels down to notify.Options.Version, the same version source send uses
func runFinish(ctx context.Context, opts options, p *printer, version string) invocation {
	// 先解析配置/内联渠道, 再计算状态路径并读取起点
	// 配置错误因此先于状态文件错误报出, 调用方不用等到读状态才发现配置写错
	//
	// The configuration or inline channel resolves first, then the state path is computed and the
	// start time read
	// A configuration failure therefore surfaces before a state file failure, so a caller does not
	// have to reach the state read to learn the configuration is wrong
	in, err := resolveInput(&opts)
	if err != nil {
		return invocation{code: exitFailure, err: err}
	}
	path, err := statePath(&opts)
	if err != nil {
		return invocation{code: exitFailure, err: err}
	}
	startedAt, err := state.Read(path)
	if err != nil {
		return invocation{code: exitFailure, err: finishReadError(err, path)}
	}

	now := time.Now()
	if startedAt.After(now) {
		// 起点在未来时只发出警告, 起点保持原值: 计时层按 now-start 计算并把负值归零, 因此耗时正好是 0
		// 把起点改成 now 会让随后的一次取时拉开几十毫秒, 耗时就不再是 0
		//
		// A start time in the future only raises the warning while the origin keeps its value: the
		// timing layer computes now minus start and clamps a negative result to zero, so the elapsed
		// time comes out as exactly 0
		// Rewriting the origin to now would let the later clock read widen the gap by a few dozen
		// milliseconds, and the elapsed time would stop being 0
		p.warn("state.warn.future", map[string]any{"Time": startedAt.Local().Format(time.RFC3339)})
	}

	title, body, err := readContent(&opts)
	if err != nil {
		return invocation{code: exitFailure, err: err}
	}
	if err := ctx.Err(); err != nil {
		return invocation{code: exitFailure, err: err}
	}

	p.info("cli.info.using_config", map[string]any{"Source": in.label})
	results, sendErr := notify.Send(ctx, in.defaults, in.channels, sendOptions(&opts, title, body, startedAt, version, p))
	if sendErr != nil {
		if len(results) > 0 {
			p.writeResults(results)
		}
		p.info("cli.info.finish_kept", map[string]any{"Path": path})
		return invocation{code: exitFailure, err: sendErr}
	}

	failed := p.writeResults(results)
	if opts.DryRun {
		// dry-run 也要按渠道结果定退出码: 它是完整渲染与校验, 渠道坏了就该让流水线看见
		//
		// A dry run still takes its exit code from the channel results, since it performs the full
		// render and validation and a broken channel has to be visible to the pipeline
		if failed > 0 {
			p.info("cli.info.finish_kept", map[string]any{"Path": path})
			return invocation{code: exitFailure}
		}
		p.info("cli.result.ok_dry_run", nil)
		p.info("cli.info.finish_kept", map[string]any{"Path": path})
		return invocation{code: exitOK}
	}
	p.writeSummary(len(results), failed)
	if failed > 0 {
		// 部分失败时保留状态文件, 但重试会再次发送到已成功的渠道
		// 这条提示让用户知道重跑不是增量补发
		//
		// A partial failure keeps the state file, while a retry sends to the channels that already
		// succeeded once more
		// The note tells the user that a rerun is not an incremental resend
		p.warn("cli.warn.finish_rerun", nil)
		p.info("cli.info.finish_kept", map[string]any{"Path": path})
		return invocation{code: exitFailure}
	}
	if opts.KeepState {
		p.info("cli.info.finish_kept", map[string]any{"Path": path})
		return invocation{code: exitOK}
	}
	if err := state.Remove(path); err != nil {
		return invocation{code: exitFailure, err: err}
	}
	p.info("cli.info.finish_deleted", map[string]any{"Path": path})
	return invocation{code: exitOK}
}

// finishReadError 把状态文件读取失败映射成用户可见的错误
// 文件不存在渲染成 state.error.missing 并提示先跑 start, 其余错误原样返回
//
// finishReadError maps a failed state file read into a user-visible error
// A missing file renders as state.error.missing with a hint to run start first, and any other
// error passes through
func finishReadError(err error, path string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return &apperr.Problem{
			ID:   "state.error.missing",
			Data: map[string]any{"Path": path},
		}
	}
	return err
}

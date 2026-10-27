package cli

import (
	"context"
	"time"

	"github.com/MarchSnow-1/TaskPush/internal/notify"
)

// ---------------------------------------------------------------------------
// send 命令: 解析输入 → 调度层发送 → 按渠道顺序输出结果
// 渠道级失败只影响退出码, 变量/内容/无渠道之类的全局错误直接返回给 Run
//
// The send command: resolve the input, hand it to the scheduler and print the results in channel
// order
// A channel failure affects the exit code only, while a global error such as a variable, content or
// no-channel failure goes back to Run
// ---------------------------------------------------------------------------

// runSend 执行一次发送
// dry-run 时结果只含预览, 并用一条 INFO 行说明未实际发送, 不输出发送汇总
// version 一路传到 notify.Options.Version, 它是 User-Agent 的版本来源
//
// runSend performs one send
// A dry run returns previews only and states in an INFO line that nothing was sent, with no send
// summary
// version travels down to notify.Options.Version, which is where the User-Agent version comes from
func runSend(ctx context.Context, opts options, p *printer, version string) invocation {
	in, err := resolveInput(&opts)
	if err != nil {
		return invocation{code: exitFailure, err: err}
	}
	title, body, err := readContent(&opts)
	if err != nil {
		return invocation{code: exitFailure, err: err}
	}
	if err := ctx.Err(); err != nil {
		return invocation{code: exitFailure, err: err}
	}

	p.info("cli.info.using_config", map[string]any{"Source": in.label})
	results, sendErr := notify.Send(ctx, in.defaults, in.channels, sendOptions(&opts, title, body, time.Time{}, version, p))
	if sendErr != nil {
		return sendFailure(results, sendErr, p)
	}
	failed := p.writeResults(results)
	if opts.DryRun {
		// dry-run 也要按渠道结果定退出码: 它是完整渲染与校验, 渠道坏了就该让流水线看见
		//
		// A dry run still takes its exit code from the channel results, since it performs the full
		// render and validation and a broken channel has to be visible to the pipeline
		if failed > 0 {
			return invocation{code: exitFailure}
		}
		p.info("cli.result.ok_dry_run", nil)
		return invocation{code: exitOK}
	}
	p.writeSummary(len(results), failed)
	if failed > 0 {
		return invocation{code: exitFailure}
	}
	return invocation{code: exitOK}
}

// sendFailure 在调度层返回全局错误时输出已经收集到的渠道结果并返回错误
// 取消之外的全局错误一律退出 1, 取消由 Run 转成 130
//
// sendFailure prints the channel results collected so far when the scheduler returns a global error
// and passes the error on
// A global error other than cancellation exits 1, and Run turns a cancellation into 130
func sendFailure(results []notify.Result, sendErr error, p *printer) invocation {
	if len(results) > 0 {
		p.writeResults(results)
	}
	return invocation{code: exitFailure, err: sendErr}
}

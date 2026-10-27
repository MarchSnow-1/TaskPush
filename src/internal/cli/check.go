package cli

import (
	"bytes"
	"context"
	"errors"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/config"
	"github.com/MarchSnow-1/TaskPush/internal/notify"
)

// ---------------------------------------------------------------------------
// check 命令: 解析输入 → 逐渠道校验配置与内容 → 输出表格
// check 不发送, 只有警告时退出 0; --all 把 Root.Deferred 里的禁用渠道展开错误一并列入表格
//
// The check command: resolve the input, validate every channel's configuration and content and
// print the table
// check sends nothing and exits 0 with warnings alone, while --all adds the expansion errors of
// disabled channels from Root.Deferred to the table
// ---------------------------------------------------------------------------

// isNoChannels 报告错误是否为配置层或调度层的无渠道错误
//
// isNoChannels reports whether the error is the no-channel failure of the configuration or
// scheduling layer
func isNoChannels(err error) bool {
	var problem *apperr.Problem
	if !errors.As(err, &problem) {
		return false
	}
	return problem.ID == "config.error.no_channels" || problem.ID == "notify.error.no_channels"
}

// runCheck 执行一次配置校验
//
// runCheck performs one configuration check
func runCheck(ctx context.Context, opts options, p *printer) invocation {
	in, err := resolveInput(&opts)
	if err != nil {
		// 没有可校验的渠道按提示处理并退出 0, 与 check 的警告语义一致
		// 例外是 --all: 禁用渠道的展开错误在 Root.Deferred 里, 此时仍要把它列出来
		//
		// No channel to check earns an informational note and exits 0, matching the warning-only
		// semantics of check
		// The --all case is the exception, since the expansion errors of disabled channels live in
		// Root.Deferred and still have to be listed
		if isNoChannels(err) {
			deferred := deferredOf(opts, in)
			targets := disabledTargets(opts, in)
			if len(deferred) == 0 && len(targets) == 0 {
				p.info("cli.info.check_no_channels", nil)
				return invocation{code: exitOK}
			}
			p.info("cli.info.using_config", map[string]any{"Source": in.label})
			results, checkErr := notify.Check(ctx, in.defaults, targets, checkOptions(&opts, p))
			if checkErr != nil {
				if len(results) > 0 {
					p.writeCheckRows(results, nil)
				}
				return invocation{code: exitFailure, err: checkErr}
			}
			if failed := p.writeCheckRows(results, deferred); failed > 0 {
				return invocation{code: exitFailure}
			}
			return invocation{code: exitOK}
		}
		return invocation{code: exitFailure, err: err}
	}
	if err := ctx.Err(); err != nil {
		return invocation{code: exitFailure, err: err}
	}

	p.info("cli.info.using_config", map[string]any{"Source": in.label})
	// 标题与正文在 check 里是占位内容, 先说清楚, 免得用户以为校验的是真实内容
	//
	// The title and body are placeholder content under check, and saying so up front keeps a user
	// from thinking the real content was validated
	p.warn("cli.warn.check_placeholder", nil)
	targets := in.channels
	if opts.All {
		targets = append(append([]config.Channel{}, in.channels...), disabledTargets(opts, in)...)
	}
	results, checkErr := notify.Check(ctx, in.defaults, targets, checkOptions(&opts, p))
	if checkErr != nil {
		if len(results) > 0 {
			p.writeCheckRows(results, nil)
		}
		return invocation{code: exitFailure, err: checkErr}
	}

	deferred := deferredOf(opts, in)
	failed := p.writeCheckRows(results, deferred)
	if failed > 0 {
		return invocation{code: exitFailure}
	}
	return invocation{code: exitOK}
}

// disabledTargets 取出 --all 要一并校验的禁用渠道
// 只选 --channel 指定的子集, 未指定时取全部禁用渠道; 省略 settings 的禁用渠道跳过,
// 因为规格允许它们不写 settings, 没有可校验的内容
//
// disabledTargets picks the disabled channels --all has to validate as well
// Only the subset named by --channel is taken, every disabled channel when it names none, and a
// disabled channel without settings is skipped because the spec allows one to omit them, which
// leaves nothing to validate
func disabledTargets(opts options, in *input) []config.Channel {
	if !opts.All || in == nil || in.root == nil {
		return nil
	}
	wanted := map[string]bool{}
	for _, id := range in.requested {
		wanted[id] = true
	}
	out := make([]config.Channel, 0, len(in.root.Channels))
	for _, channel := range in.root.Channels {
		if channel.IsEnabled() {
			continue
		}
		if len(wanted) > 0 && !wanted[channel.ID] {
			continue
		}
		if len(bytes.TrimSpace(channel.Settings)) == 0 {
			continue
		}
		out = append(out, channel)
	}
	return out
}

// deferredOf 取 --all 需要展示的延后错误, 只在配置模式下且 root 已加载时存在
//
// deferredOf picks the deferred problems --all has to show, which only exist in configuration mode
// once the root is loaded
func deferredOf(opts options, in *input) []apperr.Problem {
	if !opts.All || in == nil || in.root == nil {
		return nil
	}
	return in.root.Deferred
}

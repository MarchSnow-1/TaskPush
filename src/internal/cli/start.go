package cli

import (
	"context"
	"os"
	"time"

	"github.com/MarchSnow-1/TaskPush/internal/state"
)

// ---------------------------------------------------------------------------
// start 命令: 计算状态路径 → 已存在时提示覆盖 → 写入计时起点
// start 不读取配置内容, --config 只用于定位状态目录; --dry-run 只打印不落盘
//
// The start command: compute the state path, note an existing file and write the start time
// start never reads configuration content, since --config only locates the state directory, and
// --dry-run prints without writing
// ---------------------------------------------------------------------------

// runStart 写入计时起点
// 覆盖是 start 的语义, 因此已存在时提示 INFO 而不要求 --force
//
// runStart records the start time
// Overwriting is the meaning of start, so an existing file earns an INFO note rather than a demand
// for --force
func runStart(ctx context.Context, opts options, p *printer) invocation {
	path, err := statePath(&opts)
	if err != nil {
		return invocation{code: exitFailure, err: err}
	}
	now := time.Now()
	if _, err := os.Stat(path); err == nil {
		p.info("state.info.overwritten", map[string]any{"Path": path})
	}
	if opts.DryRun {
		p.info("cli.info.start_dry_run", map[string]any{
			"Path": path,
			"Time": now.Local().Format(time.RFC3339),
		})
		return invocation{code: exitOK}
	}
	if err := state.Write(path, now); err != nil {
		return invocation{code: exitFailure, err: err}
	}
	p.info("cli.info.start_written", map[string]any{
		"Path": path,
		"Time": now.Local().Format(time.RFC3339),
	})
	return invocation{code: exitOK}
}

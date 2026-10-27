// TaskPush 的进程入口: 只负责装配渠道, 接管信号, 把参数交给 cli.Run 并退出
// 业务逻辑全部在 internal/cli 及其下游包, main 不解析参数也不打印文案
//
// The TaskPush process entry: it assembles the channels, takes over signals, hands the arguments to
// cli.Run and exits
// All business logic lives in internal/cli and the packages below it, so main parses no argument and
// prints no copy
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/MarchSnow-1/TaskPush/internal/channel/all"
	"github.com/MarchSnow-1/TaskPush/internal/cli"
)

// version 由构建脚本通过 -ldflags "-X main.version=<ver>" 注入, 未注入时显示 dev
//
// version is injected by the build script through -ldflags "-X main.version=<ver>" and displays
// dev when it was not injected
var version = ""

// main 运行一次 CLI, 并把上下文取消映射为 cli.Run 的退出码
// SIGINT 与 SIGTERM 都交给 signal.NotifyContext, os.Exit 只出现在这里
//
// main runs one CLI invocation and maps context cancellation onto the exit code of cli.Run
// Both SIGINT and SIGTERM go through signal.NotifyContext, and os.Exit appears here only
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(cli.Run(ctx, os.Args[1:], version, os.Stdout, os.Stderr))
}

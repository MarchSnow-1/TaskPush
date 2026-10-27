package cli

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/i18n"
)

// ---------------------------------------------------------------------------
// 入口: Run 负责语言扫描, 解析, 分发与退出码, 不调用 os.Exit
// 退出码 0 成功, 1 运行时错误, 2 用法错误, 130 上下文取消
//
// The entry point: Run performs the language scan, parsing, dispatch and exit code, and never calls
// os.Exit
// The exit codes are 0 for success, 1 for a runtime failure, 2 for a usage error and 130 for a
// cancelled context
// ---------------------------------------------------------------------------

const (
	// exitOK 表示成功, check 只有警告时同样是 0
	//
	// exitOK means success, and a check with warnings only also returns it
	exitOK = 0
	// exitFailure 表示配置, 渠道, 发送或状态错误
	//
	// exitFailure means a configuration, channel, send or state failure
	exitFailure = 1
	// exitUsage 表示命令行用法错误
	//
	// exitUsage means a command-line usage error
	exitUsage = 2
	// exitCanceled 表示上下文取消, 与 shell 对 SIGINT 的惯例一致
	//
	// exitCanceled means a cancelled context, matching the shell convention for SIGINT
	exitCanceled = 130
)

// Run 执行一次 CLI 调用并返回退出码
// args 不含程序名; version 为空时显示 dev; 输出全部写入注入的 stdout 与 stderr
//
// Run performs one CLI invocation and returns the exit code
// args carries no program name, an empty version displays dev, and every output goes to the injected
// stdout and stderr
func Run(ctx context.Context, args []string, version string, stdout, stderr io.Writer) int {
	if ctx == nil {
		ctx = context.Background()
	}

	lang, _ := scanLang(args)
	translator, err := i18n.New(lang)
	if err != nil {
		return writeLanguageError(err, stdout, stderr)
	}
	p := newPrinter(translator, stdout, stderr)

	parsed, problem := parseArgs(args)
	if problem != nil {
		p.globalError(problem)
		return exitUsage
	}
	if problem := validateInputFlags(&parsed.opts); problem != nil {
		p.globalError(problem)
		return exitUsage
	}

	if parsed.opts.Command == "" {
		switch {
		case parsed.ShowVersion:
			writeVersion(stdout, translator, version)
		default:
			writeGlobalHelp(stdout, translator)
		}
		return exitOK
	}
	if parsed.Help {
		return writeHelp(stdout, translator, &parsed.opts)
	}
	if parsed.ShowVersion {
		// --version 是全局 flag: 即便同时给了命令也只输出版本, 避免把一次误写的
		// `send --version` 当成真实发送
		//
		// --version is a global flag: even alongside a command it only prints the version, which keeps
		// a mistyped `send --version` from turning into a real send
		writeVersion(stdout, translator, version)
		return exitOK
	}

	return dispatch(ctx, parsed, p, version)
}

// writeLanguageError 处理语言错误
// 非法语言用英文输出并列出可用语言, 退出 2; 目录加载失败属于运行时错误, 退出 1
//
// writeLanguageError handles a language failure
// An unsupported language is reported in English with the available list and exits 2, while a
// catalog loading failure is a runtime error and exits 1
func writeLanguageError(err error, stdout, stderr io.Writer) int {
	english, newErr := i18n.New("en")
	if newErr != nil {
		// 英语目录都加载失败说明嵌入消息表本身有问题, 此时没有可用的本地化文案
		//
		// A failed English catalog means the embedded message table itself is broken, and no
		// localised copy is available
		return exitFailure
	}
	p := newPrinter(english, stdout, stderr)

	var unsupported *i18n.UnsupportedLanguageError
	if errors.As(err, &unsupported) {
		p.globalError(&apperr.Problem{
			ID: "cli.error.invalid_lang",
			Data: map[string]any{
				"Lang":      unsupported.Lang,
				"Available": strings.Join(unsupported.Supported, ", "),
			},
		})
		return exitUsage
	}
	p.globalError(err)
	return exitFailure
}

// validateInputFlags 校验与输入相关的组合: stdin 单一消费者, 标题正文互斥与可重复值格式
// 这些检查与命令无关, 放在分发之前让所有命令共享同一套规则
//
// validateInputFlags validates the input-related combinations: the single standard-input consumer,
// the mutually exclusive title and body flags, and the repeatable value shapes
// The checks are command independent and run before dispatch, so every command shares one rule set
func validateInputFlags(opts *options) *apperr.Problem {
	if problem := validateStdin(opts); problem != nil {
		return problem
	}
	if problem := validateTitles(opts); problem != nil {
		return problem
	}
	if problem := validatePayload(opts); problem != nil {
		return problem
	}
	if problem := validateVarPairs(opts.Vars); problem != nil {
		return problem
	}
	return validateSetPairs(opts.Sets)
}

// invocation 是一个命令处理函数的结果
// code 为退出码, err 为 nil 表示无全局错误
//
// invocation is the result of one command handler
// code carries the exit code and a nil err means there was no global error
type invocation struct {
	code int
	err  error
}

// dispatch 调用命令处理函数, 并在返回错误时把可取消的错误映射成 130
//
// dispatch calls the command handler and maps a cancellable error to 130 on the way out
func dispatch(ctx context.Context, parsed *parsedArgs, p *printer, version string) int {
	result := runCommand(ctx, parsed, p, version)
	if code, ok := canceledExit(ctx, result.err); ok {
		p.globalError(&apperr.Problem{ID: "cli.error.context_canceled", Cause: result.err})
		return code
	}
	if result.err != nil {
		p.globalError(result.err)
	}
	return result.code
}

// canceledExit 判断错误或上下文是否表示取消
// 取消优先于命令自身的退出码, 让 Ctrl+C 与超时都统一成 130
//
// canceledExit reports whether the error or the context means cancellation
// Cancellation outranks the command's own exit code, which turns both Ctrl+C and a deadline into 130
func canceledExit(ctx context.Context, err error) (int, bool) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return exitCanceled, true
	}
	if ctx != nil && (errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded)) {
		return exitCanceled, true
	}
	return 0, false
}

// runCommand 分发到具体命令处理函数
// 版本号与译文查表函数经入参交给 send/finish/check
//
// runCommand routes to the concrete command handler
// The version and the translation lookup reach send, finish and check as arguments
func runCommand(ctx context.Context, parsed *parsedArgs, p *printer, version string) invocation {
	switch parsed.opts.Command {
	case "send":
		return runSend(ctx, parsed.opts, p, version)
	case "finish":
		return runFinish(ctx, parsed.opts, p, version)
	case "start":
		return runStart(ctx, parsed.opts, p)
	case "check":
		return runCheck(ctx, parsed.opts, p)
	case "list":
		return runList(parsed.opts, p)
	case "types":
		return runTypes(parsed, p)
	case "init":
		return runInit(parsed, p)
	case "version":
		writeVersion(p.stdout, p.translator, version)
		return invocation{code: exitOK}
	case "help":
		return runHelpCommand(parsed, p)
	default:
		return invocation{
			code: exitUsage,
			err: &apperr.Problem{
				ID: "cli.error.unknown_command",
				Data: map[string]any{
					"Command":   parsed.opts.Command,
					"Available": strings.Join(commandNames, ", "),
				},
			},
		}
	}
}

// writeHelp 输出全局帮助或某个命令的帮助
//
// writeHelp writes the global help or the help of one command
func writeHelp(stdout io.Writer, t *i18n.Translator, opts *options) int {
	if opts.Command == "" {
		writeGlobalHelp(stdout, t)
		return exitOK
	}
	spec, ok := commandIndex()[opts.Command]
	if !ok {
		writeGlobalHelp(stdout, t)
		return exitOK
	}
	writeCommandHelp(stdout, t, spec)
	return exitOK
}

// runHelpCommand 处理 help 命令, 可选位置参数是命令名
// 未知命令名按未知命令报出, 退出 2
//
// runHelpCommand handles the help command, whose optional positional is a command name
// An unknown command name is reported as an unknown command and exits 2
func runHelpCommand(parsed *parsedArgs, p *printer) invocation {
	if len(parsed.positionals) == 0 {
		writeGlobalHelp(p.stdout, p.translator)
		return invocation{code: exitOK}
	}
	target := parsed.positionals[0]
	spec, ok := commandIndex()[target]
	if !ok {
		return invocation{
			code: exitUsage,
			err: &apperr.Problem{
				ID: "cli.error.unknown_command",
				Data: map[string]any{
					"Command":   target,
					"Available": strings.Join(commandNames, ", "),
				},
			},
		}
	}
	writeCommandHelp(p.stdout, p.translator, spec)
	return invocation{code: exitOK}
}

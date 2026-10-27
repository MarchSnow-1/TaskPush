package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/config"
	"github.com/MarchSnow-1/TaskPush/internal/notify"
	"github.com/MarchSnow-1/TaskPush/internal/state"
)

// ---------------------------------------------------------------------------
// 输入解析: 配置来源, 内联渠道, 标题正文与状态路径都集中在这里
// stdin 由包级接线点注入, 一次调用最多一个消费者, 保证输入不会被读走两次
//
// Input resolution: the configuration source, the inline channel, the title and body and the state
// path all live here
// Standard input arrives through a package-level wiring point, and one invocation has at most one
// consumer, which keeps the input from being read twice
// ---------------------------------------------------------------------------

// stdinReader 是标准输入接线点, 默认取进程标准输入, 测试可替换
//
// stdinReader is the standard-input wiring point, defaulting to the process standard input and
// replaceable by tests
var stdinReader io.Reader = os.Stdin

// lookupEnv 是环境变量接线点, 默认读进程环境, 测试可替换
//
// lookupEnv is the environment wiring point, defaulting to the process environment and replaceable
// by tests
var lookupEnv = os.LookupEnv

// workingDir 是工作目录接线点, 默认取进程工作目录, 测试可替换
//
// workingDir is the working-directory wiring point, defaulting to the process working directory and
// replaceable by tests
var workingDir = os.Getwd

// inlineSourceLabel 是内联模式在 INFO 行里的来源标签
//
// inlineSourceLabel is the source label inline mode writes in its INFO line
const inlineSourceLabel = "inline"

// sourceHint 列出配置模式之外的输入方式, 供 source_missing 的占位符使用
// 与 config.ResolveSource 的提示保持一致, 避免两处文案漂移
//
// sourceHint lists the input forms besides configuration mode for the source_missing placeholder
// It matches the hint from config.ResolveSource, which keeps the two copies from drifting apart
const sourceHint = "--webhook, --type/--set, --config or TASKPUSH_CONFIG_JSON"

// maxContentBytes 是标题与正文文件的读取上限, 与配置来源的 1MB 上限保持一致
//
// maxContentBytes caps a title or body file read, matching the 1MB cap of the configuration sources
const maxContentBytes = 1 << 20

// defaultInlineTimeout 是内联模式使用的请求超时, 与 config.Load 的缺省值一致
//
// defaultInlineTimeout is the request timeout inline mode uses, matching the default of config.Load
const defaultInlineTimeout = 15 * time.Second

// errTooLarge 是标题正文超过上限时的内部原因
//
// errTooLarge is the internal reason for a title or body above the cap
var errTooLarge = fmt.Errorf("input exceeds the 1MB cap")

// input 是一次命令的解析结果: 渠道, 生效默认值与来源标签
//
// input is the resolved input of one command: the channels, the defaults in force and the source
// label
type input struct {
	root     *config.Root
	defaults config.Defaults
	channels []config.Channel
	label    string
	inline   bool
	// requested 是 --channel 指定的渠道 ID, 供 check --all 判断禁用渠道是否在所选子集里
	//
	// requested holds the channel IDs named by --channel, which lets check --all tell whether a
	// disabled channel sits inside the requested subset
	requested []string
}

// resolveInput 按模式构造渠道列表
// 内联模式跳过配置来源与 TASKPUSH_CONFIG_JSON; 配置模式走 ResolveSource, Load 与 Select
//
// resolveInput builds the channel list according to the mode
// Inline mode skips the configuration source and TASKPUSH_CONFIG_JSON, while configuration mode
// runs ResolveSource, Load and Select
func resolveInput(opts *options) (*input, error) {
	if isInline(opts) {
		return resolveInline(opts)
	}
	return resolveFromConfig(opts)
}

// resolveInline 构造一个内联渠道
// 内联模式没有配置文件, 因此超时取契约缺省值, 重试与代理保持零值
//
// resolveInline builds the single inline channel
// Inline mode has no configuration file, so the timeout takes the contract default while retries
// and the proxy stay at their zero values
func resolveInline(opts *options) (*input, error) {
	built, err := config.BuildInline(config.InlineOptions{
		Webhook:   opts.Webhook,
		Type:      opts.Type,
		Payload:   opts.Payload,
		Sets:      opts.Sets,
		LookupEnv: lookupEnv,
	})
	if err != nil {
		return nil, err
	}
	return &input{
		defaults: config.Defaults{Timeout: defaultInlineTimeout},
		channels: []config.Channel{built},
		label:    inlineSourceLabel,
		inline:   true,
	}, nil
}

// resolveFromConfig 解析配置来源, 加载并按 --channel 选择渠道
//
// resolveFromConfig resolves the configuration source, loads it and selects channels by --channel
func resolveFromConfig(opts *options) (*input, error) {
	cwd, err := workingDir()
	if err != nil {
		return nil, &apperr.Problem{
			ID:    "config.error.source_missing",
			Data:  map[string]any{"Hint": sourceHint},
			Cause: err,
		}
	}
	source, err := config.ResolveSource(config.SourceOptions{
		Path:      opts.ConfigPath,
		CWD:       cwd,
		Stdin:     stdinReader,
		LookupEnv: lookupEnv,
	})
	if err != nil {
		return nil, err
	}
	root, err := config.Load(source)
	if err != nil {
		return nil, err
	}
	selected, err := root.Select(opts.Channels)
	if err != nil {
		// 无渠道时仍把已加载的 root 带回去: check --all 要靠 Root.Deferred 报出禁用渠道的展开错误
		//
		// An empty selection still carries the loaded root back, because check --all needs
		// Root.Deferred to report the expansion errors of disabled channels
		if isNoChannels(err) {
			return &input{
				root:      root,
				defaults:  root.Defaults(),
				label:     source.Label(),
				requested: opts.Channels,
			}, err
		}
		return nil, err
	}
	return &input{
		root:      root,
		defaults:  root.Defaults(),
		channels:  selected,
		label:     source.Label(),
		requested: opts.Channels,
	}, nil
}

// sendOptions 把解析后的 flag 转成调度层入参
// Elapsed 不为零时进入计时模式, 由 notify 生成 elapsed 系列变量
// version 与 p.translator 一并写入, 由调度层原样使用
//
// sendOptions turns the parsed flags into the scheduling arguments
// A non-zero start time enters timing mode, and notify derives the elapsed variables from it
// version and p.translator are written in too, and the scheduling layer uses them as given
func sendOptions(opts *options, title, body string, start time.Time, version string, p *printer) notify.Options {
	return notify.Options{
		Title:     title,
		Body:      body,
		VarPairs:  opts.Vars,
		VarFile:   opts.VarFile,
		DryRun:    opts.DryRun,
		Parallel:  opts.Parallel,
		StartTime: start,
		Timed:     !start.IsZero(),
		Version:   version,
		Translate: translateLookup(p),
	}
}

// checkOptions 把 check 的 flag 与译文查表函数转成调度层入参
// check 不发送, 因此没有版本号
//
// checkOptions turns the check flags and the translation lookup into the scheduling arguments
// check sends nothing, so it carries no version
func checkOptions(opts *options, p *printer) notify.CheckOptions {
	return notify.CheckOptions{
		VarPairs:  opts.Vars,
		VarFile:   opts.VarFile,
		Translate: translateLookup(p),
	}
}

// translateLookup 从 printer 持有的译员取译文查表函数, printer 为 nil 时返回 nil
// 返回 nil 让渠道回退到自己的语言无关常量, 与 Message.Translate 为 nil 的语义一致
//
// translateLookup takes the translation lookup from the translator the printer holds, and a nil
// printer yields nil
// A nil result makes a channel fall back to its own language-neutral constant, matching the meaning
// of a nil Message.Translate
func translateLookup(p *printer) func(key string) string {
	if p == nil || p.translator == nil {
		return nil
	}
	translator := p.translator
	return func(key string) string { return translator.T(key, nil) }
}

// readContent 解析标题与正文: --title/--body 优先, 否则读对应的文件, "-" 读 stdin
// 两者都没有时返回空串, 由模板渲染按空内容处理
// 读取结果先做一次环境变量展开, 让 --title / --body 与配置里的字符串遵循同一套 ${VAR} 规则
//
// readContent resolves the title and body: --title and --body win, otherwise the matching file is
// read and "-" means stdin
// With neither given it returns empty strings, and rendering treats empty content accordingly
// The result passes through one environment-variable expansion, so --title and --body follow the
// same ${VAR} rules as a configuration string
func readContent(opts *options) (title, body string, err error) {
	title, err = readContentValue(opts.Title, opts.TitleFile, "--title", "--title-file")
	if err != nil {
		return "", "", err
	}
	body, err = readContentValue(opts.Body, opts.BodyFile, "--body", "--body-file")
	if err != nil {
		return "", "", err
	}
	return title, body, nil
}

// readContentValue 读取一个内容来源并展开其中的环境变量, 内联文本优先于文件
// 展开在读取之后、模板渲染之前进行, 因此 ${VAR} 与 {{var}} 可以在同一段文本里并存
//
// readContentValue reads one content source and expands the environment references inside it, with
// the inline text winning over the file
// The expansion runs after reading and before template rendering, so ${VAR} and {{var}} can sit in
// the same piece of text
func readContentValue(inline, path, flag, fileFlag string) (string, error) {
	raw, err := readContentRaw(inline, path, fileFlag)
	if err != nil {
		return "", err
	}
	expanded, err := config.ExpandEnvString(contentSourceLabel, flag, raw, lookupEnv)
	if err != nil {
		return "", err
	}
	return expanded, nil
}

// readContentRaw 读取一个内容来源的原文, 内联文本优先于文件
//
// readContentRaw reads the raw text of one content source, with the inline text winning over the file
func readContentRaw(inline, path, flag string) (string, error) {
	if inline != "" {
		return inline, nil
	}
	if path == "" {
		return "", nil
	}
	if path == "-" {
		data, err := io.ReadAll(io.LimitReader(stdinReader, maxContentBytes+1))
		if err != nil {
			return "", contentReadProblem(flag, path, err)
		}
		if len(data) > maxContentBytes {
			return "", contentReadProblem(flag, path, errTooLarge)
		}
		return string(data), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", contentReadProblem(flag, path, err)
	}
	if len(data) > maxContentBytes {
		return "", contentReadProblem(flag, path, errTooLarge)
	}
	return string(data), nil
}

// contentSourceLabel 是标题正文环境变量展开的错误来源标签
//
// contentSourceLabel is the error-source label of the title and body environment expansion
const contentSourceLabel = "content"

// contentReadProblem 构造标题正文读取失败的 Problem, 复用 config.error.read
//
// contentReadProblem builds the Problem for a failed title or body read, reusing config.error.read
func contentReadProblem(flag, path string, cause error) *apperr.Problem {
	detail := ""
	if cause != nil {
		detail = cause.Error()
	}
	return &apperr.Problem{
		ID: "config.error.read",
		Data: map[string]any{
			"Source": flag + ":" + path,
			"Detail": detail,
		},
		Cause: cause,
	}
}

// statePath 计算状态文件路径
// --state-file 覆盖一切; 否则 --config 指出的文件同目录; 内联模式, env 来源与默认配置都落在工作目录
// start 上的 --config - 没有可用目录, 解析阶段已按用法错误拦下
//
// statePath computes the state file path
// --state-file overrides everything, otherwise the directory of the file named by --config applies,
// and inline mode, the environment source and the default configuration all land in the working
// directory
// A --config - on start has no usable directory, which the parse stage already rejects as a usage
// error
func statePath(opts *options) (string, error) {
	if opts.StateFile != "" {
		return opts.StateFile, nil
	}
	cwd, err := workingDir()
	if err != nil {
		return "", &apperr.Problem{
			ID: "state.error.write",
			Data: map[string]any{
				"Path":   state.DefaultPath(""),
				"Detail": err.Error(),
			},
			Cause: err,
		}
	}
	dir := cwd
	if !isInline(opts) && opts.ConfigPath != "" && opts.ConfigPath != "-" {
		dir = filepath.Dir(configPathInCWD(opts.ConfigPath, cwd))
	}
	return state.DefaultPath(dir), nil
}

// configPathInCWD 把 --config 的相对路径按工作目录解析, 绝对路径原样返回
//
// configPathInCWD resolves a relative --config path against the working directory and returns an
// absolute path unchanged
func configPathInCWD(path, cwd string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(cwd, path)
}

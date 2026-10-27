package cli

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
)

// ---------------------------------------------------------------------------
// 参数解析: 先把 token 扫成 flag 与位置参数, 再按命令表校验可用 flag 与位置参数数量
// 所有失败都返回带消息键的 Problem, 由 Run 按当前语言渲染并统一退出 2
//
// Parsing the arguments: the tokens are scanned into flags and positional arguments first, then the
// command table validates which flags the command accepts and how many positionals it takes
// Every failure returns a Problem carrying a message key, which Run renders in the active language
// and always exits with 2
// ---------------------------------------------------------------------------

// langFlagName 是语言 flag 的名字, 它在任何输出之前被单独扫描
//
// langFlagName is the name of the language flag, scanned on its own before any output
const langFlagName = "lang"

// parsedArgs 保存解析结果: 选项以及位置参数
// help/version/force 只影响分发与 init 行为, 因此留在本结构而不进冻结的 options
//
// parsedArgs holds the parse result: the options plus the positional arguments
// help, version and force affect dispatching and init only, so they stay on this struct rather than
// entering the frozen options
type parsedArgs struct {
	opts        options
	positionals []string
	Help        bool
	ShowVersion bool
	Force       bool
}

// rawFlag 是一次 flag 出现记录
// HasValue 区分"没有取值"与"取值是空串", Dash 标记后一个 token 以 -- 开头因而必须改用 = 形式
//
// rawFlag records one flag occurrence
// HasValue tells "no value" from "an empty value", and Dash marks a following token starting with
// -- that therefore has to be written with = instead
type rawFlag struct {
	Name     string
	Value    string
	HasEq    bool
	HasValue bool
	Dash     bool
	Short    bool
}

// scanLang 在解析之前扫描 --lang, 让帮助与用法错误都能用所选语言输出
// 只取第一次出现的取值, 重复仍由完整解析按 duplicate_flag 报出
//
// scanLang scans --lang before anything else, which lets help and usage errors use the chosen
// language
// It takes the first occurrence, while a repeat is still reported by the full parse as
// duplicate_flag
func scanLang(args []string) (string, bool) {
	terminated := false
	for i := 0; i < len(args); i++ {
		token := args[i]
		if terminated {
			continue
		}
		if token == "--" {
			terminated = true
			continue
		}
		if !strings.HasPrefix(token, "--") {
			continue
		}
		body := token[2:]
		name := body
		value := ""
		hasEq := false
		if at := strings.IndexByte(body, '='); at >= 0 {
			name = body[:at]
			value = body[at+1:]
			hasEq = true
		}
		// 大小写差异交给完整解析按未知 flag 报错, 语言扫描只认精确的 --lang
		// 否则 --Lang=zh-CN 会出现"语言已切换却报未知 flag"的割裂输出
		//
		// A case variant is left to the full parse, which reports it as an unknown flag, and this
		// scan accepts the exact --lang only
		// Otherwise --Lang=zh-CN switches the language and still fails as an unknown flag, which
		// reads as a contradiction
		if name != langFlagName {
			continue
		}
		if hasEq {
			return value, true
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
			return args[i+1], true
		}
		// 缺值时无法确定语言, 交给完整解析在默认语言下报 missing_value
		//
		// Without a value the language is unknown, so the full parse reports missing_value in the
		// default language
		return "", true
	}
	return "", false
}

// parseArgs 解析参数并返回选项或一个用法错误
// 第一个位置参数是命令, flag 可以出现在命令前后; -- 之后的 token 全部视为位置参数
//
// parseArgs parses the arguments and returns the options or one usage error
// The first positional is the command and flags may appear before or after it, while everything
// after -- counts as a positional
func parseArgs(args []string) (*parsedArgs, *apperr.Problem) {
	flagsByRaw, positionals, problem := scanTokens(args)
	if problem != nil {
		return nil, problem
	}

	command := ""
	if len(positionals) > 0 {
		command = positionals[0]
	}
	if command == "" {
		if err := unknownOrMissingCommand(flagsByRaw); err != nil {
			return nil, err
		}
		// 只有 --lang/--help/--version 时由 Run 决定帮助或版本, 空参数同样落到帮助
		//
		// With only --lang, --help or --version, Run decides between help and version, and a bare
		// invocation falls through to help as well
		result := &parsedArgs{opts: options{Command: "", Parallel: 1}}
		if err := applyFlags(flagsByRaw, commandSpec{Name: ""}, result); err != nil {
			return nil, err
		}
		return result, nil
	}

	spec, ok := commandIndex()[command]
	if !ok {
		return nil, &apperr.Problem{
			ID: "cli.error.unknown_command",
			Data: map[string]any{
				"Command":   command,
				"Available": strings.Join(commandNames, ", "),
			},
		}
	}

	if err := checkPositionals(spec, command, positionals[1:]); err != nil {
		return nil, err
	}
	result := &parsedArgs{opts: options{Command: command, Parallel: 1}}
	if len(positionals) > 1 {
		result.positionals = positionals[1:]
	}
	if err := applyFlags(flagsByRaw, spec, result); err != nil {
		return nil, err
	}
	if err := validateConflicts(&result.opts); err != nil {
		return nil, err
	}
	return result, nil
}

// unknownOrMissingCommand 处理没有命令的情况: 有命令 flag 时报告缺少命令, 否则返回 nil
// 全局 flag 单独出现不构成缺少命令, 例如 --help 与 --version 各自都能独立工作
//
// unknownOrMissingCommand handles the absence of a command: a command flag reports a missing
// command, while otherwise it returns nil
// A lone global flag does not mean a missing command, since --help and --version each work on their
// own
func unknownOrMissingCommand(seen []rawFlag) *apperr.Problem {
	index := flagIndex()
	for _, flag := range seen {
		spec, ok := index[flag.Name]
		if !ok {
			return &apperr.Problem{
				ID:   "cli.error.unknown_flag",
				Data: map[string]any{"Flag": "--" + flag.Name, "Command": ""},
			}
		}
		if len(spec.Commands) > 0 {
			return &apperr.Problem{ID: "cli.error.missing_command"}
		}
	}
	return nil
}

// scanTokens 把参数扫成 flag 记录与位置参数
// 未知 flag 只记录自身, 不消费后一个 token, 因为缺少命令上下文时无法判断它是否带值
//
// scanTokens scans the arguments into flag records and positional arguments
// An unknown flag records itself without consuming the next token, since without the command
// context there is no way to tell whether it takes a value
func scanTokens(args []string) ([]rawFlag, []string, *apperr.Problem) {
	index := flagIndex()
	var seen []rawFlag
	var positionals []string
	terminated := false

	for i := 0; i < len(args); i++ {
		token := args[i]
		if terminated {
			positionals = append(positionals, token)
			continue
		}
		if token == "--" {
			terminated = true
			continue
		}
		if token == "-h" {
			seen = append(seen, rawFlag{Name: "help", Short: true})
			continue
		}
		if !strings.HasPrefix(token, "--") {
			positionals = append(positionals, token)
			continue
		}

		body := token[2:]
		name := body
		value := ""
		hasEq := false
		if at := strings.IndexByte(body, '='); at >= 0 {
			name = body[:at]
			value = body[at+1:]
			hasEq = true
		}
		spec, ok := index[name]
		if !ok {
			seen = append(seen, rawFlag{Name: name, Value: value, HasEq: hasEq, HasValue: hasEq})
			continue
		}
		if spec.Kind == flagBool {
			seen = append(seen, rawFlag{Name: name, Value: value, HasEq: hasEq, HasValue: hasEq})
			continue
		}
		if hasEq {
			seen = append(seen, rawFlag{Name: name, Value: value, HasEq: true, HasValue: true})
			continue
		}
		if i+1 >= len(args) {
			seen = append(seen, rawFlag{Name: name})
			continue
		}
		next := args[i+1]
		if strings.HasPrefix(next, "--") {
			// 以 -- 开头的值必须写成 --flag=value, 由 applyFlags 报 value_dash
			// 该 token 不当成取值消费, 让后续解析仍把它读成 flag
			//
			// A value starting with -- has to be written as --flag=value, which applyFlags reports
			// as value_dash
			// The token is not consumed as a value, so the following parse still reads it as a flag
			seen = append(seen, rawFlag{Name: name, Value: next, Dash: true})
			continue
		}
		seen = append(seen, rawFlag{Name: name, Value: next, HasValue: true})
		i++
	}
	return seen, positionals, nil
}

// checkPositionals 校验除命令之外的位置参数数量
//
// checkPositionals validates the number of positional arguments beyond the command
func checkPositionals(spec commandSpec, command string, rest []string) *apperr.Problem {
	for _, arg := range rest {
		if spec.Positionals == 0 {
			return &apperr.Problem{
				ID:   "cli.error.extra_arg",
				Data: map[string]any{"Arg": arg, "Command": command},
			}
		}
	}
	if len(rest) > spec.Positionals {
		return &apperr.Problem{
			ID:   "cli.error.extra_arg",
			Data: map[string]any{"Arg": rest[spec.Positionals], "Command": command},
		}
	}
	return nil
}

// applyFlags 逐个校验 flag 并写入选项
// 未知 flag, 不适用 flag, 缺值, 重复与空值都在这里报出; 取第一个错误让输出稳定
//
// applyFlags validates the flags one by one and writes them into the options
// An unknown flag, a flag the command does not accept, a missing value, a repeat and an empty
// value are all reported here, with the first error winning so the output stays stable
func applyFlags(seen []rawFlag, spec commandSpec, result *parsedArgs) *apperr.Problem {
	index := flagIndex()
	counted := map[string]int{}

	for _, flag := range seen {
		flagSpec, ok := index[flag.Name]
		if !ok {
			return &apperr.Problem{
				ID:   "cli.error.unknown_flag",
				Data: map[string]any{"Flag": "--" + flag.Name, "Command": spec.Name},
			}
		}
		if !flagAppliesTo(flagSpec, spec.Name) {
			return &apperr.Problem{
				ID: "cli.error.flag_not_for_command",
				Data: map[string]any{
					"Flag":      "--" + flag.Name,
					"Command":   spec.Name,
					"Available": strings.Join(flagNames(spec.Name), ", "),
				},
			}
		}
		counted[flag.Name]++
		if flagSpec.Kind != flagRepeatString && counted[flag.Name] > 1 {
			return &apperr.Problem{
				ID:   "cli.error.duplicate_flag",
				Data: map[string]any{"Flag": "--" + flag.Name},
			}
		}
		if flagSpec.Kind == flagBool {
			if err := applyBoolFlag(flag, result); err != nil {
				return err
			}
			continue
		}
		if err := applyValueFlag(flag, flagSpec, result); err != nil {
			return err
		}
	}
	return nil
}

// applyBoolFlag 处理布尔 flag, 只接受 --flag 与 --flag=true/false/1/0 两种写法
//
// applyBoolFlag handles a boolean flag, accepting --flag and --flag=true/false/1/0 only
func applyBoolFlag(flag rawFlag, result *parsedArgs) *apperr.Problem {
	on := true
	if flag.HasEq {
		parsed, ok := parseBoolValue(flag.Value)
		if !ok {
			return &apperr.Problem{
				ID:   "cli.error.invalid_bool",
				Data: map[string]any{"Flag": "--" + flag.Name, "Value": flag.Value},
			}
		}
		on = parsed
	}
	switch flag.Name {
	case "help":
		result.Help = on
	case "version":
		result.ShowVersion = on
	case "dry-run":
		result.opts.DryRun = on
	case "keep-state":
		result.opts.KeepState = on
	case "all":
		result.opts.All = on
	case "force":
		result.Force = on
	}
	return nil
}

// parseBoolValue 解析布尔字面量, 大小写不敏感, 只接受 true/false/1/0
//
// parseBoolValue parses a boolean literal case-insensitively, accepting true, false, 1 and 0 only
func parseBoolValue(value string) (bool, bool) {
	switch strings.ToLower(value) {
	case "true", "1":
		return true, true
	case "false", "0":
		return false, true
	}
	return false, false
}

// applyValueFlag 处理取值 flag: 缺值, 以 -- 开头的值, 路径空值都在这里拦下
// --flag= 的显式空值对路径类 flag 是用法错误, 对其它取值 flag 按各自的语义继续校验
//
// applyValueFlag handles a value flag, catching a missing value, a value starting with -- and an
// empty path value
// An explicit empty value for a path flag is a usage error, while another value flag carries the
// empty string on to its own validation
func applyValueFlag(flag rawFlag, spec flagSpec, result *parsedArgs) *apperr.Problem {
	if flag.Dash {
		return &apperr.Problem{
			ID:   "cli.error.value_dash",
			Data: map[string]any{"Flag": "--" + flag.Name, "Value": flag.Value},
		}
	}
	if !flag.HasValue {
		return &apperr.Problem{
			ID:   "cli.error.missing_value",
			Data: map[string]any{"Flag": "--" + flag.Name},
		}
	}
	if (spec.Kind == flagPath || spec.Kind == flagInline) && flag.Value == "" {
		return &apperr.Problem{
			ID:   "cli.error.missing_value",
			Data: map[string]any{"Flag": "--" + flag.Name},
		}
	}
	return assignValue(flag, result)
}

// assignValue 把一个取值 flag 写入选项
//
// assignValue writes one value flag into the options
func assignValue(flag rawFlag, result *parsedArgs) *apperr.Problem {
	opts := &result.opts
	switch flag.Name {
	case "lang":
		opts.Lang = flag.Value
	case "config":
		opts.ConfigPath = flag.Value
	case "title":
		opts.Title = flag.Value
	case "title-file":
		opts.TitleFile = flag.Value
	case "body":
		opts.Body = flag.Value
	case "body-file":
		opts.BodyFile = flag.Value
	case "var":
		opts.Vars = append(opts.Vars, flag.Value)
	case "var-file":
		opts.VarFile = flag.Value
	case "channel":
		opts.Channels = append(opts.Channels, flag.Value)
	case "parallel":
		value, ok := parseParallel(flag.Value)
		if !ok {
			return &apperr.Problem{
				ID:   "cli.error.invalid_parallel",
				Data: map[string]any{"Value": flag.Value},
			}
		}
		opts.Parallel = value
	case "state-file":
		opts.StateFile = flag.Value
	case "webhook":
		opts.Webhook = flag.Value
	case "type":
		opts.Type = flag.Value
	case "set":
		opts.Sets = append(opts.Sets, flag.Value)
	case "payload":
		opts.Payload = flag.Value
	}
	return nil
}

// parseParallel 解析渠道并发数, 范围 1-8
//
// parseParallel parses the channel concurrency, in the range 1 to 8
func parseParallel(value string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < 1 || n > 8 {
		return 0, false
	}
	return n, true
}

// validateConflicts 校验模式判定与 flag 组合
// 内联模式由任一内联 flag 触发, 它与配置模式相关 flag 互斥, 其余非法组合逐条拒绝
//
// validateConflicts validates the mode decision and the flag combinations
// Any inline flag switches to inline mode, which excludes the configuration-mode flags, and every
// other illegal combination is refused one by one
func validateConflicts(opts *options) *apperr.Problem {
	switch {
	case opts.Command == "start" && opts.ConfigPath == "-":
		return &apperr.Problem{
			ID:   "config.error.source_missing",
			Data: map[string]any{"Hint": "--state-file"},
		}
	case opts.Command == "start":
		return nil
	}

	inline := isInline(opts)
	if !inline {
		return nil
	}
	switch {
	case opts.ConfigPath != "":
		return &apperr.Problem{ID: "cli.error.inline_config_conflict"}
	case len(opts.Channels) > 0:
		return &apperr.Problem{ID: "cli.error.inline_channel_conflict"}
	case opts.All:
		return &apperr.Problem{ID: "cli.error.inline_all_conflict"}
	}

	if len(opts.Sets) > 0 && opts.Type == "" && opts.Webhook == "" {
		return &apperr.Problem{ID: "cli.error.set_without_type"}
	}
	if opts.Payload != "" && opts.Webhook == "" && opts.Type != "webhook" {
		return &apperr.Problem{ID: "cli.error.payload_without_webhook"}
	}
	if opts.Webhook != "" && opts.Type != "" && opts.Type != "webhook" {
		return &apperr.Problem{ID: "cli.error.webhook_url_conflict"}
	}
	for _, entry := range opts.Sets {
		key, _, ok := strings.Cut(entry, "=")
		trimmed := strings.TrimSpace(key)
		switch {
		case !ok || trimmed == "":
			return &apperr.Problem{
				ID:   "config.inline.set",
				Data: map[string]any{"Value": entry},
			}
		case opts.Webhook != "" && trimmed == "url":
			return &apperr.Problem{ID: "cli.error.webhook_url_conflict"}
		case opts.Payload != "" && (trimmed == "body" || strings.HasPrefix(trimmed, "body.")):
			return &apperr.Problem{
				ID:   "config.inline.set_conflict",
				Data: map[string]any{"Key": trimmed, "Reason": "--payload"},
			}
		}
	}
	return nil
}

// isInline 报告是否出现任一内联 flag
//
// isInline reports whether any inline flag is present
func isInline(opts *options) bool {
	return opts.Webhook != "" || opts.Type != "" || len(opts.Sets) > 0 || opts.Payload != ""
}

// validatePayload 校验 --payload 是合法 JSON
// 空串表示未给出 payload, 因此不参与校验
//
// validatePayload checks that --payload holds valid JSON
// An empty string means no payload was given and takes no part in the check
func validatePayload(opts *options) *apperr.Problem {
	if opts.Payload == "" {
		return nil
	}
	var probe any
	if err := json.Unmarshal([]byte(opts.Payload), &probe); err != nil {
		return &apperr.Problem{
			ID:   "cli.error.invalid_payload",
			Data: map[string]any{"Detail": err.Error()},
		}
	}
	return nil
}

// validateVarPairs 校验可重复 --var 的格式与保留名
// 形状与保留名都在解析期拦下, 取值合并仍由 notify 完成
// 形状错误归到 cli.error.invalid_var, 保留名另有自己的键, 这样文案可被翻译而不是硬编码英文片段
//
// validateVarPairs validates the shape and reserved names of the repeatable --var values
// The shape and a reserved name are caught while parsing, while merging stays with notify
// A shape failure takes cli.error.invalid_var, while a reserved name gets its own key so the copy is
// translated instead of being a hard-coded English fragment
func validateVarPairs(pairs []string) *apperr.Problem {
	for _, pair := range pairs {
		key, _, ok := strings.Cut(pair, "=")
		trimmed := strings.TrimSpace(key)
		switch {
		case !ok || trimmed == "":
			return &apperr.Problem{
				ID:   "cli.error.invalid_var",
				Data: map[string]any{"Value": pair, "Reason": "KEY=VALUE"},
			}
		case isReservedVarName(trimmed):
			return &apperr.Problem{
				ID:   "cli.error.invalid_var_reserved",
				Data: map[string]any{"Value": pair, "Variable": trimmed},
			}
		}
	}
	return nil
}

// validateSetPairs 校验可重复 --set 的格式并拒绝重复键
// 格式失败用 cli.error.invalid_set, 重复键沿用配置层的 config.inline.set_duplicate
//
// validateSetPairs validates the shape of the repeatable --set values and rejects a duplicate key
// A bad shape uses cli.error.invalid_set, while a duplicate key keeps the configuration-layer key
// config.inline.set_duplicate
func validateSetPairs(sets []string) *apperr.Problem {
	seen := map[string]struct{}{}
	for _, entry := range sets {
		key, _, ok := strings.Cut(entry, "=")
		trimmed := strings.TrimSpace(key)
		if !ok || trimmed == "" {
			return &apperr.Problem{
				ID:   "cli.error.invalid_set",
				Data: map[string]any{"Value": entry, "Reason": "KEY=VALUE"},
			}
		}
		if _, dup := seen[trimmed]; dup {
			return &apperr.Problem{
				ID:   "config.inline.set_duplicate",
				Data: map[string]any{"Key": trimmed},
			}
		}
		seen[trimmed] = struct{}{}
	}
	return nil
}

// reservedVarNames 保留变量名, 与 template.IsReservedVar 的定义保持一致
// 这里单独列出是为了让解析期不依赖模板包, 键集合的权威仍在 template
//
// reservedVarNames lists the reserved variable names, matching template.IsReservedVar
// The list is repeated here so the parser does not depend on the template package, while
// template stays the authority for the set
var reservedVarNames = map[string]struct{}{
	"title": {}, "body": {},
	"time": {}, "date": {}, "datetime": {},
	"elapsed": {}, "elapsed_ms": {}, "elapsed_s": {},
	"env": {},
}

// isReservedVarName 报告变量名是否为保留名或落在 env 命名空间里
//
// isReservedVarName reports whether a variable name is reserved or lives in the env namespace
func isReservedVarName(name string) bool {
	if _, ok := reservedVarNames[name]; ok {
		return true
	}
	return strings.HasPrefix(name, "env.")
}

// stdinConsumers 返回会读取标准输入的 flag 名列表
// 一次调用最多允许一个消费者, 多于一个属于用法错误
//
// stdinConsumers returns the flags that would read standard input
// One call may have at most one consumer, and more than one is a usage error
func stdinConsumers(opts *options) []string {
	var out []string
	if opts.ConfigPath == "-" {
		out = append(out, "--config")
	}
	if opts.TitleFile == "-" {
		out = append(out, "--title-file")
	}
	if opts.BodyFile == "-" {
		out = append(out, "--body-file")
	}
	if opts.VarFile == "-" {
		out = append(out, "--var-file")
	}
	return out
}

// validateStdin 校验 stdin 单一消费者规则
//
// validateStdin validates the single-consumer rule for standard input
func validateStdin(opts *options) *apperr.Problem {
	consumers := stdinConsumers(opts)
	if len(consumers) <= 1 {
		return nil
	}
	return &apperr.Problem{
		ID:   "cli.error.stdin_conflict",
		Data: map[string]any{"Flags": strings.Join(consumers, ", ")},
	}
}

// validateTitles 校验标题与正文的互斥 flag 组合
//
// validateTitles validates the mutually exclusive flag pairs of the title and the body
func validateTitles(opts *options) *apperr.Problem {
	if opts.Title != "" && opts.TitleFile != "" {
		return &apperr.Problem{
			ID:   "cli.error.duplicate_flag",
			Data: map[string]any{"Flag": "--title/--title-file"},
		}
	}
	if opts.Body != "" && opts.BodyFile != "" {
		return &apperr.Problem{
			ID:   "cli.error.duplicate_flag",
			Data: map[string]any{"Flag": "--body/--body-file"},
		}
	}
	return nil
}

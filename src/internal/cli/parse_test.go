package cli

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// 解析用例: 命令前后 flag, = 与空格取值, -- 终止, 重复, 可重复, 布尔, 位置参数与 stdin 规则
//
// Parse cases: flags before and after the command, the = and space value forms, -- termination,
// repeats, repeatable flags, booleans, positional arguments and the stdin rules
// ---------------------------------------------------------------------------

// TestParseArgs_FlagsAroundCommand 覆盖 flag 写在命令前后都能解析
//
// TestParseArgs_FlagsAroundCommand covers flags parsed both before and after the command
func TestParseArgs_FlagsAroundCommand(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "before", args: []string{"--config", "example.json", "send", "--title", "example"}},
		{name: "after", args: []string{"send", "--config", "example.json", "--title", "example"}},
		{name: "mixed", args: []string{"--title", "example", "send", "--config", "example.json"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, problem := parseArgs(tc.args)
			if problem != nil {
				t.Fatalf("parseArgs(%v) = %v", tc.args, problem)
			}
			if parsed.opts.Command != "send" {
				t.Fatalf("command = %q, want send", parsed.opts.Command)
			}
			if parsed.opts.ConfigPath != "example.json" {
				t.Fatalf("config = %q, want example.json", parsed.opts.ConfigPath)
			}
			if parsed.opts.Title != "example" {
				t.Fatalf("title = %q, want example", parsed.opts.Title)
			}
		})
	}
}

// TestParseArgs_ValueForms 覆盖 = 与空格两种取值形式, 以及显式空值
//
// TestParseArgs_ValueForms covers both value forms and the explicit empty value
func TestParseArgs_ValueForms(t *testing.T) {
	parsed, problem := parseArgs([]string{"send", "--title=example", "--body", ""})
	if problem != nil {
		t.Fatalf("parseArgs = %v", problem)
	}
	if parsed.opts.Title != "example" {
		t.Fatalf("title = %q, want example", parsed.opts.Title)
	}
	if parsed.opts.Body != "" {
		t.Fatalf("body = %q, want an empty string", parsed.opts.Body)
	}
}

// TestParseArgs_DoubleDash 覆盖 -- 之后的位置参数按位置参数处理而不再解析成 flag
//
// TestParseArgs_DoubleDash covers positionals after -- no longer being read as flags
func TestParseArgs_DoubleDash(t *testing.T) {
	parsed, problem := parseArgs([]string{"types", "--", "--help"})
	if problem != nil {
		t.Fatalf("parseArgs = %v", problem)
	}
	if len(parsed.positionals) != 1 || parsed.positionals[0] != "--help" {
		t.Fatalf("positionals = %v, want [--help]", parsed.positionals)
	}
}

// TestParseArgs_RepeatRules 覆盖单值 flag 重复报错与可重复 flag 追加
//
// TestParseArgs_RepeatRules covers a repeated single-value flag failing and repeatable flags
// appending
func TestParseArgs_RepeatRules(t *testing.T) {
	if _, problem := parseArgs([]string{"send", "--title", "a", "--title", "b"}); problem == nil || problem.ID != "cli.error.duplicate_flag" {
		t.Fatalf("repeated --title = %v, want cli.error.duplicate_flag", problem)
	}
	if _, problem := parseArgs([]string{"send", "--config", "a", "--config", "b"}); problem == nil || problem.ID != "cli.error.duplicate_flag" {
		t.Fatalf("repeated --config = %v, want cli.error.duplicate_flag", problem)
	}
	parsed, problem := parseArgs([]string{"send", "--var", "a=1", "--var", "b=2", "--channel", "x", "--channel", "y"})
	if problem != nil {
		t.Fatalf("parseArgs = %v", problem)
	}
	if strings.Join(parsed.opts.Vars, ",") != "a=1,b=2" {
		t.Fatalf("vars = %v", parsed.opts.Vars)
	}
	if strings.Join(parsed.opts.Channels, ",") != "x,y" {
		t.Fatalf("channels = %v", parsed.opts.Channels)
	}
}

// TestParseArgs_Bool 覆盖布尔 flag 的两种写法与非法取值
//
// TestParseArgs_Bool covers both boolean forms and an invalid value
func TestParseArgs_Bool(t *testing.T) {
	cases := []struct {
		args  []string
		dry   bool
		valid bool
	}{
		{args: []string{"send", "--dry-run"}, dry: true, valid: true},
		{args: []string{"send", "--dry-run=true"}, dry: true, valid: true},
		{args: []string{"send", "--dry-run=false"}, dry: false, valid: true},
		{args: []string{"send", "--dry-run=0"}, dry: false, valid: true},
	}
	for _, tc := range cases {
		parsed, problem := parseArgs(tc.args)
		if !tc.valid {
			if problem == nil {
				t.Fatalf("parseArgs(%v) accepted an invalid boolean", tc.args)
			}
			continue
		}
		if problem != nil {
			t.Fatalf("parseArgs(%v) = %v", tc.args, problem)
		}
		if parsed.opts.DryRun != tc.dry {
			t.Fatalf("parseArgs(%v).DryRun = %v, want %v", tc.args, parsed.opts.DryRun, tc.dry)
		}
	}

	if _, problem := parseArgs([]string{"send", "--dry-run=maybe"}); problem == nil || problem.ID != "cli.error.invalid_bool" {
		t.Fatalf("--dry-run=maybe = %v, want cli.error.invalid_bool", problem)
	}
	// 空格形式不接受布尔取值: false 会变成多余位置参数
	//
	// The space form takes no boolean value, and false becomes an extra positional
	if _, problem := parseArgs([]string{"send", "--dry-run", "false"}); problem == nil || problem.ID != "cli.error.extra_arg" {
		t.Fatalf("--dry-run false = %v, want cli.error.extra_arg", problem)
	}
}

// TestParseArgs_DashValue 覆盖以 -- 开头的取值必须写成 = 形式
//
// TestParseArgs_DashValue covers a value starting with -- having to use the = form
func TestParseArgs_DashValue(t *testing.T) {
	if _, problem := parseArgs([]string{"send", "--title", "--example"}); problem == nil || problem.ID != "cli.error.value_dash" {
		t.Fatalf("space form = %v, want cli.error.value_dash", problem)
	}
	parsed, problem := parseArgs([]string{"send", "--title=--example"})
	if problem != nil {
		t.Fatalf("= form = %v", problem)
	}
	if parsed.opts.Title != "--example" {
		t.Fatalf("title = %q, want --example", parsed.opts.Title)
	}
}

// TestParseArgs_EmptyPathValue 覆盖路径类 flag 的显式空值
//
// TestParseArgs_EmptyPathValue covers an explicit empty value for a path flag
func TestParseArgs_EmptyPathValue(t *testing.T) {
	for _, args := range [][]string{
		{"send", "--config="},
		{"start", "--state-file="},
		{"send", "--var-file="},
		{"send", "--title-file="},
		{"send", "--body-file="},
	} {
		if _, problem := parseArgs(args); problem == nil || problem.ID != "cli.error.missing_value" {
			t.Fatalf("parseArgs(%v) = %v, want cli.error.missing_value", args, problem)
		}
	}
}

// TestParseArgs_Positionals 覆盖位置参数数量规则
//
// TestParseArgs_Positionals covers the positional-count rules
func TestParseArgs_Positionals(t *testing.T) {
	if _, problem := parseArgs([]string{"types", "webhook"}); problem != nil {
		t.Fatalf("types webhook = %v", problem)
	}
	if _, problem := parseArgs([]string{"types", "webhook", "extra"}); problem == nil || problem.ID != "cli.error.extra_arg" {
		t.Fatalf("types with two args = %v, want cli.error.extra_arg", problem)
	}
	if _, problem := parseArgs([]string{"send", "extra"}); problem == nil || problem.ID != "cli.error.extra_arg" {
		t.Fatalf("send extra = %v, want cli.error.extra_arg", problem)
	}
	if _, problem := parseArgs([]string{"help", "send"}); problem != nil {
		t.Fatalf("help send = %v", problem)
	}
	if _, problem := parseArgs([]string{"help", "send", "extra"}); problem == nil || problem.ID != "cli.error.extra_arg" {
		t.Fatalf("help with two args = %v, want cli.error.extra_arg", problem)
	}
}

// TestParseArgs_Unknown 覆盖未知命令, 未知 flag 与不适用 flag
//
// TestParseArgs_Unknown covers an unknown command, an unknown flag and a flag the command rejects
func TestParseArgs_Unknown(t *testing.T) {
	if _, problem := parseArgs([]string{"example"}); problem == nil || problem.ID != "cli.error.unknown_command" {
		t.Fatalf("unknown command = %v, want cli.error.unknown_command", problem)
	}
	if _, problem := parseArgs([]string{"send", "--bogus"}); problem == nil || problem.ID != "cli.error.unknown_flag" {
		t.Fatalf("unknown flag = %v, want cli.error.unknown_flag", problem)
	}
	if _, problem := parseArgs([]string{"list", "--title", "example"}); problem == nil || problem.ID != "cli.error.flag_not_for_command" {
		t.Fatalf("flag not for command = %v, want cli.error.flag_not_for_command", problem)
	}
	if _, problem := parseArgs([]string{"version", "--config", "example.json"}); problem == nil || problem.ID != "cli.error.flag_not_for_command" {
		t.Fatalf("version --config = %v, want cli.error.flag_not_for_command", problem)
	}
}

// TestParseArgs_MissingValue 覆盖缺值与缺命令
//
// TestParseArgs_MissingValue covers a missing value and a missing command
func TestParseArgs_MissingValue(t *testing.T) {
	if _, problem := parseArgs([]string{"send", "--config"}); problem == nil || problem.ID != "cli.error.missing_value" {
		t.Fatalf("missing value = %v, want cli.error.missing_value", problem)
	}
	if _, problem := parseArgs([]string{"--config", "example.json"}); problem == nil || problem.ID != "cli.error.missing_command" {
		t.Fatalf("missing command = %v, want cli.error.missing_command", problem)
	}
}

// TestParseArgs_NoCommand 覆盖没有命令时的三种情况: 空参数, 只有 --help, 只有 --version
//
// TestParseArgs_NoCommand covers the three cases without a command: no arguments, --help alone and
// --version alone
func TestParseArgs_NoCommand(t *testing.T) {
	parsed, problem := parseArgs(nil)
	if problem != nil || parsed.opts.Command != "" {
		t.Fatalf("empty args = %+v, %v", parsed, problem)
	}
	parsed, problem = parseArgs([]string{"--help"})
	if problem != nil || !parsed.Help {
		t.Fatalf("--help = %+v, %v", parsed, problem)
	}
	parsed, problem = parseArgs([]string{"--version"})
	if problem != nil || !parsed.ShowVersion {
		t.Fatalf("--version = %+v, %v", parsed, problem)
	}
}

// TestScanLang 覆盖语言扫描的任意位置, 大小写与等号形式
//
// TestScanLang covers the language scan at any position, in any case and in the = form
func TestScanLang(t *testing.T) {
	cases := []struct {
		args []string
		want string
		ok   bool
	}{
		{args: []string{"--lang", "zh"}, want: "zh", ok: true},
		{args: []string{"--lang=ZH-CN"}, want: "ZH-CN", ok: true},
		{args: []string{"send", "--lang", "EN", "--title", "example"}, want: "EN", ok: true},
		{args: []string{"--lang", "--title"}, want: "", ok: true},
		{args: []string{"send", "--title", "example"}, ok: false},
	}
	for _, tc := range cases {
		got, ok := scanLang(tc.args)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("scanLang(%v) = %q, %v; want %q, %v", tc.args, got, ok, tc.want, tc.ok)
		}
	}
}

// TestValidateStdin 覆盖 stdin 单一消费者规则
//
// TestValidateStdin covers the single-consumer rule for standard input
func TestValidateStdin(t *testing.T) {
	ok := &options{ConfigPath: "-"}
	if problem := validateStdin(ok); problem != nil {
		t.Fatalf("single consumer = %v", problem)
	}
	conflict := &options{ConfigPath: "-", VarFile: "-"}
	problem := validateStdin(conflict)
	if problem == nil || problem.ID != "cli.error.stdin_conflict" {
		t.Fatalf("two consumers = %v, want cli.error.stdin_conflict", problem)
	}
	if flags, _ := problem.Data["Flags"].(string); !strings.Contains(flags, "--config") || !strings.Contains(flags, "--var-file") {
		t.Fatalf("Flags = %v, want both flags", problem.Data["Flags"])
	}
}

// TestParseArgs_Parallel 覆盖 --parallel 的范围校验
//
// TestParseArgs_Parallel covers the range check of --parallel
func TestParseArgs_Parallel(t *testing.T) {
	for _, value := range []string{"1", "4", "8"} {
		parsed, problem := parseArgs([]string{"send", "--parallel", value})
		if problem != nil {
			t.Fatalf("--parallel %s = %v", value, problem)
		}
		if parsed.opts.Parallel == 0 {
			t.Fatalf("--parallel %s left the concurrency unset", value)
		}
	}
	for _, value := range []string{"0", "9", "-1", "example"} {
		if _, problem := parseArgs([]string{"send", "--parallel", value}); problem == nil || problem.ID != "cli.error.invalid_parallel" {
			t.Fatalf("--parallel %s = %v, want cli.error.invalid_parallel", value, problem)
		}
	}
	if parsed, problem := parseArgs([]string{"send"}); problem != nil || parsed.opts.Parallel != 1 {
		t.Fatalf("default parallel = %+v, %v; want 1", parsed, problem)
	}
}

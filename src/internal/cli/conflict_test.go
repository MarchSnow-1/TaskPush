package cli

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// 模式与冲突用例: 内联判定, 内联与配置项互斥, payload/set/webhook 组合, 标题正文互斥, stdin
// 断言看渲染后的英文文案片段, 消息键本身由 catalog 一致性测试守护
//
// Mode and conflict cases: the inline decision, inline against configuration flags, the
// payload/set/webhook combinations, the mutually exclusive title and body flags, and stdin
// The assertions look at the rendered English fragments, while the catalog consistency test guards
// the message keys themselves
// ---------------------------------------------------------------------------

// TestValidateConflicts_Table 逐条覆盖冲突表
//
// TestValidateConflicts_Table covers the conflict table row by row
func TestValidateConflicts_Table(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "inline with config",
			args: []string{"send", "--webhook", "https://example.com/hook", "--config", "example.json"},
			want: "inline mode cannot be combined with --config",
		},
		{
			name: "inline with channel",
			args: []string{"send", "--type", "example", "--channel", "ops"},
			want: "inline mode cannot be combined with --channel",
		},
		{
			name: "inline with all",
			args: []string{"check", "--type", "example", "--all"},
			want: "inline mode cannot be combined with --all",
		},
		{
			name: "set without type",
			args: []string{"send", "--set", "token=example"},
			want: "--set needs --type or --webhook",
		},
		{
			name: "payload without webhook",
			args: []string{"send", "--payload", "{}"},
			want: "--payload needs --webhook or --type webhook",
		},
		{
			name: "webhook with set url",
			args: []string{"send", "--webhook", "https://example.com/hook", "--set", "url=https://example.org"},
			want: "--webhook already sets url",
		},
		{
			name: "payload with set body",
			args: []string{"send", "--webhook", "https://example.com/hook", "--payload", "{}", "--set", "body.format=text"},
			want: "conflicts with another flag",
		},
		{
			name: "type with webhook",
			args: []string{"send", "--type", "example", "--webhook", "https://example.com/hook"},
			want: "--webhook already sets url",
		},
		{
			name: "title and title file",
			args: []string{"send", "--title", "example", "--title-file", "example.txt"},
			want: "--title/--title-file",
		},
		{
			name: "body and body file",
			args: []string{"send", "--body", "example", "--body-file", "example.txt"},
			want: "--body/--body-file",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := runCLI(t, tc.args...)
			if result.code != exitUsage {
				t.Fatalf("exit code = %d, want 2 (stderr %q)", result.code, result.stderr)
			}
			if !strings.Contains(result.stderr, tc.want) {
				t.Fatalf("stderr %q does not mention %q", result.stderr, tc.want)
			}
		})
	}
}

// TestValidateConflicts_InlinePayloadOnWebhookType 覆盖 --type webhook 与 --payload 的合法组合
//
// TestValidateConflicts_InlinePayloadOnWebhookType covers --type webhook combined with --payload
// being accepted
func TestValidateConflicts_InlinePayloadOnWebhookType(t *testing.T) {
	parsed, problem := parseArgs([]string{
		"send", "--type", "webhook", "--payload", `{"text":"example"}`,
	})
	if problem != nil {
		t.Fatalf("parseArgs = %v", problem)
	}
	if problem := validateConflicts(&parsed.opts); problem != nil {
		t.Fatalf("validateConflicts = %v", problem)
	}
}

// TestValidateConflicts_InlineIgnoresEnvConfig 覆盖内联模式不与 TASKPUSH_CONFIG_JSON 冲突
//
// TestValidateConflicts_InlineIgnoresEnvConfig covers inline mode not conflicting with
// TASKPUSH_CONFIG_JSON
func TestValidateConflicts_InlineIgnoresEnvConfig(t *testing.T) {
	withEnv(t, map[string]string{"TASKPUSH_CONFIG_JSON": `{"channels":[]}`})
	parsed, problem := parseArgs([]string{"send", "--webhook", "https://example.com/hook"})
	if problem != nil {
		t.Fatalf("parseArgs = %v", problem)
	}
	if problem := validateConflicts(&parsed.opts); problem != nil {
		t.Fatalf("validateConflicts = %v", problem)
	}
}

// TestValidateConflicts_StartConfigStdin 覆盖 start --config - 被拒绝
//
// TestValidateConflicts_StartConfigStdin covers start --config - being rejected
func TestValidateConflicts_StartConfigStdin(t *testing.T) {
	result := runCLI(t, "start", "--config", "-")
	if result.code != exitUsage {
		t.Fatalf("exit code = %d, want 2 (stderr %q)", result.code, result.stderr)
	}
}

// TestValidateTitles 覆盖标题正文的互斥标志, 以及只给一方的合法情形
//
// TestValidateTitles covers the mutually exclusive title and body flags and the accepted case of
// giving one side only
func TestValidateTitles(t *testing.T) {
	if problem := validateTitles(&options{Title: "example"}); problem != nil {
		t.Fatalf("title only = %v", problem)
	}
	if problem := validateTitles(&options{TitleFile: "example.txt"}); problem != nil {
		t.Fatalf("title file only = %v", problem)
	}
	if problem := validateTitles(&options{Title: "example", TitleFile: "example.txt"}); problem == nil {
		t.Fatal("title plus title file was accepted")
	}
	if problem := validateTitles(&options{Body: "example", BodyFile: "example.txt"}); problem == nil {
		t.Fatal("body plus body file was accepted")
	}
}

// TestValidateVarAndSetPairs 覆盖 --var 与 --set 的形状与保留名
// 形状失败沿用契约的 invalid_var/invalid_set, 保留名单独用 invalid_var_reserved 以便本地化
//
// TestValidateVarAndSetPairs covers the shape and reserved names of --var and --set
// A bad shape keeps the contract keys invalid_var and invalid_set, while a reserved name gets its
// own invalid_var_reserved key so the copy can be translated
func TestValidateVarAndSetPairs(t *testing.T) {
	if problem := validateVarPairs([]string{"stage=build"}); problem != nil {
		t.Fatalf("valid var = %v", problem)
	}
	if problem := validateVarPairs([]string{"stage"}); problem == nil || problem.ID != "cli.error.invalid_var" {
		t.Fatalf("var without = = %v, want cli.error.invalid_var", problem)
	}
	reserved := validateVarPairs([]string{"title=x"})
	if reserved == nil || reserved.ID != "cli.error.invalid_var_reserved" {
		t.Fatalf("reserved var = %v, want cli.error.invalid_var_reserved", reserved)
	}
	if got := reserved.Data["Variable"]; got != "title" {
		t.Fatalf("reserved var Variable = %v, want title", got)
	}
	if problem := validateVarPairs([]string{"env.HOME=x"}); problem == nil || problem.ID != "cli.error.invalid_var_reserved" {
		t.Fatalf("env namespace var = %v, want cli.error.invalid_var_reserved", problem)
	}

	if problem := validateSetPairs([]string{"token=example"}); problem != nil {
		t.Fatalf("valid set = %v", problem)
	}
	if problem := validateSetPairs([]string{"token"}); problem == nil || problem.ID != "cli.error.invalid_set" {
		t.Fatalf("set without = = %v, want cli.error.invalid_set", problem)
	}
	if problem := validateSetPairs([]string{"token=a", "token=b"}); problem == nil || problem.ID != "config.inline.set_duplicate" {
		t.Fatalf("duplicate set = %v, want config.inline.set_duplicate", problem)
	}
}

// TestValidatePayload 覆盖 --payload 的 JSON 校验
//
// TestValidatePayload covers the JSON check of --payload
func TestValidatePayload(t *testing.T) {
	if problem := validatePayload(&options{Payload: `{"text":"example"}`}); problem != nil {
		t.Fatalf("valid payload = %v", problem)
	}
	if problem := validatePayload(&options{Payload: "{"}); problem == nil || problem.ID != "cli.error.invalid_payload" {
		t.Fatalf("invalid payload = %v, want cli.error.invalid_payload", problem)
	}
}

// TestIsInline 覆盖内联模式的判定
//
// TestIsInline covers the inline-mode decision
func TestIsInline(t *testing.T) {
	if isInline(&options{}) {
		t.Fatal("empty options counted as inline")
	}
	for _, opts := range []options{
		{Webhook: "https://example.com/hook"},
		{Type: "example"},
		{Sets: []string{"token=x"}},
		{Payload: "{}"},
	} {
		if !isInline(&opts) {
			t.Fatalf("options %+v did not count as inline", opts)
		}
	}
}

// TestParse_EmptyInlineFlagIsUsageError 覆盖内联 flag 的显式空值按用法错误处理
// 空值若被当成未给出, 命令会改用配置来源里的渠道发送, 消息就发到了别处
//
// TestParse_EmptyInlineFlagIsUsageError covers an explicit empty inline flag counting as a usage
// error
// Treating an empty value as absent would let the command send through the channels of the
// configuration source, which delivers the message somewhere else
func TestParse_EmptyInlineFlagIsUsageError(t *testing.T) {
	for _, name := range []string{"webhook", "type", "payload"} {
		args := []string{"send", "--" + name + "=", "--title", "example"}
		_, problem := parseArgs(args)
		if problem == nil || problem.ID != "cli.error.missing_value" {
			t.Fatalf("%s = %v, want cli.error.missing_value", args, problem)
		}
		if got := problem.Data["Flag"]; got != "--"+name {
			t.Fatalf("%s Flag = %v, want --%s", args, got, name)
		}
	}
}

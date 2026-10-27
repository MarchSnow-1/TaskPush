package notify

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/config"
	"github.com/MarchSnow-1/TaskPush/internal/template"
)

// ---------------------------------------------------------------------------
// 变量与渲染测试: 来源优先级, 保留名, 内置/计时变量, 严格模式标题正文
//
// Variable and rendering tests: source priority, reserved names, built-in and elapsed variables,
// and strict-mode title and body
// ---------------------------------------------------------------------------

// TestMain 只注册一次假渠道工厂
// channel.Register 对重复 type 直接 panic, 因此注册入口在这里收口
//
// TestMain registers the fake channel factory once
// channel.Register panics on a duplicate type, so registration is funnelled through here
func TestMain(m *testing.M) {
	registerFake()
	os.Exit(m.Run())
}

// defaultTestDefaults 返回一份可用的全局默认值
//
// defaultTestDefaults returns a usable set of global defaults
func defaultTestDefaults() config.Defaults {
	return config.Defaults{Timeout: time.Second, Retries: 1, RetryDelay: 0}
}

// testChannel 构造一个假渠道条目
//
// testChannel builds a fake channel entry
func testChannel(id string, settings fakeSettings) config.Channel {
	raw, err := json.Marshal(settings)
	if err != nil {
		panic(err)
	}
	return config.Channel{ID: id, Type: fakeType, Settings: raw}
}

// literalText 把 title/body 变量还原成文本: 调度层用 template.Literal 写入这两个变量,
// 测试断言文本内容时先经过这里
//
// literalText turns the title and body variables back into text: the scheduling layer writes them as
// a template.Literal, and a text assertion goes through here first
func literalText(val any) string {
	switch v := val.(type) {
	case template.Literal:
		return string(v)
	case string:
		return v
	default:
		return ""
	}
}

// TestSend_VariablePriority 覆盖内置 → 文件 → CLI 的优先级与 title/body 写入
//
// TestSend_VariablePriority covers the built-in, file then CLI priority and the title and body
// variables being written
func TestSend_VariablePriority(t *testing.T) {
	resetFake()
	dir := t.TempDir()
	varsPath := filepath.Join(dir, "vars.json")
	if err := os.WriteFile(varsPath, []byte(`{"fromFile":"file","shared":"file"}`), 0o600); err != nil {
		t.Fatalf("write vars file: %v", err)
	}

	results, err := Send(context.Background(), defaultTestDefaults(), []config.Channel{testChannel("example", fakeSettings{})}, Options{
		Title:    "title {{shared}}",
		Body:     "body",
		VarFile:  varsPath,
		VarPairs: []string{"shared=cli"},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("results = %+v", results)
	}
	record := recordOf("example")
	if record.Title != "title cli" {
		t.Errorf("title = %q, want the CLI value to outrank the file", record.Title)
	}
	if got := record.Vars["fromFile"]; got != "file" {
		t.Errorf("fromFile = %v, want the file value", got)
	}
	if got := literalText(record.Vars["title"]); got != "title cli" {
		t.Errorf("title variable = %v, want the rendered title", record.Vars["title"])
	}
	if got := literalText(record.Vars["body"]); got != "body" {
		t.Errorf("body variable = %v, want the rendered body", record.Vars["body"])
	}
	for _, name := range []string{"time", "date", "datetime"} {
		if _, ok := record.Vars[name]; !ok {
			t.Errorf("built-in variable %q is missing", name)
		}
	}
}

// TestSend_TimedVars 覆盖 Timed 时由 StartTime 生成 elapsed 系列
//
// TestSend_TimedVars covers the elapsed variables derived from StartTime when Timed is set
func TestSend_TimedVars(t *testing.T) {
	resetFake()
	start := time.Now().Add(-2500 * time.Millisecond)
	if _, err := Send(context.Background(), defaultTestDefaults(), []config.Channel{testChannel("example", fakeSettings{})}, Options{
		Timed:     true,
		StartTime: start,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	record := recordOf("example")
	if _, ok := record.Vars["elapsed"]; !ok {
		t.Error("elapsed is missing under Timed")
	}
	if _, ok := record.Vars["elapsed_ms"]; !ok {
		t.Error("elapsed_ms is missing under Timed")
	}
	if _, ok := record.Vars["elapsed_s"]; !ok {
		t.Error("elapsed_s is missing under Timed")
	}
}

// TestSend_FutureStartTimeClampsElapsedToZero 覆盖起点在未来时 elapsed 系列按 0 计
// 用 now 作为起点会与前一次取时拉开几十毫秒, 因此这里断言严格为 0 而不是"接近 0"
//
// TestSend_FutureStartTimeClampsElapsedToZero covers a start time in the future, which makes the
// elapsed variables come out as 0
// Using now as the origin would leave a gap of a few dozen milliseconds from the earlier clock read,
// so this asserts an exact 0 rather than "close to 0"
func TestSend_FutureStartTimeClampsElapsedToZero(t *testing.T) {
	resetFake()
	if _, err := Send(context.Background(), defaultTestDefaults(), []config.Channel{testChannel("example", fakeSettings{})}, Options{
		Timed:     true,
		StartTime: time.Now().Add(2 * time.Hour),
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	record := recordOf("example")
	got, ok := record.Vars["elapsed_ms"]
	if !ok {
		t.Fatal("elapsed_ms is missing under Timed")
	}
	if got != int64(0) {
		t.Errorf("elapsed_ms = %v, want 0 for a start time in the future", got)
	}
	if elapsed, ok := record.Vars["elapsed"]; !ok || elapsed != "0ms" {
		t.Errorf("elapsed = %v, want 0ms for a start time in the future", elapsed)
	}
}

// TestSend_NoElapsedVarsWhenNotTimed 覆盖非 Timed 时 elapsed 系列不存在
// 严格模式下引用未定义变量因此会报错, 这里只检查变量集合
//
// TestSend_NoElapsedVarsWhenNotTimed covers the elapsed variables being absent without Timed
// A reference to an undefined variable fails under strict mode, so this only inspects the variable set
func TestSend_NoElapsedVarsWhenNotTimed(t *testing.T) {
	resetFake()
	vars, err := buildVars("", nil, time.Time{}, false)
	if err != nil {
		t.Fatalf("buildVars: %v", err)
	}
	for _, name := range []string{"elapsed", "elapsed_ms", "elapsed_s"} {
		if _, ok := vars.Resolve(name); ok {
			t.Errorf("%q is defined without Timed", name)
		}
	}
}

// TestSend_ReservedVar 覆盖保留名在 --var 与 --var-file 中被拒, Path 指向来源
//
// TestSend_ReservedVar covers a reserved name being rejected through --var and --var-file, with
// Path pointing at the source
func TestSend_ReservedVar(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "vars.json")
	if err := os.WriteFile(filePath, []byte(`{"env.FOO":"x"}`), 0o600); err != nil {
		t.Fatalf("write vars file: %v", err)
	}

	cases := []struct {
		name     string
		opts     Options
		wantPath string
		variable string
	}{
		{
			name:     "cli reserved",
			opts:     Options{VarPairs: []string{"title=x"}},
			wantPath: "--var",
			variable: "title",
		},
		{
			name:     "cli env namespace",
			opts:     Options{VarPairs: []string{"env.FOO=x"}},
			wantPath: "--var",
			variable: "env.FOO",
		},
		{
			name:     "file reserved",
			opts:     Options{VarFile: filePath},
			wantPath: "--var-file",
			variable: "env.FOO",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Send(context.Background(), defaultTestDefaults(), []config.Channel{testChannel("example", fakeSettings{})}, tc.opts)
			var prob *apperr.Problem
			if !errors.As(err, &prob) {
				t.Fatalf("Send error = %v, want *apperr.Problem", err)
			}
			if prob.ID != "template.error.reserved_var" {
				t.Errorf("ID = %q, want template.error.reserved_var", prob.ID)
			}
			if got := prob.Data["Path"]; got != tc.wantPath {
				t.Errorf("Path = %v, want %q", got, tc.wantPath)
			}
			if got := prob.Data["Variable"]; got != tc.variable {
				t.Errorf("Variable = %v, want %q", got, tc.variable)
			}
		})
	}
}

// TestSend_StrictRender 覆盖标题与正文的严格渲染错误, 以及内层 Path 被覆写
//
// TestSend_StrictRender covers strict render failures of the title and body and the inner Path being
// overwritten
func TestSend_StrictRender(t *testing.T) {
	cases := []struct {
		name     string
		opts     Options
		wantKey  string
		wantPath string
	}{
		{
			name:     "title",
			opts:     Options{Title: "{{missing}}"},
			wantKey:  keyErrorTitle,
			wantPath: "--title",
		},
		{
			name:     "body",
			opts:     Options{Body: "{{missing}}"},
			wantKey:  keyErrorBody,
			wantPath: "--body",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Send(context.Background(), defaultTestDefaults(), []config.Channel{testChannel("example", fakeSettings{})}, tc.opts)
			var prob *apperr.Problem
			if !errors.As(err, &prob) {
				t.Fatalf("Send error = %v, want *apperr.Problem", err)
			}
			if prob.ID != tc.wantKey {
				t.Errorf("ID = %q, want %q", prob.ID, tc.wantKey)
			}
			var inner *apperr.Problem
			if !errors.As(prob.Cause, &inner) {
				t.Fatalf("Cause = %v, want the inner template Problem", prob.Cause)
			}
			if got := inner.Data["Path"]; got != tc.wantPath {
				t.Errorf("inner Path = %v, want %q", got, tc.wantPath)
			}
			if prob.Data["Detail"] == "" {
				t.Error("Detail is empty")
			}
		})
	}
}

// TestSend_InvalidVar 覆盖 CLI --var 的格式错误与空键
//
// TestSend_InvalidVar covers a malformed CLI --var and an empty key
func TestSend_InvalidVar(t *testing.T) {
	cases := []struct {
		name   string
		pair   string
		reason string
	}{
		{name: "no separator", pair: "example", reason: "expect key=value"},
		{name: "empty key", pair: "=value", reason: "empty key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Send(context.Background(), defaultTestDefaults(), []config.Channel{testChannel("example", fakeSettings{})}, Options{VarPairs: []string{tc.pair}})
			var prob *apperr.Problem
			if !errors.As(err, &prob) {
				t.Fatalf("Send error = %v, want *apperr.Problem", err)
			}
			if prob.ID != keyErrorVar {
				t.Fatalf("ID = %q, want %q", prob.ID, keyErrorVar)
			}
			if got := prob.Data["Value"]; got != tc.pair {
				t.Errorf("Value = %v, want %q", got, tc.pair)
			}
			if got := prob.Data["Reason"]; got != tc.reason {
				t.Errorf("Reason = %v, want %q", got, tc.reason)
			}
		})
	}
}

// TestSend_VarFileProblem 覆盖变量文件读取失败的结构化错误
//
// TestSend_VarFileProblem covers the structured error for an unreadable vars file
func TestSend_VarFileProblem(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.json")
	_, err := Send(context.Background(), defaultTestDefaults(), []config.Channel{testChannel("example", fakeSettings{})}, Options{VarFile: missing})
	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		t.Fatalf("Send error = %v, want *apperr.Problem", err)
	}
	if prob.ID != keyErrorVarFile {
		t.Errorf("ID = %q, want %q", prob.ID, keyErrorVarFile)
	}
	if got := prob.Data["Path"]; got != missing {
		t.Errorf("Path = %v, want %q", got, missing)
	}
}

// TestSend_NoChannels 覆盖空渠道列表的全局错误
//
// TestSend_NoChannels covers the global error for an empty channel list
func TestSend_NoChannels(t *testing.T) {
	_, err := Send(context.Background(), defaultTestDefaults(), nil, Options{})
	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		t.Fatalf("Send error = %v, want *apperr.Problem", err)
	}
	if prob.ID != keyErrorNoChannels {
		t.Errorf("ID = %q, want %q", prob.ID, keyErrorNoChannels)
	}
}

// TestSend_InvalidSettingsLandsInResult 覆盖 settings 解码错误进 Result 而不是全局错误
//
// TestSend_InvalidSettingsLandsInResult covers a settings decode error landing in Result rather than
// a global error
func TestSend_InvalidSettingsLandsInResult(t *testing.T) {
	resetFake()
	spec := config.Channel{ID: "example", Type: fakeType, Settings: json.RawMessage(`{"unknown":1}`)}
	results, err := Send(context.Background(), defaultTestDefaults(), []config.Channel{spec}, Options{})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if results[0].Error == nil {
		t.Fatal("an unknown settings field did not produce a result error")
	}
	if results[0].ID != "example" || results[0].Type != fakeType {
		t.Errorf("result identity = %s/%s", results[0].ID, results[0].Type)
	}
}

// TestSend_UnknownChannelType 覆盖未注册 type 进 Result 而不是全局错误
//
// TestSend_UnknownChannelType covers an unregistered type landing in Result rather than a global error
func TestSend_UnknownChannelType(t *testing.T) {
	resetFake()
	spec := config.Channel{ID: "example", Type: "absent", Settings: json.RawMessage(`{}`)}
	results, err := Send(context.Background(), defaultTestDefaults(), []config.Channel{spec}, Options{})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	var prob *apperr.Problem
	if !errors.As(results[0].Error, &prob) {
		t.Fatalf("result error = %v, want *apperr.Problem", results[0].Error)
	}
	if prob.ID != "channel.error.unknown_type" {
		t.Errorf("ID = %q, want channel.error.unknown_type", prob.ID)
	}
}

// TestSend_ResultFields 覆盖 Attempts/Warnings/Preview/Error 的取值
//
// TestSend_ResultFields covers the Attempts, Warnings, Preview and Error values
func TestSend_ResultFields(t *testing.T) {
	resetFake()
	defaults := defaultTestDefaults()
	defaults.Retries = 0
	results, err := Send(context.Background(), defaults, []config.Channel{testChannel("example", fakeSettings{PrepareWarn: true, SendWarn: true})}, Options{})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	result := results[0]
	if result.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", result.Attempts)
	}
	if result.Error != nil {
		t.Errorf("Error = %v, want nil", result.Error)
	}
	if result.Preview != nil {
		t.Errorf("Preview = %v, want nil on a real send", result.Preview)
	}
	if len(result.Warnings) != 2 {
		t.Fatalf("Warnings = %v, want the Prepare and Send warnings", result.Warnings)
	}
	if result.Warnings[0].ID != "channel.example.warn.prepare" || result.Warnings[1].ID != "channel.example.warn.send" {
		t.Errorf("Warnings = %+v, want the Prepare warning then the Send warning", result.Warnings)
	}
}

// TestSend_PrepareFailure 覆盖 Prepare 失败时不调用 Send, 错误进 Result
//
// TestSend_PrepareFailure covers Prepare failing without calling Send and the error landing in Result
func TestSend_PrepareFailure(t *testing.T) {
	resetFake()
	results, err := Send(context.Background(), defaultTestDefaults(), []config.Channel{testChannel("example", fakeSettings{PrepareFail: true})}, Options{})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if results[0].Error == nil {
		t.Fatal("Prepare failure did not land in Result.Error")
	}
	if got := recordOf("example").SendCnt; got != 0 {
		t.Errorf("Send call count = %d, want 0", got)
	}
}

// TestSend_DryRunDoesNotSend 覆盖 dry-run 只取预览, 不调用 Send, Attempts 为 0
//
// TestSend_DryRunDoesNotSend covers a dry run taking the preview only, calling no Send and leaving
// Attempts at 0
func TestSend_DryRunDoesNotSend(t *testing.T) {
	resetFake()
	results, err := Send(context.Background(), defaultTestDefaults(), []config.Channel{testChannel("example", fakeSettings{})}, Options{DryRun: true})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := recordOf("example").SendCnt; got != 0 {
		t.Errorf("Send call count = %d, want 0 under dry-run", got)
	}
	if results[0].Attempts != 0 {
		t.Errorf("Attempts = %d, want 0 under dry-run", results[0].Attempts)
	}
	if results[0].Preview == nil {
		t.Fatal("Preview is nil under dry-run")
	}
	if results[0].Preview.ID == "" {
		t.Error("Preview carries an empty message key")
	}
	if results[0].Error != nil {
		t.Errorf("Error = %v, want nil", results[0].Error)
	}
}

// TestSend_SendWarningsOnFailure 覆盖发送失败时 Send 阶段警告仍然上送
//
// TestSend_SendWarningsOnFailure covers Send-phase warnings still coming up when the send fails
func TestSend_SendWarningsOnFailure(t *testing.T) {
	resetFake()
	defaults := defaultTestDefaults()
	defaults.Retries = 0
	results, err := Send(context.Background(), defaults, []config.Channel{testChannel("example", fakeSettings{Permanent: true, SendWarn: true})}, Options{})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if results[0].Error == nil {
		t.Fatal("the permanent failure is missing from Result.Error")
	}
	if len(results[0].Warnings) != 1 || results[0].Warnings[0].ID != "channel.example.warn.send" {
		t.Errorf("Warnings = %+v, want the Send warning on the failure path", results[0].Warnings)
	}
}

// TestSend_AllChannelsFailIndependently 覆盖一个渠道失败不影响其它渠道, 结果顺序与输入一致
//
// TestSend_AllChannelsFailIndependently covers one channel failing without affecting the others,
// with the result order matching the input
func TestSend_AllChannelsFailIndependently(t *testing.T) {
	resetFake()
	defaults := defaultTestDefaults()
	defaults.Retries = 0
	specs := []config.Channel{
		testChannel("example.a", fakeSettings{Permanent: true}),
		testChannel("example.b", fakeSettings{}),
		testChannel("example.c", fakeSettings{PrepareFail: true}),
	}
	results, err := Send(context.Background(), defaults, specs, Options{Parallel: 3})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	for i, want := range []string{"example.a", "example.b", "example.c"} {
		if results[i].ID != want {
			t.Errorf("results[%d].ID = %q, want %q", i, results[i].ID, want)
		}
	}
	if results[0].Error == nil {
		t.Error("the permanent failure did not land in Result.Error")
	}
	if results[1].Error != nil {
		t.Errorf("the healthy channel failed: %v", results[1].Error)
	}
	if results[2].Error == nil {
		t.Error("the Prepare failure did not land in Result.Error")
	}
}

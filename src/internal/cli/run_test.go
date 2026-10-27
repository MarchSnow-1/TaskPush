package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MarchSnow-1/TaskPush/internal/state"
)

// ---------------------------------------------------------------------------
// 命令用例: 每个命令的成功, 失败与退出码, 以及端到端流程
// 网络用例只访问 httptest 的本地地址
//
// Command cases: the success, the failure and the exit code of every command, plus the end-to-end
// flows
// Network cases reach local httptest addresses only
// ---------------------------------------------------------------------------

// TestRun_VersionAndHelp 覆盖 version 与 help 命令
//
// TestRun_VersionAndHelp covers the version and help commands
func TestRun_VersionAndHelp(t *testing.T) {
	result := runCLI(t, "version")
	if result.code != exitOK || result.stdout != "taskpush test-version\n" {
		t.Fatalf("version = %d %q", result.code, result.stdout)
	}

	empty := runWithVersion(t, "", "version")
	if empty.stdout != "taskpush dev\n" {
		t.Fatalf("empty version = %q, want dev", empty.stdout)
	}

	help := runCLI(t, "help", "send")
	if help.code != exitOK || !strings.Contains(help.stdout, "Send one notification") {
		t.Fatalf("help send = %d %q", help.code, help.stdout)
	}
	if unknown := runCLI(t, "help", "example"); unknown.code != exitUsage {
		t.Fatalf("help example = %d, want 2", unknown.code)
	}
}

// runWithVersion 用指定的版本号执行一次 Run
//
// runWithVersion performs one Run call with the given version
func runWithVersion(t *testing.T, version string, args ...string) runResult {
	t.Helper()
	var stdout, stderr strings.Builder
	code := Run(t.Context(), args, version, &stdout, &stderr)
	return runResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// TestRun_List 覆盖 list 的成功与配置错误
//
// TestRun_List covers list succeeding and a configuration failure
func TestRun_List(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	path := writeFile(t, dir, "taskpush.json", exampleConfig(
		`{"id":"ops","type":"`+fakeType+`","settings":{}}`,
		`{"id":"off","type":"`+fakeType+`","enabled":false,"settings":{}}`,
	))
	result := runCLI(t, "list", "--config", path)
	if result.code != exitOK {
		t.Fatalf("list = %d, stderr %q", result.code, result.stderr)
	}
	if !strings.Contains(result.stdout, "ops") || !strings.Contains(result.stdout, "off") {
		t.Fatalf("list output %q lost a channel", result.stdout)
	}
	if !strings.Contains(result.stdout, "false") {
		t.Fatalf("list output %q lost the disabled marker", result.stdout)
	}

	missing := runCLI(t, "list", "--config", filepath.Join(dir, "absent.json"))
	if missing.code != exitFailure {
		t.Fatalf("missing config = %d, want 1", missing.code)
	}
}

// TestRun_Types 覆盖 types 的列出与未知类型
//
// TestRun_Types covers listing the types and an unknown type
func TestRun_Types(t *testing.T) {
	result := runCLI(t, "types")
	if result.code != exitOK {
		t.Fatalf("types = %d, stderr %q", result.code, result.stderr)
	}
	if !strings.Contains(result.stdout, fakeType) {
		t.Fatalf("types output %q lost the registered type", result.stdout)
	}

	fields := runCLI(t, "types", fakeType)
	if fields.code != exitOK || !strings.Contains(fields.stdout, "REQUIRED") {
		t.Fatalf("types %s = %d %q", fakeType, fields.code, fields.stdout)
	}

	unknown := runCLI(t, "types", "example-absent")
	if unknown.code != exitFailure {
		t.Fatalf("unknown type = %d, want 1", unknown.code)
	}
}

// TestRun_SendInline 覆盖内联模式的成功发送与结果行
// 内联渠道不带 url, 因此假渠道只返回成功而不发任何网络请求
//
// TestRun_SendInline covers a successful inline send and its result line
// The inline channel carries no url, so the fake channel returns success without any network call
func TestRun_SendInline(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	result := runCLI(t, "send", "--type", fakeType, "--title", "example")
	if result.code != exitOK {
		t.Fatalf("inline send = %d, stderr %q", result.code, result.stderr)
	}
	if !strings.Contains(result.stdout, "[OK] inline ("+fakeType+")") {
		t.Fatalf("stdout %q lost the inline result line", result.stdout)
	}
	if !strings.Contains(result.stderr, "inline") {
		t.Fatalf("stderr %q lost the source note", result.stderr)
	}
}

// TestRun_SendDryRun 覆盖 dry-run 只输出预览且不发送
//
// TestRun_SendDryRun covers a dry run printing the preview and sending nothing
func TestRun_SendDryRun(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := exampleConfig(`{"id":"ops","type":"` + fakeHTTPType + `","settings":{"url":"` + server.URL + `"}}`)
	path := writeFile(t, dir, "taskpush.json", cfg)
	result := runCLI(t, "send", "--config", path, "--dry-run", "--title", "example")
	if result.code != exitOK {
		t.Fatalf("dry run = %d, stderr %q", result.code, result.stderr)
	}
	if !strings.Contains(result.stdout, "POST https://example.com/redacted") {
		t.Fatalf("dry run %q lost the redacted preview", result.stdout)
	}
	if strings.Contains(result.stdout, server.URL) {
		t.Fatalf("dry run %q leaked the target address", result.stdout)
	}
	if hits.Load() != 0 {
		t.Fatalf("dry run sent %d requests, want 0", hits.Load())
	}
}

// TestRun_DryRunKeepsFailureExit 覆盖 dry-run 在渠道失败时仍退出 1
// dry-run 是完整渲染与校验, 渠道坏了就有失败数, 不能让流水线误判为通过
//
// TestRun_DryRunKeepsFailureExit covers a dry run exiting 1 on a channel failure
// A dry run performs the full render and validation, so a broken channel counts as a failure and
// the pipeline must not read the run as a pass
func TestRun_DryRunKeepsFailureExit(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	cfg := exampleConfig(
		`{"id":"good","type":"`+fakeType+`","settings":{}}`,
		`{"id":"bad","type":"`+fakeType+`","settings":{"prepareFail":true}}`,
	)
	path := writeFile(t, dir, "taskpush.json", cfg)

	result := runCLI(t, "send", "--config", path, "--dry-run", "--title", "example")
	if result.code != exitFailure {
		t.Fatalf("dry run with a broken channel = %d, want %d (stdout %q)", result.code, exitFailure, result.stdout)
	}
	if !strings.Contains(result.stdout, "[ERROR] bad (") {
		t.Fatalf("dry run %q lost the failing channel line", result.stdout)
	}
}

// TestRun_FinishDryRunKeepsFailureExit 覆盖 finish --dry-run 在渠道失败时仍退出 1
// 状态文件在有渠道失败时保留, 与真实发送的部分失败语义一致
//
// TestRun_FinishDryRunKeepsFailureExit covers finish --dry-run exiting 1 on a channel failure
// The state file stays when a channel fails, matching the partial-failure semantics of a real send
func TestRun_FinishDryRunKeepsFailureExit(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	cfg := exampleConfig(`{"id":"bad","type":"` + fakeType + `","settings":{"prepareFail":true}}`)
	path := writeFile(t, dir, "taskpush.json", cfg)
	statePath := filepath.Join(dir, ".taskpush-state.json")
	if err := state.Write(statePath, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("write state: %v", err)
	}

	result := runCLI(t, "finish", "--config", path, "--state-file", statePath, "--dry-run", "--title", "example")
	if result.code != exitFailure {
		t.Fatalf("finish dry run with a broken channel = %d, want %d (stdout %q)", result.code, exitFailure, result.stdout)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state file must survive a failed finish: %v", err)
	}
}

// TestRun_SendConfigFile 覆盖配置文件模式的真实发送
//
// TestRun_SendConfigFile covers a real send in configuration-file mode
func TestRun_SendConfigFile(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := exampleConfig(`{"id":"ops","type":"` + fakeHTTPType + `","settings":{"url":"` + server.URL + `"}}`)
	path := writeFile(t, dir, "taskpush.json", cfg)
	result := runCLI(t, "send", "--config", path, "--title", "example", "--body", "example body")
	if result.code != exitOK {
		t.Fatalf("send = %d, stderr %q", result.code, result.stderr)
	}
	if hits.Load() != 1 {
		t.Fatalf("send made %d requests, want 1", hits.Load())
	}
}

// TestRun_SendEnvConfig 覆盖 TASKPUSH_CONFIG_JSON 来源
//
// TestRun_SendEnvConfig covers the TASKPUSH_CONFIG_JSON source
func TestRun_SendEnvConfig(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	withEnv(t, map[string]string{
		"TASKPUSH_CONFIG_JSON": exampleConfig(`{"id":"ops","type":"` + fakeType + `","settings":{}}`),
	})
	result := runCLI(t, "send", "--title", "example")
	if result.code != exitOK {
		t.Fatalf("env send = %d, stderr %q", result.code, result.stderr)
	}
	if !strings.Contains(result.stderr, "env:TASKPUSH_CONFIG_JSON") {
		t.Fatalf("stderr %q lost the env source label", result.stderr)
	}
}

// TestRun_SendConfigStdin 覆盖 --config - 从 stdin 读取配置
//
// TestRun_SendConfigStdin covers --config - reading the configuration from stdin
func TestRun_SendConfigStdin(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	withStdin(t, exampleConfig(`{"id":"ops","type":"`+fakeType+`","settings":{}}`))
	result := runCLI(t, "send", "--config", "-", "--title", "example")
	if result.code != exitOK {
		t.Fatalf("stdin config = %d, stderr %q", result.code, result.stderr)
	}
	if !strings.Contains(result.stderr, "stdin") {
		t.Fatalf("stderr %q lost the stdin label", result.stderr)
	}
}

// TestRun_SendMultiChannelOrder 覆盖多渠道结果按配置顺序输出, 并发不改变顺序
//
// TestRun_SendMultiChannelOrder covers multi-channel results in configuration order, unchanged by
// concurrency
func TestRun_SendMultiChannelOrder(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	cfg := exampleConfig(
		`{"id":"first","type":"`+fakeType+`","settings":{}}`,
		`{"id":"second","type":"`+fakeType+`","settings":{}}`,
		`{"id":"third","type":"`+fakeType+`","settings":{}}`,
	)
	path := writeFile(t, dir, "taskpush.json", cfg)

	for _, parallel := range []string{"1", "3", "8"} {
		result := runCLI(t, "send", "--config", path, "--parallel", parallel, "--title", "example")
		if result.code != exitOK {
			t.Fatalf("parallel %s = %d, stderr %q", parallel, result.code, result.stderr)
		}
		first := strings.Index(result.stdout, "first")
		second := strings.Index(result.stdout, "second")
		third := strings.Index(result.stdout, "third")
		if !(first < second && second < third) {
			t.Fatalf("parallel %s order = %d,%d,%d in %q", parallel, first, second, third, result.stdout)
		}
	}
}

// TestRun_SendChannelSelection 覆盖 --channel 选择与未知目标
//
// TestRun_SendChannelSelection covers --channel selection and an unknown target
func TestRun_SendChannelSelection(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	cfg := exampleConfig(
		`{"id":"ops","type":"`+fakeType+`","settings":{}}`,
		`{"id":"other","type":"`+fakeType+`","settings":{}}`,
	)
	path := writeFile(t, dir, "taskpush.json", cfg)

	selected := runCLI(t, "send", "--config", path, "--channel", "other", "--title", "example")
	if selected.code != exitOK {
		t.Fatalf("selected send = %d, stderr %q", selected.code, selected.stderr)
	}
	if strings.Contains(selected.stdout, "[OK] ops") || !strings.Contains(selected.stdout, "[OK] other") {
		t.Fatalf("selected stdout %q did not keep only the requested channel", selected.stdout)
	}

	unknown := runCLI(t, "send", "--config", path, "--channel", "absent", "--title", "example")
	if unknown.code != exitFailure {
		t.Fatalf("unknown channel = %d, want 1", unknown.code)
	}
}

// TestRun_SendFailure 覆盖渠道失败时的退出码与结果行
//
// TestRun_SendFailure covers the exit code and result line of a failed channel
func TestRun_SendFailure(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	cfg := exampleConfig(`{"id":"ops","type":"` + fakeType + `","settings":{"fail":true}}`)
	path := writeFile(t, dir, "taskpush.json", cfg)
	result := runCLI(t, "send", "--config", path, "--title", "example")
	if result.code != exitFailure {
		t.Fatalf("failed send = %d, want 1", result.code)
	}
	if !strings.Contains(result.stdout, "[ERROR] ops ("+fakeType+")") {
		t.Fatalf("stdout %q lost the error result line", result.stdout)
	}
}

// TestRun_Check 覆盖 check 表格, 警告与禁用渠道的延后错误
//
// TestRun_Check covers the check table, a warning and the deferred error of a disabled channel
func TestRun_Check(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	cfg := exampleConfig(
		`{"id":"ops","type":"`+fakeType+`","settings":{}}`,
		`{"id":"warned","type":"`+fakeType+`","settings":{"warn":true}}`,
		`{"id":"off","type":"`+fakeType+`","enabled":false,"settings":{"url":"${TASKPUSH_ABSENT}"}}`,
	)
	path := writeFile(t, dir, "taskpush.json", cfg)

	plain := runCLI(t, "check", "--config", path)
	if plain.code != exitOK {
		t.Fatalf("check = %d, stderr %q", plain.code, plain.stderr)
	}
	if !strings.Contains(plain.stdout, "[WARN]") {
		t.Fatalf("check %q lost the warning status", plain.stdout)
	}
	if !strings.Contains(plain.stderr, "check uses placeholder content") {
		t.Fatalf("stderr %q lost the placeholder note", plain.stderr)
	}
	if strings.Contains(plain.stdout, "off") {
		t.Fatalf("check %q listed a disabled channel without --all", plain.stdout)
	}

	all := runCLI(t, "check", "--config", path, "--all")
	if all.code != exitFailure {
		t.Fatalf("check --all = %d, want 1 for the deferred error", all.code)
	}
	if !strings.Contains(all.stdout, "TASKPUSH_ABSENT") {
		t.Fatalf("check --all %q lost the deferred variable", all.stdout)
	}
}

// TestRun_CheckAllValidatesDisabled 覆盖 --all 一并校验禁用渠道的 settings
// 禁用渠道在加载阶段跳过 settings 校验, 只有 --all 明确要求时才把它们的静态错误报出来
//
// TestRun_CheckAllValidatesDisabled covers --all validating the settings of disabled channels too
// Loading skips the settings check of a disabled channel, so their static errors surface only when
// --all asks for them explicitly
func TestRun_CheckAllValidatesDisabled(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	cfg := exampleConfig(
		`{"id":"on","type":"`+fakeType+`","settings":{}}`,
		`{"id":"off","type":"`+fakeType+`","enabled":false,"settings":{"nope":1}}`,
	)
	path := writeFile(t, dir, "taskpush.json", cfg)

	plain := runCLI(t, "check", "--config", path)
	if plain.code != exitOK {
		t.Fatalf("check = %d, want 0 without --all (stdout %q)", plain.code, plain.stdout)
	}
	if strings.Contains(plain.stdout, "off") {
		t.Fatalf("check %q listed a disabled channel without --all", plain.stdout)
	}

	all := runCLI(t, "check", "--config", path, "--all")
	if all.code != exitFailure {
		t.Fatalf("check --all = %d, want 1 for the bad disabled settings (stdout %q)", all.code, all.stdout)
	}
	if !strings.Contains(all.stdout, "off") || !strings.Contains(all.stdout, "[ERROR]") {
		t.Fatalf("check --all %q did not report the disabled channel", all.stdout)
	}
}

// TestRun_CheckAllDeferredRowShowsChannel 覆盖 --all 的延后错误行显示渠道名与类型而不是配置路径
// 第二个渠道故意出问题, 路径里的 1 基下标同时被断言, 用来固定面向用户的下标约定
//
// TestRun_CheckAllDeferredRowShowsChannel covers the deferred row of --all showing the channel name
// and type rather than the configuration path
// The second channel is the one at fault, and the one-based subscript in the path is asserted too,
// which pins the user-facing subscript convention
func TestRun_CheckAllDeferredRowShowsChannel(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	cfg := exampleConfig(
		`{"id":"ops","type":"`+fakeType+`","settings":{}}`,
		`{"id":"phone","type":"`+fakeType+`","enabled":false,"timeout":"${TASKPUSH_ABSENT}"}`,
	)
	path := writeFile(t, dir, "taskpush.json", cfg)

	result := runCLI(t, "check", "--config", path, "--all")
	if result.code != exitFailure {
		t.Fatalf("check --all = %d, want 1 for the deferred error (stdout %q)", result.code, result.stdout)
	}
	line := deferredRow(t, result.stdout)
	// 表格用 tabwriter 按显示宽度对齐, 列之间可能有多于一个空格, 因此按空白切分
	// The table uses tabwriter alignment by display width, so a column gap can hold more than one
	// space and the cells are split on whitespace
	cells := strings.Fields(line)
	if len(cells) < 5 {
		t.Fatalf("deferred row = %q, want at least five fields", line)
	}
	if cells[0] != "phone" {
		t.Fatalf("ID column = %q, want the channel id phone", cells[0])
	}
	if cells[1] != fakeType {
		t.Fatalf("TYPE column = %q, want the channel type %q", cells[1], fakeType)
	}
	if cells[2] != prefixError {
		t.Fatalf("STATUS column = %q, want %q", cells[2], prefixError)
	}
	if !strings.Contains(line, "channels[2].timeout") {
		t.Fatalf("row = %q, want the one-based path channels[2].timeout", line)
	}
}

// deferredRow 取出输出里唯一含 [ERROR] 的那一行, 前提是恰好只有一行
//
// deferredRow picks the single line containing [ERROR], which requires exactly one of them
func deferredRow(t *testing.T, stdout string) string {
	t.Helper()
	var rows []string
	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, prefixError) {
			rows = append(rows, line)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("stdout %q carries %d ERROR rows, want exactly one", stdout, len(rows))
	}
	return rows[0]
}

// TestRun_CheckAllDropsOkRowOfAFailedChannel 覆盖有延后错误的渠道不输出自己的 OK 行
// 同一渠道先显示 [OK] 再显示 [ERROR] 会读成自相矛盾, 因此 OK 与 WARN 行让位给错误行
// 渠道带上 settings 是为了让它进入 --all 的校验列表: 否则它只有 Deferred 一行, 抑制与否看不出差别
//
// TestRun_CheckAllDropsOkRowOfAFailedChannel covers a channel with a deferred error losing its own OK
// row
// The same channel showing [OK] and then [ERROR] reads as a contradiction, so the OK and WARN rows
// give way to the error row
// The settings make the channel enter the --all validation list, since without it the channel has a
// deferred row only and the suppression would not be observable
func TestRun_CheckAllDropsOkRowOfAFailedChannel(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	cfg := exampleConfig(
		`{"id":"ops","type":"`+fakeType+`","settings":{}}`,
		`{"id":"phone","type":"`+fakeType+`","enabled":false,"timeout":"${TASKPUSH_ABSENT}","settings":{}}`,
	)
	path := writeFile(t, dir, "taskpush.json", cfg)

	result := runCLI(t, "check", "--config", path, "--all")
	if result.code != exitFailure {
		t.Fatalf("check --all = %d, want 1 (stdout %q)", result.code, result.stdout)
	}

	var phoneRows []string
	var phoneOK int
	var opsOK int
	for _, line := range strings.Split(result.stdout, "\n") {
		if strings.HasPrefix(line, "phone") {
			phoneRows = append(phoneRows, line)
			if strings.Contains(line, prefixOK) {
				phoneOK++
			}
		}
		if strings.HasPrefix(line, "ops") && strings.Contains(line, prefixOK) {
			opsOK++
		}
	}
	if phoneOK != 0 {
		t.Fatalf("phone keeps %d OK rows, want the OK row suppressed by the deferred error (%v)", phoneOK, phoneRows)
	}
	if len(phoneRows) != 1 || !strings.Contains(phoneRows[0], prefixError) {
		t.Fatalf("phone rows = %v, want exactly one ERROR row", phoneRows)
	}
	if opsOK != 1 {
		t.Fatalf("healthy channel OK rows = %d, want 1 untouched", opsOK)
	}
}

// TestRun_CheckAllKeepsEveryError 覆盖同一渠道的多条错误全部保留, 互不覆盖
//
// TestRun_CheckAllKeepsEveryError covers every error of one channel being kept, with none replacing
// another
func TestRun_CheckAllKeepsEveryError(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	cfg := exampleConfig(
		`{"id":"phone","type":"` + fakeType + `","enabled":false,` +
			`"timeout":"${TASKPUSH_ABSENT_TIMEOUT}","retries":"${TASKPUSH_ABSENT_RETRIES}",` +
			`"retryDelay":"${TASKPUSH_ABSENT_DELAY}"}`,
	)
	path := writeFile(t, dir, "taskpush.json", cfg)

	result := runCLI(t, "check", "--config", path, "--all")
	if result.code != exitFailure {
		t.Fatalf("check --all = %d, want 1 (stdout %q)", result.code, result.stdout)
	}

	var errors int
	for _, line := range strings.Split(result.stdout, "\n") {
		if strings.HasPrefix(line, "phone") && strings.Contains(line, prefixError) {
			errors++
		}
	}
	if errors != 3 {
		t.Fatalf("ERROR rows = %d, want one per failed scalar (stdout %q)", errors, result.stdout)
	}
	for _, want := range []string{"TASKPUSH_ABSENT_TIMEOUT", "TASKPUSH_ABSENT_RETRIES", "TASKPUSH_ABSENT_DELAY"} {
		if !strings.Contains(result.stdout, want) {
			t.Fatalf("stdout %q lost the error for %s", result.stdout, want)
		}
	}
}

// TestRun_CheckAllKeepsCheckAndDeferredErrors 覆盖校验错误与延后错误各自成行
// 抑制只针对 OK 与 WARN, 错误行永远不会被另一条错误顶掉
//
// TestRun_CheckAllKeepsCheckAndDeferredErrors covers a validation error and a deferred error keeping
// their own rows
// The suppression targets OK and WARN only, and no error row is ever displaced by another error
func TestRun_CheckAllKeepsCheckAndDeferredErrors(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	cfg := exampleConfig(
		`{"id":"phone","type":"` + fakeType + `","enabled":false,` +
			`"timeout":"${TASKPUSH_ABSENT}","settings":{"nope":1}}`,
	)
	path := writeFile(t, dir, "taskpush.json", cfg)

	result := runCLI(t, "check", "--config", path, "--all")
	if result.code != exitFailure {
		t.Fatalf("check --all = %d, want 1 (stdout %q)", result.code, result.stdout)
	}

	var errors int
	for _, line := range strings.Split(result.stdout, "\n") {
		if strings.HasPrefix(line, "phone") && strings.Contains(line, prefixError) {
			errors++
		}
	}
	if errors != 2 {
		t.Fatalf("ERROR rows = %d, want the settings error and the deferred error (stdout %q)", errors, result.stdout)
	}
	if !strings.Contains(result.stdout, "nope") {
		t.Fatalf("stdout %q lost the settings error", result.stdout)
	}
	if !strings.Contains(result.stdout, "TASKPUSH_ABSENT") {
		t.Fatalf("stdout %q lost the deferred error", result.stdout)
	}
}

// TestRun_NetworkErrorCopy 覆盖连接类错误走请求失败文案而不是坏响应文案
// 连接被拒这类错误根本没拿到响应, 用「响应字段缺失」描述会误导排查方向
//
// TestRun_NetworkErrorCopy covers a connection failure rendering through the request-failed copy
// rather than the bad-response copy
// A refused connection never reaches a response, so describing it as a missing response field points
// the reader in the wrong direction
func TestRun_NetworkErrorCopy(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	cfg := exampleConfig(`{"id":"ops","type":"` + fakeHTTPType + `","settings":{"url":"http://127.0.0.1:1/hook"}}`)
	path := writeFile(t, dir, "taskpush.json", cfg)

	result := runCLI(t, "send", "--config", path, "--title", "example")
	if result.code != exitFailure {
		t.Fatalf("send to a refused port = %d, want %d", result.code, exitFailure)
	}
	if !strings.Contains(result.stdout, "the request failed") {
		t.Fatalf("stdout %q lost the request-failed copy", result.stdout)
	}
	if strings.Contains(result.stdout, "is missing or malformed") {
		t.Fatalf("stdout %q still describes a transport failure as a bad response", result.stdout)
	}
	if !strings.Contains(result.stdout, "<redacted-url>") {
		t.Fatalf("stdout %q lost the URL redaction", result.stdout)
	}
}

// TestRun_CheckNoChannels 覆盖没有启用渠道时的提示与退出 0
//
// TestRun_CheckNoChannels covers the note and the 0 exit with no enabled channel
func TestRun_CheckNoChannels(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	cfg := exampleConfig(`{"id":"off","type":"` + fakeType + `","enabled":false,"settings":{}}`)
	path := writeFile(t, dir, "taskpush.json", cfg)
	result := runCLI(t, "check", "--config", path)
	if result.code != exitOK {
		t.Fatalf("check with no channel = %d, want 0 (stderr %q)", result.code, result.stderr)
	}
	if !strings.Contains(result.stderr, "no channel to check") {
		t.Fatalf("stderr %q lost the informational note", result.stderr)
	}
}

// TestRun_CheckAllDisabled 覆盖全部渠道都被禁用时 --all 仍然报出延后错误
// 这条路径不经过 notify.Check, 延后错误只能从已加载的 root 里取
//
// TestRun_CheckAllDisabled covers --all still reporting the deferred error when every channel is
// disabled
// This path never reaches notify.Check, so the deferred error has to come from the loaded root
func TestRun_CheckAllDisabled(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	cfg := exampleConfig(`{"id":"off","type":"` + fakeType + `","enabled":false,"settings":{"url":"${TASKPUSH_ABSENT}"}}`)
	path := writeFile(t, dir, "taskpush.json", cfg)

	plain := runCLI(t, "check", "--config", path)
	if plain.code != exitOK {
		t.Fatalf("check with every channel disabled = %d, want 0 (stdout %q)", plain.code, plain.stdout)
	}

	all := runCLI(t, "check", "--config", path, "--all")
	if all.code != exitFailure {
		t.Fatalf("check --all with every channel disabled = %d, want 1 (stdout %q)", all.code, all.stdout)
	}
	if !strings.Contains(all.stdout, "TASKPUSH_ABSENT") {
		t.Fatalf("check --all %q lost the deferred variable", all.stdout)
	}
}

// TestRun_StartFinish 覆盖 start 与 finish 的完整计时流程
//
// TestRun_StartFinish covers the full timed flow of start and finish
func TestRun_StartFinish(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	path := filepath.Join(dir, "timed-state.json")
	cfg := exampleConfig(`{"id":"ops","type":"` + fakeType + `","settings":{}}`)
	configPath := writeFile(t, dir, "taskpush.json", cfg)

	start := runCLI(t, "start", "--state-file", path)
	if start.code != exitOK {
		t.Fatalf("start = %d, stderr %q", start.code, start.stderr)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("start did not write the state file: %v", err)
	}

	finish := runCLI(t, "finish", "--config", configPath, "--state-file", path, "--title", "elapsed {{elapsed}}")
	if finish.code != exitOK {
		t.Fatalf("finish = %d, stderr %q", finish.code, finish.stderr)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("finish left the state file behind: %v", err)
	}
	if !strings.Contains(finish.stderr, "state file removed") {
		t.Fatalf("stderr %q lost the removal note", finish.stderr)
	}
}

// TestRun_StartDryRun 覆盖 start --dry-run 不落盘
//
// TestRun_StartDryRun covers start --dry-run writing nothing
func TestRun_StartDryRun(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	path := filepath.Join(dir, "dry-state.json")
	result := runCLI(t, "start", "--state-file", path, "--dry-run")
	if result.code != exitOK {
		t.Fatalf("start --dry-run = %d, stderr %q", result.code, result.stderr)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("start --dry-run wrote the state file")
	}
}

// TestRun_FinishKeepState 覆盖 --keep-state 成功后保留状态文件
//
// TestRun_FinishKeepState covers --keep-state keeping the file after success
func TestRun_FinishKeepState(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	path := filepath.Join(dir, "keep-state.json")
	cfg := exampleConfig(`{"id":"ops","type":"` + fakeType + `","settings":{}}`)
	configPath := writeFile(t, dir, "taskpush.json", cfg)

	if code := runCLI(t, "start", "--state-file", path).code; code != exitOK {
		t.Fatalf("start = %d", code)
	}
	result := runCLI(t, "finish", "--config", configPath, "--state-file", path, "--keep-state", "--title", "example")
	if result.code != exitOK {
		t.Fatalf("finish --keep-state = %d, stderr %q", result.code, result.stderr)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("finish --keep-state removed the state file: %v", err)
	}
}

// TestRun_FinishFailureKeepsState 覆盖发送失败时保留状态文件
//
// TestRun_FinishFailureKeepsState covers the state file being kept when the send fails
func TestRun_FinishFailureKeepsState(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	path := filepath.Join(dir, "fail-state.json")
	cfg := exampleConfig(`{"id":"ops","type":"` + fakeType + `","settings":{"fail":true}}`)
	configPath := writeFile(t, dir, "taskpush.json", cfg)

	if code := runCLI(t, "start", "--state-file", path).code; code != exitOK {
		t.Fatalf("start = %d", code)
	}
	result := runCLI(t, "finish", "--config", configPath, "--state-file", path, "--title", "example")
	if result.code != exitFailure {
		t.Fatalf("failed finish = %d, want 1", result.code)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("failed finish removed the state file: %v", err)
	}
	if !strings.Contains(result.stderr, "a rerun sends to the channels that already succeeded again") {
		t.Fatalf("stderr %q lost the rerun note", result.stderr)
	}
}

// TestRun_FinishMissingState 覆盖缺失状态文件的错误
//
// TestRun_FinishMissingState covers the error of a missing state file
func TestRun_FinishMissingState(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	path := filepath.Join(dir, "absent-state.json")
	result := runCLI(t, "finish", "--state-file", path, "--type", fakeType)
	if result.code != exitFailure {
		t.Fatalf("finish without state = %d, want 1", result.code)
	}
	if !strings.Contains(result.stderr, "run start first") {
		t.Fatalf("stderr %q lost the start hint", result.stderr)
	}
}

// TestRun_FinishFutureStart 覆盖起点在未来时的退出码与警告
// 耗时归零的行为由 notify 包的 TestSend_FutureStartTimeClampsElapsedToZero 钉住
//
// TestRun_FinishFutureStart covers the exit code and the warning of a future start
// The zero elapsed time is pinned by TestSend_FutureStartTimeClampsElapsedToZero in the notify package
func TestRun_FinishFutureStart(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	path := filepath.Join(dir, "future-state.json")
	if err := state.Write(path, time.Now().Add(2*time.Hour)); err != nil {
		t.Fatalf("write state: %v", err)
	}
	cfg := exampleConfig(`{"id":"ops","type":"` + fakeType + `","settings":{}}`)
	configPath := writeFile(t, dir, "taskpush.json", cfg)

	result := runCLI(t, "finish", "--config", configPath, "--state-file", path, "--title", "example")
	if result.code != exitOK {
		t.Fatalf("future start = %d, stderr %q", result.code, result.stderr)
	}
	if !strings.Contains(result.stderr, "future") {
		t.Fatalf("stderr %q lost the future warning", result.stderr)
	}
}

// TestRun_Init 覆盖 init 的创建, 已存在与 --force
//
// TestRun_Init covers creating, seeing an existing file and --force
func TestRun_Init(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	path := filepath.Join(dir, "taskpush.json")

	created := runCLI(t, "init")
	if created.code != exitOK {
		t.Fatalf("init = %d, stderr %q", created.code, created.stderr)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("init did not write the file: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("init wrote invalid JSON: %v", err)
	}
	if strings.Contains(string(data), "token") {
		t.Fatalf("init document carries a secret-shaped field:\n%s", data)
	}

	existing := runCLI(t, "init")
	if existing.code != exitOK || !strings.Contains(existing.stderr, "already exists") {
		t.Fatalf("second init = %d %q, want the existing note", existing.code, existing.stderr)
	}

	forced := runCLI(t, "init", "--force")
	if forced.code != exitOK || !strings.Contains(forced.stderr, "wrote") {
		t.Fatalf("init --force = %d %q", forced.code, forced.stderr)
	}
}

// TestRun_StatePath 覆盖状态路径的四种来源
//
// TestRun_StatePath covers the four sources of the state path
func TestRun_StatePath(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)

	// --state-file 覆盖一切
	//
	// --state-file overrides everything
	path, err := statePath(&options{ConfigPath: "config/taskpush.json", StateFile: "custom.json"})
	if err != nil || path != "custom.json" {
		t.Fatalf("statePath = %q, %v; want custom.json", path, err)
	}

	// --config PATH 同目录
	//
	// The directory of the --config file
	path, err = statePath(&options{ConfigPath: filepath.Join(dir, "config", "taskpush.json")})
	if err != nil {
		t.Fatalf("statePath: %v", err)
	}
	if want := filepath.Join(dir, "config", ".taskpush-state.json"); path != want {
		t.Fatalf("statePath = %q, want %q", path, want)
	}

	// --config - 落到工作目录, 但 start 上的该写法在解析阶段就被拒绝
	//
	// --config - lands in the working directory, while start rejects that form during parsing
	path, err = statePath(&options{ConfigPath: "-"})
	if err != nil {
		t.Fatalf("statePath: %v", err)
	}
	if want := filepath.Join(dir, ".taskpush-state.json"); path != want {
		t.Fatalf("statePath = %q, want %q", path, want)
	}

	// 内联模式落到工作目录
	//
	// Inline mode lands in the working directory
	path, err = statePath(&options{Webhook: "https://example.com/hook"})
	if err != nil {
		t.Fatalf("statePath: %v", err)
	}
	if want := filepath.Join(dir, ".taskpush-state.json"); path != want {
		t.Fatalf("statePath = %q, want %q", path, want)
	}
}

// TestRun_CanceledContext 覆盖已取消的上下文返回 130
//
// TestRun_CanceledContext covers a cancelled context returning 130
func TestRun_CanceledContext(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	cfg := exampleConfig(`{"id":"ops","type":"` + fakeType + `","settings":{}}`)
	path := writeFile(t, dir, "taskpush.json", cfg)

	var stdout, stderr strings.Builder
	code := Run(t.Context(), []string{"send", "--config", path, "--title", "example"}, "test-version", &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("live context = %d, stderr %q", code, stderr.String())
	}

	canceled := canceledContext(t)
	code = Run(canceled, []string{"send", "--config", path, "--title", "example"}, "test-version", &stdout, &stderr)
	if code != exitCanceled {
		t.Fatalf("cancelled context = %d, want 130", code)
	}
	if !strings.Contains(stderr.String(), "cancelled") {
		t.Fatalf("stderr %q lost the cancellation copy", stderr.String())
	}
}

// TestRun_ReadContentEnvExpansion 覆盖 --body 的环境变量展开与 --title/--body 的展开失败
// 展开规则与配置里的字符串一致: ${VAR} 取值, ${VAR:-默认值} 允许缺省, 未设置且无默认值则报错
// 假渠道预览的字节数等于展开并渲染后的正文长度, 因此据此断言展开发生
//
// TestRun_ReadContentEnvExpansion covers the environment expansion of --body and the failure of an
// unset reference in --title and --body
// The rules match a configuration string: ${VAR} resolves, ${VAR:-default} allows a missing value,
// and an unset variable without a default fails
// The fake channel's preview byte count equals the expanded, rendered body, which proves expansion
func TestRun_ReadContentEnvExpansion(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	cfg := exampleConfig(`{"id":"ops","type":"` + fakeType + `","settings":{}}`)
	path := writeFile(t, dir, "taskpush.json", cfg)

	cases := []struct {
		name string
		env  map[string]string
		body string
		want string
	}{
		{name: "resolved", env: map[string]string{"REPO": "TaskPush"}, body: "${REPO}", want: "TaskPush"},
		{name: "fallback", env: map[string]string{}, body: "${REPO:-fb}", want: "fb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withEnv(t, tc.env)
			result := runCLI(t, "send", "--config", path, "--title", "t", "--body", tc.body, "--dry-run")
			if result.code != exitOK {
				t.Fatalf("send = %d, stderr %q", result.code, result.stderr)
			}
			if !strings.Contains(result.stdout, fmt.Sprintf("(text, %d bytes)", len(tc.want))) {
				t.Fatalf("preview %q, want a %d-byte body from the expanded %q", result.stdout, len(tc.want), tc.want)
			}
			if strings.Contains(result.stdout, "${") {
				t.Fatalf("preview %q still carries a raw reference", result.stdout)
			}
		})
	}

	// 未设置且无默认值: 标题与正文都退出 1, 错误点出变量名与对应 flag
	withEnv(t, map[string]string{})
	title := runCLI(t, "send", "--config", path, "--title", "${REPO_ABSENT}", "--body", "example")
	if title.code != exitFailure {
		t.Fatalf("unset title = %d, want 1 (stdout %q)", title.code, title.stdout)
	}
	if !strings.Contains(title.stderr, "REPO_ABSENT") || !strings.Contains(title.stderr, "--title") {
		t.Fatalf("title stderr %q lost the variable name or the flag", title.stderr)
	}

	body := runCLI(t, "send", "--config", path, "--title", "example", "--body", "${REPO_ABSENT}")
	if body.code != exitFailure {
		t.Fatalf("unset body = %d, want 1 (stdout %q)", body.code, body.stdout)
	}
	if !strings.Contains(body.stderr, "--body") {
		t.Fatalf("body stderr %q lost the flag", body.stderr)
	}
}

// TestRun_ReadContentFiles 覆盖标题正文从文件与 stdin 读取
//
// TestRun_ReadContentFiles covers the title and body read from a file and from stdin
func TestRun_ReadContentFiles(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	titlePath := writeFile(t, dir, "title.txt", "example title")
	bodyPath := writeFile(t, dir, "body.txt", "example body")
	cfg := exampleConfig(`{"id":"ops","type":"` + fakeType + `","settings":{}}`)
	configPath := writeFile(t, dir, "taskpush.json", cfg)

	result := runCLI(t, "send", "--config", configPath, "--title-file", titlePath, "--body-file", bodyPath)
	if result.code != exitOK {
		t.Fatalf("file content = %d, stderr %q", result.code, result.stderr)
	}

	missing := runCLI(t, "send", "--config", configPath, "--title-file", filepath.Join(dir, "absent.txt"))
	if missing.code != exitFailure {
		t.Fatalf("missing title file = %d, want 1", missing.code)
	}
}

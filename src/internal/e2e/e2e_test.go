package e2e

// E2E 验收测试: 通过 cli.Run 驱动真实 CLI, 用 channel/all 注册九个真实渠道,
// HTTP 只访问 httptest 本地服务, 覆盖 14-acceptance.md 中可在进程内执行的场景
//
// End-to-end acceptance tests: they drive the real CLI through cli.Run, register the nine real
// channels through channel/all, reach httptest loopback servers only, and cover the scenarios of
// 14-acceptance.md that can run inside the process

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/MarchSnow-1/TaskPush/internal/channel/all"
	"github.com/MarchSnow-1/TaskPush/internal/cli"
)

// recorded 是一次被本地服务收到的请求
//
// recorded is one request a loopback server received
type recorded struct {
	Method      string
	Path        string
	ContentType string
	Body        string
}

// recorder 是一个记录请求并统一回 200 的本地服务
//
// recorder is a loopback server that records requests and always answers 200
type recorder struct {
	mu   sync.Mutex
	reqs []recorded
	srv  *httptest.Server
}

// newRecorder 启动一个本地记录服务并在测试结束时关闭
//
// newRecorder starts a loopback recording server and closes it when the test ends
func newRecorder(t *testing.T) *recorder {
	t.Helper()
	r := &recorder{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.reqs = append(r.reqs, recorded{
			Method:      req.Method,
			Path:        req.URL.Path,
			ContentType: req.Header.Get("Content-Type"),
			Body:        string(body),
		})
		r.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"code":200}`))
	}))
	t.Cleanup(r.srv.Close)
	return r
}

// requests 返回已记录请求的副本
//
// requests returns a copy of the recorded requests
func (r *recorder) requests() []recorded {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recorded, len(r.reqs))
	copy(out, r.reqs)
	return out
}

// inTempDir 把进程工作目录切到临时目录并在测试结束恢复
//
// inTempDir switches the process working directory to a temporary directory and restores it when
// the test ends
func inTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	return dir
}

// runCLI 执行一次真实 CLI 调用
//
// runCLI performs one real CLI invocation
func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cli.Run(context.Background(), args, "test-version", &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// writeFile 写一个测试配置文件
//
// writeFile writes a test configuration file
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// multiChannelConfig 构造 a/b 两个启用渠道与一个禁用渠道的配置
//
// multiChannelConfig builds a configuration with two enabled channels and one disabled channel
func multiChannelConfig(baseURL string) string {
	return fmt.Sprintf(`{
  "version": 1,
  "channels": [
    {"id":"a","type":"webhook","enabled":true,"settings":{"url":"%s/a","body":{"format":"text","text":"{{title}}"}}},
    {"id":"b","type":"webhook","enabled":true,"settings":{"url":"%s/b","body":{"format":"text","text":"{{body}}"}}},
    {"id":"off","type":"webhook","enabled":false,"settings":{"url":"%s/off"}}
  ]
}`, baseURL, baseURL, baseURL)
}

// TestScenario1InlineWebhook 覆盖无配置文件的内联 webhook 发送
//
// TestScenario1InlineWebhook covers an inline webhook send without any configuration file
func TestScenario1InlineWebhook(t *testing.T) {
	inTempDir(t)
	rec := newRecorder(t)

	code, stdout, _ := runCLI(t, "send", "--webhook", rec.srv.URL+"/hook", "--title", "Build done", "--body", "commit abc123")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	reqs := rec.requests()
	if len(reqs) != 1 {
		t.Fatalf("server received %d requests, want 1", len(reqs))
	}
	if reqs[0].Method != http.MethodPost {
		t.Fatalf("method = %s, want POST", reqs[0].Method)
	}
	if !strings.Contains(reqs[0].ContentType, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain", reqs[0].ContentType)
	}
	if !strings.Contains(reqs[0].Body, "Build done") || !strings.Contains(reqs[0].Body, "commit abc123") {
		t.Fatalf("body = %q, want title and body", reqs[0].Body)
	}
	if !strings.Contains(stdout, "[OK] inline (webhook)") {
		t.Fatalf("stdout = %q, want the inline webhook result line", stdout)
	}
}

// TestScenario1bContentEnvExpansion 覆盖 --title 与 --body 的环境变量展开
// webhook 的缺省正文是 {{title}} 加空行加 {{body}}, 因此捕获到的正文同时反映两处展开
//
// TestScenario1bContentEnvExpansion covers the environment expansion of --title and --body
// The default webhook body is {{title}} plus a blank line plus {{body}}, so the captured body shows
// both expansions
func TestScenario1bContentEnvExpansion(t *testing.T) {
	inTempDir(t)
	rec := newRecorder(t)
	t.Setenv("TP_E2E_REPO", "TaskPush")

	code, _, stderr := runCLI(t, "send", "--webhook", rec.srv.URL+"/hook",
		"--title", "${TP_E2E_REPO} 构建", "--body", "仓库 ${TP_E2E_REPO}")
	if code != 0 {
		t.Fatalf("exit = %d, stderr %q", code, stderr)
	}
	reqs := rec.requests()
	if len(reqs) != 1 {
		t.Fatalf("server received %d requests, want 1", len(reqs))
	}
	want := "TaskPush 构建\n\n仓库 TaskPush"
	if reqs[0].Body != want {
		t.Fatalf("body = %q, want %q", reqs[0].Body, want)
	}
	if strings.Contains(reqs[0].Body, "${") {
		t.Fatalf("body %q still carries a raw reference", reqs[0].Body)
	}
}

// TestScenario2PayloadJSON 覆盖 --payload 的 JSON Content-Type 与数字类型保真
//
// TestScenario2PayloadJSON covers the JSON Content-Type of --payload and numeric type fidelity
func TestScenario2PayloadJSON(t *testing.T) {
	inTempDir(t)
	rec := newRecorder(t)

	code, _, _ := runCLI(t, "send", "--webhook", rec.srv.URL+"/hook",
		"--payload", `{"text":"{{title}}: {{body}}","count":"{{count}}"}`,
		"--title", "Build done", "--body", "ok", "--var", "count=5")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	reqs := rec.requests()
	if len(reqs) != 1 {
		t.Fatalf("server received %d requests, want 1", len(reqs))
	}
	if !strings.Contains(reqs[0].ContentType, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", reqs[0].ContentType)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(reqs[0].Body), &body); err != nil {
		t.Fatalf("payload is not JSON: %v (%s)", err, reqs[0].Body)
	}
	if got, ok := body["count"].(float64); !ok || got != 5 {
		t.Fatalf("count = %#v, want the JSON number 5", body["count"])
	}
	if body["text"] != "Build done: ok" {
		t.Fatalf("text = %#v", body["text"])
	}
}

// TestScenario3InlineProviderDryRun 覆盖内联 provider 的 dry-run 预览不含密钥
//
// TestScenario3InlineProviderDryRun covers an inline provider dry run whose preview holds no secret
func TestScenario3InlineProviderDryRun(t *testing.T) {
	inTempDir(t)

	code, stdout, stderr := runCLI(t, "send", "--type", "telegram",
		"--set", "token=example-token", "--set", "chatIds=987654321",
		"--title", "T", "--body", "ok", "--dry-run")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, stderr)
	}
	if !strings.Contains(stdout, "telegram: 1 chats") {
		t.Fatalf("stdout = %q, want the telegram preview", stdout)
	}
	combined := stdout + stderr
	for _, secret := range []string{"example-token", "987654321"} {
		if strings.Contains(combined, secret) {
			t.Fatalf("output leaks %q:\n%s", secret, combined)
		}
	}
}

// TestScenario4EnvMultiChannel 覆盖 TASKPUSH_CONFIG_JSON 的多渠道与禁用渠道
//
// TestScenario4EnvMultiChannel covers multiple channels through TASKPUSH_CONFIG_JSON and a disabled
// channel
func TestScenario4EnvMultiChannel(t *testing.T) {
	inTempDir(t)
	rec := newRecorder(t)
	t.Setenv("TASKPUSH_CONFIG_JSON", multiChannelConfig(rec.srv.URL))

	code, stdout, _ := runCLI(t, "send", "--title", "T", "--body", "B")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	reqs := rec.requests()
	if len(reqs) != 2 {
		t.Fatalf("server received %d requests, want 2", len(reqs))
	}
	if reqs[0].Path != "/a" || reqs[1].Path != "/b" {
		t.Fatalf("paths = %s, %s; want /a then /b", reqs[0].Path, reqs[1].Path)
	}
	if strings.Index(stdout, "[OK] a (webhook)") > strings.Index(stdout, "[OK] b (webhook)") {
		t.Fatalf("results are not in configuration order:\n%s", stdout)
	}
}

// TestScenario8ChannelSubset 覆盖 --channel 子集与禁用目标
//
// TestScenario8ChannelSubset covers the --channel subset and a disabled target
func TestScenario8ChannelSubset(t *testing.T) {
	inTempDir(t)
	rec := newRecorder(t)
	t.Setenv("TASKPUSH_CONFIG_JSON", multiChannelConfig(rec.srv.URL))

	code, _, _ := runCLI(t, "send", "--channel", "a", "--title", "T", "--body", "B")
	if code != 0 {
		t.Fatalf("subset exit = %d, want 0", code)
	}
	reqs := rec.requests()
	if len(reqs) != 1 || reqs[0].Path != "/a" {
		t.Fatalf("subset requests = %+v, want only /a", reqs)
	}

	code, _, stderr := runCLI(t, "send", "--channel", "off", "--title", "T", "--body", "B")
	if code != 1 {
		t.Fatalf("disabled exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, "disabled") {
		t.Fatalf("stderr = %q, want a disabled-channel error", stderr)
	}
}

// TestScenario9CheckAndList 覆盖 check 的静态错误、--all 的禁用渠道延迟错误与 list
//
// TestScenario9CheckAndList covers check static errors, the deferred disabled-channel error under
// --all, and list
func TestScenario9CheckAndList(t *testing.T) {
	dir := inTempDir(t)
	const unsetVar = "TASKPUSH_E2E_UNSET_VARIABLE"
	path := writeFile(t, dir, "bad.json", fmt.Sprintf(`{
  "version": 1,
  "channels": [
    {"id":"bad-key","type":"webhook","settings":{"url":"https://example.com/hook","metod":"POST"}},
    {"id":"missing-url","type":"webhook","settings":{}},
    {"id":"bad-tpl","type":"webhook","settings":{"url":"https://example.com/hook","body":{"format":"text","text":"{{if x}}"}}},
    {"id":"off","type":"webhook","enabled":false,"settings":{"url":"${%s}"}}
  ]
}`, unsetVar))

	code, stdout, _ := runCLI(t, "list", "--config", path)
	if code != 0 {
		t.Fatalf("list exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "off") {
		t.Fatalf("list output misses the disabled channel:\n%s", stdout)
	}

	code, stdout, stderr := runCLI(t, "check", "--config", path)
	if code != 1 {
		t.Fatalf("check exit = %d, want 1", code)
	}
	combined := stdout + stderr
	for _, want := range []string{"metod", "url is required", "not closed"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("check output misses %q:\n%s", want, combined)
		}
	}
	if strings.Contains(combined, unsetVar) {
		t.Fatalf("default check reported the disabled channel variable:\n%s", combined)
	}

	code, stdout, stderr = runCLI(t, "check", "--all", "--config", path)
	if code != 1 {
		t.Fatalf("check --all exit = %d, want 1", code)
	}
	if combined := stdout + stderr; !strings.Contains(combined, unsetVar) {
		t.Fatalf("check --all misses the deferred variable error:\n%s", combined)
	}
}

// TestScenario11UnresolvedVariable 覆盖严格错误、check 警告与 \{{ 转义
//
// TestScenario11UnresolvedVariable covers the strict error, the check warning and the \{{ escape
func TestScenario11UnresolvedVariable(t *testing.T) {
	inTempDir(t)
	rec := newRecorder(t)

	code, stdout, stderr := runCLI(t, "send", "--webhook", rec.srv.URL+"/hook", "--title", "T", "--body", "{{unknown}}")
	if code != 1 {
		t.Fatalf("send exit = %d, want 1", code)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "unknown") || !strings.Contains(combined, "--body") {
		t.Fatalf("error misses the variable or --body:\n%s", combined)
	}

	code, _, _ = runCLI(t, "send", "--webhook", rec.srv.URL+"/hook", "--title", "T", "--body", `\{{unknown}}`, "--dry-run")
	if code != 0 {
		t.Fatalf("escaped send exit = %d, want 0", code)
	}

	dir := t.TempDir()
	path := writeFile(t, dir, "warn.json", fmt.Sprintf(`{
  "version": 1,
  "channels": [{"id":"w","type":"webhook","settings":{"url":"%s/w","body":{"format":"text","text":"{{missing}}"}}}]
}`, rec.srv.URL))
	code, stdout, _ = runCLI(t, "check", "--config", path)
	if code != 0 {
		t.Fatalf("check with an unresolved variable exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "[WARN]") {
		t.Fatalf("check output misses the warning:\n%s", stdout)
	}
}

// TestScenario12Conflicts 覆盖用法错误统一退出 2
//
// TestScenario12Conflicts covers usage errors all exiting with 2
func TestScenario12Conflicts(t *testing.T) {
	inTempDir(t)
	rec := newRecorder(t)
	cases := [][]string{
		{"send", "--bogus"},
		{"send", "--webhook", rec.srv.URL, "--config", "missing.json"},
		{"send", "--payload", `{}`},
		{"send", "--set", "token=x"},
		{"send", "--parallel", "0"},
		{"list", "--title", "T"},
		{"start", "--config", "-"},
	}
	for _, args := range cases {
		if code, _, _ := runCLI(t, args...); code != 2 {
			t.Fatalf("%v exit = %d, want 2", args, code)
		}
	}
}

// TestScenario13Language 覆盖默认英文、中文与非法语言
//
// TestScenario13Language covers English by default, Chinese, and an unsupported language
func TestScenario13Language(t *testing.T) {
	inTempDir(t)

	code, stdout, _ := runCLI(t, "--help")
	if code != 0 || !strings.Contains(stdout, "Usage:") {
		t.Fatalf("default help exit = %d, stdout = %q", code, stdout)
	}
	code, stdout, _ = runCLI(t, "--lang", "zh-CN", "--help")
	if code != 0 || !strings.Contains(stdout, "用法") {
		t.Fatalf("Chinese help exit = %d, stdout = %q", code, stdout)
	}
	code, _, stderr := runCLI(t, "--lang", "fr", "--help")
	if code != 2 || !strings.Contains(stderr, "en, zh-CN") {
		t.Fatalf("unsupported language exit = %d, stderr = %q", code, stderr)
	}
}

// TestScenario14StateFlow 覆盖 start/finish 的删除、保留与缺失
//
// TestScenario14StateFlow covers start and finish removing, keeping and missing the state file
func TestScenario14StateFlow(t *testing.T) {
	dir := inTempDir(t)
	rec := newRecorder(t)
	state := filepath.Join(dir, "state.json")

	code, _, _ := runCLI(t, "start", "--state-file", state)
	if code != 0 {
		t.Fatalf("start exit = %d, want 0", code)
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatalf("state file missing after start: %v", err)
	}

	code, _, _ = runCLI(t, "finish", "--state-file", state, "--webhook", rec.srv.URL+"/finish",
		"--title", "Done", "--body", "elapsed {{elapsed}}")
	if code != 0 {
		t.Fatalf("finish exit = %d, want 0", code)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("state file still exists after a successful finish: %v", err)
	}
	reqs := rec.requests()
	if len(reqs) != 1 || !strings.Contains(reqs[0].Body, "elapsed ") {
		t.Fatalf("finish requests = %+v", reqs)
	}

	if code, _, _ := runCLI(t, "start", "--state-file", state); code != 0 {
		t.Fatalf("second start exit = %d", code)
	}
	if code, _, _ := runCLI(t, "finish", "--state-file", state, "--keep-state",
		"--webhook", rec.srv.URL+"/keep", "--title", "Done"); code != 0 {
		t.Fatalf("finish --keep-state exit = %d", code)
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatalf("state file was removed despite --keep-state: %v", err)
	}

	_ = os.Remove(state)
	if code, _, stderr := runCLI(t, "finish", "--state-file", state, "--webhook", rec.srv.URL); code != 1 {
		t.Fatalf("missing state exit = %d, want 1", code)
	} else if !strings.Contains(stderr, "run start first") {
		t.Fatalf("missing state stderr = %q", stderr)
	}
}

// TestScenario18DocsExample 覆盖文档示例配置的 check --all
//
// TestScenario18DocsExample covers check --all on the documented example configuration
func TestScenario18DocsExample(t *testing.T) {
	// 路径必须在切换工作目录之前解析, 否则相对路径会落到临时目录下
	//
	// The path has to resolve before the working directory changes, otherwise the relative path
	// lands under the temporary directory
	example, err := filepath.Abs(filepath.Join("..", "..", "..", "docs", "taskpush.example.json"))
	if err != nil {
		t.Fatalf("resolve the example path: %v", err)
	}
	inTempDir(t)
	env := map[string]string{
		"HOOK_URL":         "https://example.com/hook",
		"HOOK_TOKEN":       "example-token",
		"BARK_TOKEN":       "example-bark-key",
		"TG_TOKEN":         "example-telegram-token",
		"TG_CHAT_ID":       "123456789",
		"SERVERCHAN_TOKEN": "example-serverchan-key",
		"DINGTALK_WEBHOOK": "https://example.com/robot/send?access_token=example-access-token",
		"DINGTALK_SECRET":  "SECexample-secret",
		"WECOM_WEBHOOK":    "https://example.com/cgi-bin/webhook/send?key=example-key",
		"DISCORD_WEBHOOK":  "https://example.com/api/webhooks/123456789/example-token",
		"NTFY_TOKEN":       "example-ntfy-token",
		"GOTIFY_TOKEN":     "example-gotify-token",
	}
	for key, value := range env {
		t.Setenv(key, value)
	}

	code, stdout, stderr := runCLI(t, "check", "--all", "--config", example)
	if code != 0 {
		t.Fatalf("check --all exit = %d, want 0 (stderr %q)", code, stderr)
	}
	if combined := stdout + stderr; strings.Contains(combined, "[ERROR]") {
		t.Fatalf("check --all reported an error row:\n%s", combined)
	}
}

// TestScenario7Parallel 覆盖 --parallel 的渠道间并发与结果顺序
// 服务端记录同时在处理的请求峰值, 三个渠道配 80ms 延迟, 并行 3 时峰值应大于 1
//
// TestScenario7Parallel covers channel-level concurrency under --parallel and the result order
// The server records the peak number of requests in flight; with three channels and an 80ms delay,
// a parallel of three has a peak above one
func TestScenario7Parallel(t *testing.T) {
	var mu sync.Mutex
	active, peak := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		active++
		if active > peak {
			peak = active
		}
		mu.Unlock()
		time.Sleep(80 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	inTempDir(t)
	cfg := fmt.Sprintf(`{
  "version": 1,
  "channels": [
    {"id":"a","type":"webhook","settings":{"url":"%s/a","body":{"format":"text","text":"A"}}},
    {"id":"b","type":"webhook","settings":{"url":"%s/b","body":{"format":"text","text":"B"}}},
    {"id":"c","type":"webhook","settings":{"url":"%s/c","body":{"format":"text","text":"C"}}}
  ]
}`, srv.URL, srv.URL, srv.URL)
	t.Setenv("TASKPUSH_CONFIG_JSON", cfg)

	code, stdout, stderr := runCLI(t, "send", "--parallel", "3", "--title", "T")
	if code != 0 {
		t.Fatalf("parallel send exit = %d, want 0 (stderr %q)", code, stderr)
	}
	mu.Lock()
	gotPeak := peak
	mu.Unlock()
	if gotPeak < 2 {
		t.Fatalf("peak concurrency = %d, want at least 2", gotPeak)
	}
	positions := []int{
		strings.Index(stdout, "[OK] a (webhook)"),
		strings.Index(stdout, "[OK] b (webhook)"),
		strings.Index(stdout, "[OK] c (webhook)"),
	}
	if positions[0] < 0 || positions[1] < 0 || positions[2] < 0 {
		t.Fatalf("results are missing a channel:\n%s", stdout)
	}
	if !(positions[0] < positions[1] && positions[1] < positions[2]) {
		t.Fatalf("results are not in configuration order:\n%s", stdout)
	}
}

// TestScenario16ConfigErrors 覆盖主要配置错误分类都退出 1 并带定位信息
//
// TestScenario16ConfigErrors covers the main configuration error classes all exiting with 1 and
// carrying their location
func TestScenario16ConfigErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{name: "unknown top field", body: `{"version":1,"foo":1,"channels":[]}`, want: "unknown field"},
		{name: "version", body: `{"version":2,"channels":[]}`, want: "unsupported configuration version"},
		{name: "duplicate id", body: `{"version":1,"channels":[{"id":"a","type":"webhook","settings":{}},{"id":"a","type":"webhook","settings":{}}]}`, want: "duplicate channel id"},
		{name: "unknown type", body: `{"version":1,"channels":[{"id":"a","type":"no_such_type","settings":{}}]}`, want: "unknown channel type"},
		{name: "settings not object", body: `{"version":1,"channels":[{"id":"a","type":"webhook","settings":"x"}]}`, want: "must be a JSON object"},
		{name: "bad duration", body: `{"version":1,"timeout":"15","channels":[]}`, want: "invalid duration"},
		{name: "socks5 proxy", body: `{"version":1,"proxy":"socks5://example.com:1080","channels":[]}`, want: "invalid proxy"},
		{name: "bad id", body: `{"version":1,"channels":[{"id":"a b","type":"webhook","settings":{}}]}`, want: "invalid channel id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeFile(t, dir, "bad.json", tc.body)
			code, stdout, stderr := runCLI(t, "check", "--config", path)
			if code != 1 {
				t.Fatalf("exit = %d, want 1 (stdout %q stderr %q)", code, stdout, stderr)
			}
			if combined := stdout + stderr; !strings.Contains(combined, tc.want) {
				t.Fatalf("output misses %q:\n%s", tc.want, combined)
			}
		})
	}
}

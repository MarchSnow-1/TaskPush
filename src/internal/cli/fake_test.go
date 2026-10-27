package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
)

// ---------------------------------------------------------------------------
// 测试桩: 一次性注册的假渠道工厂, 以及 stdin/环境变量/工作目录的注入助手
// 假渠道不依赖任何具体渠道包, 也不依赖 channel/all; 需要发网络时只访问 httptest 起的本地地址
//
// The test doubles: a fake channel factory registered once, plus helpers that inject stdin, the
// environment and the working directory
// The fake channel depends on no concrete channel package and not on channel/all, and when it needs
// the network it reaches a local httptest address only
// ---------------------------------------------------------------------------

const (
	// fakeType 是基础假渠道的类型名
	//
	// fakeType is the type name of the basic fake channel
	fakeType = "example"
	// fakeHTTPType 是会真正发 HTTP 请求的假渠道类型名
	//
	// fakeHTTPType is the type name of the fake channel that performs a real HTTP request
	fakeHTTPType = "example-http"
)

// fakeSettings 控制假渠道的行为
//
// fakeSettings drives the behaviour of the fake channel
type fakeSettings struct {
	Fail        bool   `json:"fail"`
	Warn        bool   `json:"warn"`
	PrepareFail bool   `json:"prepareFail"`
	URL         string `json:"url"`
	PreviewID   string `json:"previewID"`
}

// fakeFactory 实现 channel.Factory
//
// fakeFactory implements channel.Factory
type fakeFactory struct {
	typ string
}

// Type 返回注册的类型名
//
// Type returns the registered type name
func (f fakeFactory) Type() string { return f.typ }

// Describe 返回该类型描述使用的消息键
// 键指向已有的 webhook 描述, 这样两种语言都有译文, 表格的 CJK 对齐也能被 golden 观察到
//
// Describe returns the message key describing the type
// The key points at the existing webhook description, so both languages carry copy and the CJK
// alignment of the table becomes observable in a golden
func (f fakeFactory) Describe() string { return "channel.webhook.desc" }

// Fields 返回两个字段, 供 types 命令的字段表格使用
// 描述键同样复用已有的 webhook 字段描述
//
// Fields returns two fields for the field table of the types command
// Their description keys reuse the existing webhook field descriptions as well
func (f fakeFactory) Fields() []channel.Field {
	return []channel.Field{
		{
			Name:           "url",
			Type:           "string",
			Required:       false,
			Default:        "",
			DescriptionKey: "channel.webhook.field.url.desc",
		},
		{
			Name:           "fail",
			Type:           "bool",
			Required:       true,
			Default:        "false",
			DescriptionKey: "channel.webhook.field.body.desc",
		},
	}
}

// Create 严格解码 settings 并构造假渠道
//
// Create decodes settings strictly and builds the fake channel
func (f fakeFactory) Create(id string, raw json.RawMessage) (channel.Channel, error) {
	settings := fakeSettings{}
	if len(raw) > 0 {
		if err := channel.DecodeStrict(raw, &settings); err != nil {
			return nil, err
		}
	}
	return &fakeChannel{id: id, typ: f.typ, settings: settings}, nil
}

// fakeChannel 实现 channel.Channel
//
// fakeChannel implements channel.Channel
type fakeChannel struct {
	id       string
	typ      string
	settings fakeSettings
}

func (c *fakeChannel) ID() string   { return c.id }
func (c *fakeChannel) Type() string { return c.typ }

// Prepare 返回一个可发送的实例, 按 settings 附上警告或直接报错
//
// Prepare returns a sendable instance and attaches a warning according to its settings, or fails
// outright
func (c *fakeChannel) Prepare(_ context.Context, msg channel.Message) (channel.Prepared, []apperr.Problem, error) {
	if c.settings.PrepareFail {
		return nil, nil, apperr.Permanent(&apperr.Problem{ID: "channel.webhook.error.missing_url"})
	}
	var warns []apperr.Problem
	if c.settings.Warn {
		warns = append(warns, apperr.Problem{ID: "channel.webhook.error.body_empty"})
	}
	return &fakePrepared{channel: c, msg: msg}, warns, nil
}

// fakePrepared 实现 channel.Prepared
//
// fakePrepared implements channel.Prepared
type fakePrepared struct {
	channel *fakeChannel
	msg     channel.Message
}

// Preview 返回脱敏预览
//
// Preview returns the redacted preview
func (p *fakePrepared) Preview() apperr.Problem {
	id := p.channel.settings.PreviewID
	if id == "" {
		id = "channel.webhook.preview.http"
	}
	return apperr.Problem{
		ID: id,
		Data: map[string]any{
			"Method":  "POST",
			"URL":     "https://example.com/redacted",
			"Format":  "text",
			"Bytes":   len(p.msg.Body),
			"Targets": 1,
		},
	}
}

// Send 按 settings 返回成功或失败, URL 非空时向该地址发一次 POST
//
// Send returns success or failure according to its settings, and a non-empty URL posts to that
// address once
func (p *fakePrepared) Send(ctx context.Context, client *http.Client) error {
	if p.channel.settings.URL != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.channel.settings.URL, strings.NewReader(p.msg.Body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "text/plain")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return apperr.Permanent(&apperr.Problem{
				ID:   "common.error.http_status",
				Data: map[string]any{"Status": resp.StatusCode, "Body": ""},
			})
		}
		return nil
	}
	if p.channel.settings.Fail {
		return apperr.Permanent(&apperr.Problem{ID: "cli.test.fail"})
	}
	return nil
}

// initialised 保证假工厂只注册一次, channel.Register 对重复 type 直接 panic
//
// initialised keeps the fake factories registered once, since channel.Register panics on a
// duplicate type
var initialised sync.Once

// registerFakes 注册测试用工厂
// 只注册工厂, 不改写工厂查找接线点: 默认实现直接读 channel 注册表, 因此测试覆盖的是生产路径
//
// registerFakes registers the test factories
// It only registers them and never rewrites the factory lookup, because the default reads the
// channel registry directly, which keeps the tests on the production path
func registerFakes() {
	initialised.Do(func() {
		channel.Register(fakeFactory{typ: fakeType})
		channel.Register(fakeFactory{typ: fakeHTTPType})
	})
}

// TestMain 注册假工厂, 让所有测试都能通过 channel 注册表解析这些类型
//
// TestMain registers the fake factories, so every test can resolve those types through the channel
// registry
func TestMain(m *testing.M) {
	registerFakes()
	os.Exit(m.Run())
}

// runResult 是一次 Run 调用的可断言结果
//
// runResult holds the assertable outcome of one Run call
type runResult struct {
	code   int
	stdout string
	stderr string
}

// runCLI 在当前注入环境下执行一次 Run
//
// runCLI performs one Run call in the current injected environment
func runCLI(t *testing.T, args ...string) runResult {
	t.Helper()
	var stdout, stderr strings.Builder
	code := Run(context.Background(), args, "test-version", &stdout, &stderr)
	return runResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// writeFile 写一个测试文件并返回其路径, 内容由用例自行准备
//
// writeFile writes a test file and returns its path, with the content supplied by the case
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := dir + string(os.PathSeparator) + name
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// withWorkdir 把 cli 的工作目录切到 dir, 测试结束恢复
//
// withWorkdir switches the working directory of the cli package to dir and restores it afterwards
func withWorkdir(t *testing.T, dir string) {
	t.Helper()
	previous := workingDir
	workingDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { workingDir = previous })
}

// withStdin 替换 cli 读取的标准输入, 测试结束恢复
//
// withStdin replaces the standard input the cli package reads and restores it afterwards
func withStdin(t *testing.T, content string) {
	t.Helper()
	previous := stdinReader
	stdinReader = strings.NewReader(content)
	t.Cleanup(func() { stdinReader = previous })
}

// withEnv 替换 cli 使用的环境变量查询, 测试结束恢复
//
// withEnv replaces the environment lookup the cli package uses and restores it afterwards
func withEnv(t *testing.T, values map[string]string) {
	t.Helper()
	previous := lookupEnv
	lookupEnv = func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
	t.Cleanup(func() { lookupEnv = previous })
}

// canceledContext 返回一个已经取消的上下文
//
// canceledContext returns an already cancelled context
func canceledContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// exampleConfig 构造一份使用假渠道的最小配置
//
// exampleConfig builds a minimal configuration using the fake channels
func exampleConfig(channels ...string) string {
	if len(channels) == 0 {
		channels = []string{`{"id":"ops","type":"` + fakeType + `","settings":{}}`}
	}
	return fmt.Sprintf(`{"version":1,"channels":[%s]}`, strings.Join(channels, ","))
}

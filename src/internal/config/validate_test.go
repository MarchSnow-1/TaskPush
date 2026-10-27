package config

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/i18n"
)

// ---------------------------------------------------------------------------
// 配置测试的公共脚手架: fake 工厂只用于 type 注册校验, 不实现任何渠道行为
// 注册表没有重置接口, 因此 TestMain 只注册一次, 多个测试共享这两个类型
//
// Shared scaffolding for the configuration tests: the fake factories serve the type
// registration check only and implement no channel behaviour
// The registry has no reset API, so TestMain registers once and every test shares the two types
// ---------------------------------------------------------------------------

const (
	// fakeTypeAlpha 是第一个测试渠道类型
	// fakeTypeAlpha is the first test channel type
	fakeTypeAlpha = "example_alpha"
	// fakeTypeBeta 是第二个测试渠道类型, 用于多类型场景
	// fakeTypeBeta is the second test channel type, used for multi-type scenarios
	fakeTypeBeta = "example_beta"
)

// TestMain 注册 fake 工厂后运行测试
// Register 对重复 type 会 panic, 因此注册点集中在进程启动阶段
//
// TestMain registers the fake factories before running the tests
// Register panics on a duplicate type, so the registration is concentrated at process start
func TestMain(m *testing.M) {
	channel.Register(fakeFactory{typ: fakeTypeAlpha})
	channel.Register(fakeFactory{typ: fakeTypeBeta})
	os.Exit(m.Run())
}

// fakeFactory 是仅满足注册契约的工厂
// Create 不参与本包测试, 因为 Load 与 BuildInline 都不经过工厂
//
// fakeFactory is a factory satisfying the registration contract only
// Create takes no part in this package's tests, since neither Load nor BuildInline goes through
// a factory
type fakeFactory struct {
	typ string
}

// Type 返回注册类型
//
// Type returns the registered type
func (f fakeFactory) Type() string { return f.typ }

// Describe 返回一个稳定的描述键
//
// Describe returns a stable description key
func (f fakeFactory) Describe() string { return "channel.example.desc" }

// Fields 返回空字段表
//
// Fields returns an empty field table
func (f fakeFactory) Fields() []channel.Field { return nil }

// Create 返回未实现的错误, 本包测试不会调用它
//
// Create returns a not-implemented error, and this package's tests do not call it
func (f fakeFactory) Create(string, json.RawMessage) (channel.Channel, error) {
	return nil, errors.New("not implemented")
}

// testSource 把一段 JSON 包成文件来源
//
// testSource wraps a JSON text as a file source
func testSource(data string) Source {
	return Source{Kind: SourceFile, Path: "/example/taskpush.json", Data: []byte(data)}
}

// loadJSON 加载一段 JSON 并断言成功
//
// loadJSON loads a JSON text and asserts success
func loadJSON(t *testing.T, data string) *Root {
	t.Helper()
	root, err := Load(testSource(data))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return root
}

// loadProblem 加载一段 JSON 并断言返回指定消息键的 Problem
//
// loadProblem loads a JSON text and asserts a Problem with the given message key
func loadProblem(t *testing.T, data, key string) *apperr.Problem {
	t.Helper()
	root, err := Load(testSource(data))
	if err == nil {
		t.Fatalf("Load succeeded with %+v, want %s", root, key)
	}
	var problem *apperr.Problem
	if !errors.As(err, &problem) {
		t.Fatalf("Load error = %T, want *apperr.Problem", err)
	}
	if problem.ID != key {
		t.Fatalf("Problem.ID = %q, want %q (data %v)", problem.ID, key, problem.Data)
	}
	return problem
}

// lookupFrom 构造一个只认识给定键值对的环境查询
//
// lookupFrom builds an environment lookup that knows only the given pairs
func lookupFrom(pairs map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := pairs[name]
		return value, ok
	}
}

// settingsOf 把渠道的 settings 解码成通用 map, 供断言使用
//
// settingsOf decodes a channel's settings into a generic map for assertions
func settingsOf(t *testing.T, c Channel) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(c.Settings, &out); err != nil {
		t.Fatalf("settings %s: %v", c.Settings, err)
	}
	return out
}

// decodeBody 把 settings 的 body 字段解码成通用 map
//
// decodeBody decodes the body field of settings into a generic map
func decodeBody(t *testing.T, settings map[string]any) map[string]any {
	t.Helper()
	body, ok := settings["body"].(map[string]any)
	if !ok {
		t.Fatalf("body = %#v, want an object", settings["body"])
	}
	return body
}

// configProblemKeys 列出本包会产生的全部消息键
// 枚举而不是从代码里反射提取, 因为键是稳定契约, 新增键时必须同步更新这张表与两个 catalog
//
// configProblemKeys lists every message key this package produces
// The list is written out rather than reflected from the code, since the keys are a stable
// contract and a new key has to update this table along with both catalogs
var configProblemKeys = []string{
	"config.error.source_missing",
	"config.error.read",
	"config.error.empty",
	"config.error.json",
	"config.error.unknown_field",
	"config.error.type_mismatch",
	"config.error.version",
	"config.error.id_invalid",
	"config.error.id_duplicate",
	"config.error.type_unknown",
	"config.error.settings_not_object",
	"config.error.settings_missing",
	"config.error.env_unset",
	"config.error.env_name",
	"config.error.env_unclosed",
	"config.error.env_nested",
	"config.error.duration",
	"config.error.int",
	"config.error.bool",
	"config.error.proxy",
	"config.error.timeout",
	"config.error.retries",
	"config.error.retry_delay",
	"config.error.empty_target",
	"config.error.unknown_target",
	"config.error.disabled_target",
	"config.error.no_channels",
	"config.inline.mode",
	"config.inline.type",
	"config.inline.webhook_url",
	"config.inline.payload",
	"config.inline.set",
	"config.inline.set_duplicate",
	"config.inline.set_conflict",
	"config.field.version.desc",
	"config.field.timeout.desc",
	"config.field.retries.desc",
	"config.field.retryDelay.desc",
	"config.field.proxy.desc",
	"config.field.channels.desc",
	"config.field.id.desc",
	"config.field.type.desc",
	"config.field.enabled.desc",
	"config.field.settings.desc",
}

// TestProblemKeys_HaveCopy 覆盖每个 config 键在 en 与 zh-CN 都能渲染出文案
// 渲染失败时 Translator.T 返回键名本身, 因此比较返回值与键名即可发现缺失
//
// TestProblemKeys_HaveCopy covers every config key rendering copy in both en and zh-CN
// A failed render makes Translator.T return the key itself, so comparing the result with the
// key name is enough to spot a missing entry
func TestProblemKeys_HaveCopy(t *testing.T) {
	data := map[string]any{
		"Hint":       "example",
		"Source":     "stdin",
		"Detail":     "example detail",
		"Path":       "channels[1].settings",
		"Field":      "example",
		"Suggestion": "example",
		"Expected":   "string",
		"Version":    2,
		"Supported":  1,
		"ID":         "example",
		"Type":       "example",
		"Available":  "example",
		"Variable":   "EXAMPLE",
		"Name":       "EXAMPLE",
		"Value":      "example",
		"Key":        "example",
		"Reason":     "example",
	}
	for _, lang := range []string{"en", "zh-CN"} {
		translator, err := i18n.New(lang)
		if err != nil {
			t.Fatalf("i18n.New(%s): %v", lang, err)
		}
		for _, key := range configProblemKeys {
			if got := translator.T(key, data); got == key {
				t.Errorf("%s is missing from the %s catalog", key, lang)
			}
		}
	}
}

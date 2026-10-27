package common

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	texttemplate "text/template"
)

// ---------------------------------------------------------------------------
// 错误脱敏测试: 一条错误里可能有多个地址, 每个都要被打码, 同时 Unwrap 必须保留原始错误
//
// Redaction tests: one error may quote several addresses, each of which has to be masked while
// Unwrap still returns the original error
// ---------------------------------------------------------------------------

// errSentinel 用于断言 Unwrap 链没有被脱敏包装打断
//
// errSentinel asserts that the Unwrap chain survives the redaction wrapper
var errSentinel = errors.New("dial failed")

func TestSanitizeErrorMasksEveryURL(t *testing.T) {
	in := fmt.Errorf("post to %s failed after retry via %s: %w",
		"https://example.com/send?token=example-token",
		"http://127.0.0.1:8080/hook?a=1",
		errSentinel)

	got := SanitizeError(in)
	if got == nil {
		t.Fatal("SanitizeError(nil-worthwhile input) = nil")
	}
	want := "post to <redacted-url> failed after retry via <redacted-url>: dial failed"
	if got.Error() != want {
		t.Errorf("SanitizeError text = %q, want %q", got.Error(), want)
	}
	// 原始错误链仍可穿透, 调度层与调用方的 errors.Is / errors.As 不受影响
	// The original chain still reaches through, leaving errors.Is and errors.As unaffected
	if !errors.Is(got, errSentinel) {
		t.Errorf("errors.Is(sanitized, sentinel) = false, want true")
	}
	if errors.Unwrap(got) != in {
		t.Errorf("Unwrap = %v, want the original error", errors.Unwrap(got))
	}
}

func TestSanitizeErrorNilStaysNil(t *testing.T) {
	if got := SanitizeError(nil); got != nil {
		t.Errorf("SanitizeError(nil) = %v, want nil", got)
	}
}

func TestSanitizeErrorKeepsTextWithoutURL(t *testing.T) {
	in := errors.New("context deadline exceeded")
	if got := SanitizeError(in); got.Error() != "context deadline exceeded" {
		t.Errorf("SanitizeError text = %q, want it unchanged", got.Error())
	}
}

func TestSanitizeString(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"single", "GET https://example.com/push?token=x", "GET <redacted-url>"},
		{"inside quotes", `body "https://example.com/p?k=1" end`, `body "<redacted-url>" end`},
		{"no url", "plain text stays", "plain text stays"},
		{"scheme with digits plus", "see s3+-.://example.com/x", "see <redacted-url>"},
		{"bare host not masked", "host example.com:8080 down", "host example.com:8080 down"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SanitizeString(c.in); got != c.want {
				t.Errorf("SanitizeString(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestSanitizeStringTrailingPunctuation 覆盖地址后紧跟标点: 标点属于句子, 不随地址一起被打码
//
// TestSanitizeStringTrailingPunctuation covers punctuation right after an address: it belongs to
// the sentence and stays out of the masked span
func TestSanitizeStringTrailingPunctuation(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"colon then space", "GET https://example.com/x: dial failed", "GET <redacted-url>: dial failed"},
		{"comma", "see https://example.com/x, then stop", "see <redacted-url>, then stop"},
		{"full stop", "see https://example.com/x.", "see <redacted-url>."},
		{"semicolon", "see https://example.com/x;", "see <redacted-url>;"},
		{"closing paren unmatched", "(see https://example.com/x)", "(see <redacted-url>)"},
		{"matching paren kept in url", "see https://example.com/a(b)", "see <redacted-url>"},
		{"query ampersand preserved", "see https://example.com/x?a=1&b=2,", "see <redacted-url>,"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SanitizeString(c.in); got != c.want {
				t.Errorf("SanitizeString(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestSanitizeStringIdempotent 覆盖重复脱敏: 已打码的文本再过一次不变形
//
// TestSanitizeStringIdempotent covers running redaction twice: masked text keeps its shape
func TestSanitizeStringIdempotent(t *testing.T) {
	once := SanitizeString("see https://example.com/a?token=1, please")
	if twice := SanitizeString(once); twice != once {
		t.Errorf("second pass = %q, want %q", twice, once)
	}
}

// commonCatalogSampleData 是每个键的样例 Data, 键名与占位符一致, 取值用 example 占位
// 这些形状与 url.go / json.go / http.go 实际填进 Data 的类型对应, 因此这里的渲染通过也说明
// 下游 Problem 的 Data 能套上文案
//
// commonCatalogSampleData holds the sample Data for each key, with names matching the placeholders
// and values written with example placeholders
// The shapes mirror the types url.go, json.go and http.go actually put into Data, so a successful
// render here also shows a downstream Problem's Data fits its copy
var commonCatalogSampleData = map[string]map[string]any{
	"common.error.empty_url":      {"Field": "url"},
	"common.error.invalid_url":    {"Field": "url"},
	"common.error.missing_host":   {"Field": "url"},
	"common.error.http_status":    {"Status": 500, "Body": "example body"},
	"common.error.body_too_large": {"Limit": 1 << 20},
	"common.error.bad_json":       {"Field": "body", "Detail": "example detail"},
	"common.error.business_code":  {"Code": 40001, "Message": "example message"},
	"common.error.bad_response":   {"Field": "code", "Detail": "example detail"},
	"common.error.request_failed": {"Detail": "example detail"},
	"common.warn.body_too_large":  {"Limit": 1 << 20},
}

// TestCommonCatalogRendersWithSampleData 用样例 Data 渲染每个键, 确认占位符能被填上
// 缺少任一数据时模板执行失败, 返回键名, 因此比对返回值与键名即可发现缺口
//
// TestCommonCatalogRendersWithSampleData renders each key with the sample Data to confirm the
// placeholders can be filled in
// A missing piece of data makes the execution fail and return the key name, so comparing the
// result against the key reveals the gap
func TestCommonCatalogRendersWithSampleData(t *testing.T) {
	for _, lang := range []string{"en", "zh-CN"} {
		path := filepath.Join("..", "..", "i18n", "catalog", lang, "common.json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var messages map[string]string
		if err := json.Unmarshal(data, &messages); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, key := range commonCatalogKeys {
			text, ok := messages[key]
			if !ok {
				t.Errorf("%s is missing %q", lang, key)
				continue
			}
			tpl, err := texttemplate.New(key).Option("missingkey=error").Parse(text)
			if err != nil {
				t.Errorf("%s %s does not compile: %v", lang, key, err)
				continue
			}
			var sb strings.Builder
			if err := tpl.Execute(&sb, commonCatalogSampleData[key]); err != nil {
				t.Errorf("%s %s failed to render with %v: %v",
					lang, key, commonCatalogSampleData[key], err)
				continue
			}
			if sb.String() == key || sb.String() == "" {
				t.Errorf("%s %s rendered as %q, want the copy with placeholders filled",
					lang, key, sb.String())
			}
		}
	}
}

// commonCatalogKeys 是 common.json 必须覆盖的键, 新增公共层错误键时同步加进这张表
//
// commonCatalogKeys holds the keys common.json has to cover, and a new shared-layer error key goes
// into this table as well
var commonCatalogKeys = []string{
	"common.error.empty_url",
	"common.error.invalid_url",
	"common.error.missing_host",
	"common.error.http_status",
	"common.error.body_too_large",
	"common.error.bad_json",
	"common.error.business_code",
	"common.error.bad_response",
	"common.error.request_failed",
	"common.warn.body_too_large",
}

// commonCatalogPlaceholders 是每个键允许出现的占位符集合, 与 message-keys.md 第 6 节一一对应
//
// commonCatalogPlaceholders holds the placeholder set each key may use, matching section 6 of
// message-keys.md one to one
var commonCatalogPlaceholders = map[string][]string{
	"common.error.empty_url":      {"Field"},
	"common.error.invalid_url":    {"Field"},
	"common.error.missing_host":   {"Field"},
	"common.error.http_status":    {"Status", "Body"},
	"common.error.body_too_large": {"Limit"},
	"common.error.bad_json":       {"Field", "Detail"},
	"common.error.business_code":  {"Code", "Message"},
	"common.error.bad_response":   {"Field", "Detail"},
	"common.error.request_failed": {"Detail"},
	"common.warn.body_too_large":  {"Limit"},
}

// placeholderPatternInCatalog 匹配消息文案里的 {{.Name}} 占位符
//
// placeholderPatternInCatalog matches the {{.Name}} placeholder inside message copy
var placeholderPatternInCatalog = regexp.MustCompile(`\{\{\s*\.([A-Za-z0-9_]+)\s*\}\}`)

// TestCommonCatalogKeysAndPlaceholders 校验 en/zh-CN 两份 common.json: 键集合一致, 占位符集合与
// 契约一致, 且没有结尾句号
// 直接读文件而不经由 i18n 包, 因为 common 的依赖里不允许出现 i18n
//
// TestCommonCatalogKeysAndPlaceholders checks both common.json files: the key sets match, the
// placeholder sets match the contract, and no copy ends in a full stop
// The files are read directly rather than through the i18n package, which common is not allowed
// to depend on
func TestCommonCatalogKeysAndPlaceholders(t *testing.T) {
	langs := []string{"en", "zh-CN"}
	loaded := map[string]map[string]string{}
	for _, lang := range langs {
		path := filepath.Join("..", "..", "i18n", "catalog", lang, "common.json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var messages map[string]string
		if err := json.Unmarshal(data, &messages); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		loaded[lang] = messages
	}

	en := loaded["en"]
	zh := loaded["zh-CN"]
	if len(en) != len(commonCatalogKeys) {
		t.Errorf("en common.json holds %d keys, want %d", len(en), len(commonCatalogKeys))
	}
	for _, key := range commonCatalogKeys {
		enText, ok := en[key]
		if !ok {
			t.Errorf("en common.json is missing %q", key)
			continue
		}
		zhText, ok := zh[key]
		if !ok {
			t.Errorf("zh-CN common.json is missing %q", key)
			continue
		}
		want := append([]string(nil), commonCatalogPlaceholders[key]...)
		sort.Strings(want)
		for _, pair := range []struct {
			lang string
			text string
		}{{"en", enText}, {"zh-CN", zhText}} {
			got := placeholderPatternInCatalog.FindAllStringSubmatch(pair.text, -1)
			var names []string
			for _, m := range got {
				names = append(names, m[1])
			}
			sort.Strings(names)
			if strings.Join(names, ",") != strings.Join(want, ",") {
				t.Errorf("%s %s placeholders = %v, want %v", pair.lang, key, names, want)
			}
			if strings.HasSuffix(strings.TrimSpace(pair.text), ".") {
				t.Errorf("%s %s ends with a full stop: %q", pair.lang, key, pair.text)
			}
		}
	}
}

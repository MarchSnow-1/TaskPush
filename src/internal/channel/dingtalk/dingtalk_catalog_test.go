package dingtalk

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
)

// ---------------------------------------------------------------------------
// catalog 一致性测试: 代码里的每个 dingtalk 消息键在 en 与 zh-CN 都存在, 两侧占位符一致,
// 且每种 Problem 的 Data 键能填满文案占位符
// 这里只读取渠道自己的两份 catalog 文件, 不导入 i18n, 因为渠道包按契约不认识翻译器
//
// Catalog consistency tests: every DingTalk message key the code uses exists in en and zh-CN, the
// placeholder sets match, and the Data of each Problem covers the placeholders of its message
// Only the two catalog files of this channel are read here, without importing i18n, since a channel
// package does not know the translator under the contract
// ---------------------------------------------------------------------------

// catalogFile 是 dingtalk 的 catalog 文件名
//
// catalogFile is the name of the DingTalk catalog file
const catalogFile = "channel_dingtalk.json"

// placeholderRegexp 匹配文案里的 {{.Name}} 占位符
//
// placeholderRegexp matches a {{.Name}} placeholder inside message text
var placeholderRegexp = regexp.MustCompile(`\{\{\s*\.([A-Za-z0-9_]+)\s*\}\}`)

// loadCatalog 读取一种语言的 dingtalk catalog
//
// loadCatalog reads the DingTalk catalog of one language
func loadCatalog(t *testing.T, lang string) map[string]string {
	t.Helper()
	path := filepath.FromSlash(fmt.Sprintf("../../i18n/catalog/%s/%s", lang, catalogFile))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var messages map[string]string
	if err := json.Unmarshal(data, &messages); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return messages
}

// placeholderNames 提取文案里的占位符名, 排序去重
//
// placeholderNames extracts the placeholder names of a message, sorted and deduplicated
func placeholderNames(text string) []string {
	matches := placeholderRegexp.FindAllStringSubmatch(text, -1)
	seen := map[string]bool{}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, m[1])
	}
	sort.Strings(out)
	return out
}

// sortedKeys 返回消息表的排序键
//
// sortedKeys returns the sorted keys of a message table
func sortedKeys(messages map[string]string) []string {
	out := make([]string, 0, len(messages))
	for key := range messages {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// codeKeys 返回代码里声明的全部 dingtalk 消息键常量
//
// codeKeys returns every DingTalk message key constant the code declares
func codeKeys() []string {
	return []string{
		keyDesc,
		keyDefaultMarkdownText,
		keyFieldURL, keyFieldSecret, keyFieldMsgType, keyFieldAtAll,
		keyErrorMissingURL, keyErrorMissingToken, keyErrorInvalidURL, keyErrorInvalidMsgType,
		keyErrorEmptyContent, keyErrorEmptyMarkdown, keyErrorAtAllMarkdown,
		keyErrorAPICode, keyErrorBadResponse,
		keyWarnTruncated,
		keyPreviewMessage,
	}
}

// TestCatalog_KeysExistInBothLanguages 覆盖每个键在两种语言都存在且非空
//
// TestCatalog_KeysExistInBothLanguages covers every key being present and non-empty in both
// languages
func TestCatalog_KeysExistInBothLanguages(t *testing.T) {
	catalogs := map[string]map[string]string{}
	for _, lang := range []string{"en", "zh-CN"} {
		catalogs[lang] = loadCatalog(t, lang)
	}
	for _, key := range codeKeys() {
		for _, lang := range []string{"en", "zh-CN"} {
			if strings.TrimSpace(catalogs[lang][key]) == "" {
				t.Errorf("%s: %s is missing or empty in %s", key, lang, catalogFile)
			}
		}
	}
}

// TestCatalog_KeySetsMatch 覆盖 en 与 zh-CN 的键集合一致, 且没有代码未声明的孤儿键
//
// TestCatalog_KeySetsMatch covers the key sets matching between en and zh-CN, with no key this
// package does not declare
func TestCatalog_KeySetsMatch(t *testing.T) {
	en := loadCatalog(t, "en")
	zh := loadCatalog(t, "zh-CN")

	want := codeKeys()
	sort.Strings(want)
	if got := sortedKeys(en); !reflect.DeepEqual(got, want) {
		t.Errorf("en keys = %v, want %v", got, want)
	}
	if got := sortedKeys(zh); !reflect.DeepEqual(got, want) {
		t.Errorf("zh-CN keys = %v, want %v", got, want)
	}
}

// TestCatalog_PlaceholderParity 覆盖两种语言的占位符集合一致
//
// TestCatalog_PlaceholderParity covers the placeholder sets matching between the two languages
func TestCatalog_PlaceholderParity(t *testing.T) {
	en := loadCatalog(t, "en")
	zh := loadCatalog(t, "zh-CN")
	for _, key := range sortedKeys(en) {
		enText, ok := en[key]
		if !ok {
			t.Fatalf("key %q missing from en", key)
		}
		zhText, ok := zh[key]
		if !ok {
			t.Fatalf("key %q: zh-CN is missing the key", key)
		}
		if got, want := placeholderNames(zhText), placeholderNames(enText); !reflect.DeepEqual(got, want) {
			t.Errorf("key %q: zh-CN placeholders %v, en placeholders %v", key, got, want)
		}
		if !strings.HasPrefix(key, "channel.dingtalk.") {
			t.Errorf("key %q does not carry the dingtalk prefix", key)
		}
	}
}

// TestCatalog_DataCoversPlaceholders 覆盖每种 Problem 实际带上的 Data 键与文案占位符一致
//
// TestCatalog_DataCoversPlaceholders covers the Data keys each Problem actually carries matching the
// placeholders of its message
func TestCatalog_DataCoversPlaceholders(t *testing.T) {
	en := loadCatalog(t, "en")

	// 代码里带占位符的键与它们的 Data 键名
	// The keys carrying placeholders in the code, together with their Data key names
	withData := map[string][]string{
		keyErrorInvalidURL:     {"URL"},
		keyErrorInvalidMsgType: {"Type"},
		keyErrorAPICode:        {"Code", "Message"},
		keyErrorBadResponse:    {"Detail"},
		keyWarnTruncated:       {"Limit"},
		keyPreviewMessage:      {"MsgType", "Targets"},
	}
	for key, dataKeys := range withData {
		want := placeholderNames(en[key])
		sort.Strings(dataKeys)
		if !reflect.DeepEqual(want, dataKeys) {
			t.Errorf("%s: message placeholders %v, code Data keys %v", key, want, dataKeys)
		}
	}

	// 无占位符的键确认 Data 为空, 避免多余字段进文案
	// A key without placeholders is confirmed to carry no Data, so no spare field reaches the copy
	for _, key := range []string{
		keyErrorMissingURL, keyErrorMissingToken,
		keyErrorEmptyContent, keyErrorEmptyMarkdown, keyErrorAtAllMarkdown,
	} {
		if got := placeholderNames(en[key]); len(got) != 0 {
			t.Errorf("%s: unexpected placeholders %v", key, got)
		}
	}

	// 纯描述键不带占位符
	// A pure description key carries no placeholder
	for _, key := range []string{keyDesc, keyDefaultMarkdownText, keyFieldURL, keyFieldSecret, keyFieldMsgType, keyFieldAtAll} {
		if got := placeholderNames(en[key]); len(got) != 0 {
			t.Errorf("%s: unexpected placeholders %v", key, got)
		}
	}
}

// TestCatalog_ProblemsRenderable 覆盖错误与预览的 ID 已登记且 Data 能填满占位符
//
// TestCatalog_ProblemsRenderable covers each error and the preview carrying a registered ID whose
// Data fills the placeholders
func TestCatalog_ProblemsRenderable(t *testing.T) {
	en := loadCatalog(t, "en")

	problems := []*apperr.Problem{
		{ID: keyErrorMissingURL},
		{ID: keyErrorMissingToken},
		{ID: keyErrorInvalidURL, Data: map[string]any{"URL": "https://example.com/robot/send?access_token=***"}},
		{ID: keyErrorInvalidMsgType, Data: map[string]any{"Type": "image"}},
		{ID: keyErrorEmptyContent},
		{ID: keyErrorEmptyMarkdown},
		{ID: keyErrorAtAllMarkdown},
		{ID: keyErrorAPICode, Data: map[string]any{"Code": 310000, "Message": "example"}},
		{ID: keyErrorBadResponse, Data: map[string]any{"Detail": "example"}},
		{ID: keyWarnTruncated, Data: map[string]any{"Limit": contentLimit}},
		{ID: keyPreviewMessage, Data: map[string]any{"MsgType": msgTypeText, "Targets": 1}},
	}
	for _, prob := range problems {
		text, ok := en[prob.ID]
		if !ok {
			t.Errorf("%s is not registered in the en catalog", prob.ID)
			continue
		}
		for _, name := range placeholderNames(text) {
			if _, ok := prob.Data[name]; !ok {
				t.Errorf("%s: Data is missing %q", prob.ID, name)
			}
		}
	}
}

// TestCatalog_DefaultMarkdownTitle 覆盖默认 markdown 标题的三层取值
// 有译文时用译文, 没有译文或没接线时落到语言无关常量, 常量与 zh-CN 文案保持一致
//
// TestCatalog_DefaultMarkdownTitle covers the three layers of the default markdown title
// A wired lookup wins, an absent lookup or a missing copy falls back to the language-neutral
// constant, and that constant matches the zh-CN copy
func TestCatalog_DefaultMarkdownTitle(t *testing.T) {
	zh := loadCatalog(t, "zh-CN")
	if got := zh[keyDefaultMarkdownText]; got != defaultMarkdownTitle {
		t.Errorf("%s in zh-CN = %q, channel constant = %q", keyDefaultMarkdownText, got, defaultMarkdownTitle)
	}
	en := loadCatalog(t, "en")
	if strings.TrimSpace(en[keyDefaultMarkdownText]) == "" {
		t.Errorf("%s is empty in en", keyDefaultMarkdownText)
	}

	if got := markdownTitleFallback(channel.Message{}); got != defaultMarkdownTitle {
		t.Errorf("without a lookup = %q, want the constant %q", got, defaultMarkdownTitle)
	}
	wired := channel.Message{Translate: func(key string) string { return en[key] }}
	if got := markdownTitleFallback(wired); got != en[keyDefaultMarkdownText] {
		t.Errorf("with an en lookup = %q, want %q", got, en[keyDefaultMarkdownText])
	}
	missing := channel.Message{Translate: func(string) string { return "" }}
	if got := markdownTitleFallback(missing); got != defaultMarkdownTitle {
		t.Errorf("with an empty copy = %q, want the constant %q", got, defaultMarkdownTitle)
	}
}

// TestCatalog_TextsHaveNoPrintf 覆盖 catalog 里没有 Go 格式串
//
// TestCatalog_TextsHaveNoPrintf covers the catalog holding no Go format verb
func TestCatalog_TextsHaveNoPrintf(t *testing.T) {
	for _, lang := range []string{"en", "zh-CN"} {
		for key, text := range loadCatalog(t, lang) {
			if strings.Contains(text, "%s") || strings.Contains(text, "%d") {
				t.Errorf("%s/%s: message uses a printf verb: %q", lang, key, text)
			}
		}
	}
}

// TestCatalog_ExamplePlaceholders 覆盖字段描述里的示例值都是通用占位
// 描述文案里的地址示例只能出现 example.com 一类保留域名, 不写真实机器人地址
//
// TestCatalog_ExamplePlaceholders covers the example values in field descriptions being generic
// placeholders
// An address example in the copy stays on a reserved domain such as example.com rather than naming
// a real robot address
func TestCatalog_ExamplePlaceholders(t *testing.T) {
	for _, lang := range []string{"en", "zh-CN"} {
		for key, text := range loadCatalog(t, lang) {
			if !strings.HasPrefix(key, "channel.dingtalk.field.") {
				continue
			}
			if i := strings.Index(text, "http"); i >= 0 {
				tail := text[i:]
				if end := strings.IndexAny(tail, " ,。"); end >= 0 {
					tail = tail[:end]
				}
				if !strings.Contains(tail, "example.com") {
					t.Errorf("%s/%s: address example %q is not a reserved placeholder", lang, key, tail)
				}
			}
		}
	}
}

package gotify

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
)

// ---------------------------------------------------------------------------
// catalog 一致性测试: 代码用到的每个 gotify 消息键都在 en 与 zh-CN 存在, 两侧占位符一致,
// 且 Problem.Data 的键覆盖该键的占位符
// 这里只读取渠道自己的两份 catalog 文件, 不导入 i18n, 因为渠道包不认识翻译器
//
// Catalog consistency tests: every gotify message key the code uses exists in en and zh-CN, the
// placeholder sets match, and Problem.Data covers the placeholders of that key
// Only the two catalog files of this channel are read here, without importing i18n, since a channel
// package does not know the translator
// ---------------------------------------------------------------------------

// catalogLanguages 是参与对比的语言目录
//
// catalogLanguages lists the language directories compared here
var catalogLanguages = []string{"en", "zh-CN"}

// catalogFile 是 gotify 的 catalog 文件名
//
// catalogFile is the name of the gotify catalog file
const catalogFile = "channel_gotify.json"

// placeholderRegexp 匹配文案里的 {{.Name}} 占位符
//
// placeholderRegexp matches a {{.Name}} placeholder inside message text
var placeholderRegexp = regexp.MustCompile(`\{\{\s*\.([A-Za-z0-9_]+)\s*\}\}`)

// loadCatalog 读取一种语言的 gotify catalog
//
// loadCatalog reads the gotify catalog of one language
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

// placeholdersOf 提取文案里的占位符名, 排序去重
//
// placeholdersOf extracts the placeholder names of a message, sorted and deduplicated
func placeholdersOf(text string) []string {
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

// codeKeys 返回代码里声明的全部 gotify 消息键常量
//
// codeKeys returns every gotify message key constant the code declares
func codeKeys() []string {
	return []string{
		keyDesc,
		keyFieldURL, keyFieldToken, keyFieldPriority,
		keyErrorMissingURL, keyErrorMissingToken, keyErrorInvalidURL, keyErrorInvalidToken,
		keyErrorEmptyContent, keyErrorInvalidPrior, keyErrorHTTPStatus,
		keyWarnTruncated, keyPreviewMessage,
	}
}

// TestCatalog_KeysExistInBothLanguages 覆盖每个键在两种语言都存在
//
// TestCatalog_KeysExistInBothLanguages covers every key being present in both languages
func TestCatalog_KeysExistInBothLanguages(t *testing.T) {
	catalogs := map[string]map[string]string{}
	for _, lang := range catalogLanguages {
		catalogs[lang] = loadCatalog(t, lang)
	}
	for _, key := range codeKeys() {
		for _, lang := range catalogLanguages {
			if text := catalogs[lang][key]; text == "" {
				t.Errorf("%s is missing or empty in %s (%s)", key, lang, catalogFile)
			}
		}
	}
}

// TestCatalog_PlaceholderParity 覆盖两种语言的键集合与占位符集合一致
//
// TestCatalog_PlaceholderParity covers the key sets and the placeholder sets matching between the two
// languages
func TestCatalog_PlaceholderParity(t *testing.T) {
	en := loadCatalog(t, "en")
	zh := loadCatalog(t, "zh-CN")

	if len(en) != len(zh) {
		t.Fatalf("key count differs: en %d, zh-CN %d", len(en), len(zh))
	}
	for key, enText := range en {
		if !strings.HasPrefix(key, "channel.gotify.") {
			t.Errorf("key %q does not carry the gotify prefix", key)
		}
		zhText, ok := zh[key]
		if !ok {
			t.Errorf("zh-CN is missing key %q", key)
			continue
		}
		got, want := placeholdersOf(zhText), placeholdersOf(enText)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("key %q: zh-CN placeholders %v, en placeholders %v", key, got, want)
		}
	}
}

// TestCatalog_UnknownKeysRejected 覆盖 catalog 里没有代码未声明的键, 避免留下孤儿文案
//
// TestCatalog_UnknownKeysRejected covers the catalog holding no key the code never declares, which
// keeps orphan copy from accumulating
func TestCatalog_UnknownKeysRejected(t *testing.T) {
	declared := map[string]bool{}
	for _, key := range codeKeys() {
		declared[key] = true
	}
	for _, lang := range catalogLanguages {
		for key := range loadCatalog(t, lang) {
			if !declared[key] {
				t.Errorf("%s: key %q has no constant in the code", lang, key)
			}
		}
	}
}

// TestCatalog_DataCoversPlaceholders 覆盖每种 Problem 的 Data 键能填满文案占位符
//
// TestCatalog_DataCoversPlaceholders covers the Data of each Problem filling the placeholders of its
// message
func TestCatalog_DataCoversPlaceholders(t *testing.T) {
	en := loadCatalog(t, "en")

	// 代码里每种 Problem 实际带上的 Data 键, 与占位符对比即可发现漏填
	//
	// The Data keys each Problem actually carries in the code: comparing them against the placeholders
	// reveals one that was never filled
	samples := map[string][]string{
		keyErrorInvalidURL:   {"URL"},
		keyErrorInvalidPrior: {"Value"},
		keyErrorHTTPStatus:   {"Body", "Status"},
		keyWarnTruncated:     {"Limit"},
		keyPreviewMessage:    {"Bytes", "Host"},
	}
	for key, dataKeys := range samples {
		want := placeholdersOf(en[key])
		if strings.Join(want, ",") != strings.Join(dataKeys, ",") {
			t.Errorf("%s: message placeholders %v, code Data keys %v", key, want, dataKeys)
		}
	}

	// 无占位符的键确认 Data 为空, 避免多余字段进文案
	//
	// A key without placeholders is confirmed to carry no Data, so no spare field reaches the copy
	for _, key := range []string{
		keyErrorMissingURL, keyErrorMissingToken, keyErrorInvalidToken, keyErrorEmptyContent,
	} {
		if got := placeholdersOf(en[key]); len(got) != 0 {
			t.Errorf("%s: unexpected placeholders %v", key, got)
		}
	}
}

// TestProblems_Renderable 覆盖每种错误与预览的 Data 键名与文案占位符一致
//
// TestProblems_Renderable covers the Data key names of each error and of the preview matching the
// placeholders of its message
func TestProblems_Renderable(t *testing.T) {
	en := loadCatalog(t, "en")

	problems := []*apperr.Problem{
		{ID: keyErrorMissingURL},
		{ID: keyErrorMissingToken},
		{ID: keyErrorInvalidToken},
		{ID: keyErrorEmptyContent},
		{ID: keyErrorInvalidURL, Data: map[string]any{"URL": "https://example.com"}},
		{ID: keyErrorInvalidPrior, Data: map[string]any{"Value": "high"}},
		{ID: keyErrorHTTPStatus, Data: map[string]any{"Status": 401, "Body": "example"}},
		{ID: keyWarnTruncated, Data: map[string]any{"Limit": messageLimitRunes}},
		{ID: keyPreviewMessage, Data: map[string]any{"Host": "gotify.example.com", "Bytes": 42}},
	}
	for _, prob := range problems {
		text, ok := en[prob.ID]
		if !ok {
			t.Errorf("%s is not registered in the en catalog", prob.ID)
			continue
		}
		for _, name := range placeholdersOf(text) {
			if _, ok := prob.Data[name]; !ok {
				t.Errorf("%s: Data is missing %q", prob.ID, name)
			}
		}
	}
}

// TestCatalog_TextsHaveNoPrintf 覆盖 catalog 里没有 Go 格式串与行尾句号
//
// TestCatalog_TextsHaveNoPrintf covers the catalog holding no Go format verb and no trailing full
// stop
func TestCatalog_TextsHaveNoPrintf(t *testing.T) {
	for _, lang := range catalogLanguages {
		for key, text := range loadCatalog(t, lang) {
			if strings.Contains(text, "%s") || strings.Contains(text, "%d") {
				t.Errorf("%s/%s: message uses a printf verb: %q", lang, key, text)
			}
			if strings.HasSuffix(strings.TrimSpace(text), ".") {
				t.Errorf("%s/%s: copy ends with a period: %q", lang, key, text)
			}
		}
	}
}

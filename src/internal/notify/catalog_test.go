package notify

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
// catalog 一致性测试: 代码用到的每个 notify 消息键都在 en 与 zh-CN 存在, 两侧占位符一致,
// 且 Problem.Data 能填满文案里的占位符
// 这里只读取 notify 自己的两份 catalog 文件, 不导入 i18n, 因为 notify 不认识翻译器
//
// Catalog consistency tests: every notify message key the code uses exists in en and zh-CN, the
// placeholder sets match, and Problem.Data fills the placeholders of the message
// Only the two notify catalog files are read here, without importing i18n, since notify does not
// know the translator
// ---------------------------------------------------------------------------

// notifyCatalogFile 是 notify 的 catalog 文件名
//
// notifyCatalogFile is the name of the notify catalog file
const notifyCatalogFile = "notify.json"

// notifyCatalogLanguages 是参与对比的语言目录
//
// notifyCatalogLanguages lists the language directories compared here
var notifyCatalogLanguages = []string{"en", "zh-CN"}

// notifyPlaceholderRegexp 匹配文案里的 {{.Name}} 占位符
//
// notifyPlaceholderRegexp matches a {{.Name}} placeholder inside message text
var notifyPlaceholderRegexp = regexp.MustCompile(`\{\{\s*\.([A-Za-z0-9_]+)\s*\}\}`)

// loadNotifyCatalog 读取一种语言的 notify catalog
//
// loadNotifyCatalog reads the notify catalog of one language
func loadNotifyCatalog(t *testing.T, lang string) map[string]string {
	t.Helper()
	// notify 位于 src/internal/notify, 与 src/internal/i18n 同级, 因此向上只走一层
	//
	// notify lives in src/internal/notify, a sibling of src/internal/i18n, so one level up is enough
	path := filepath.FromSlash(fmt.Sprintf("../i18n/catalog/%s/%s", lang, notifyCatalogFile))
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

// notifyPlaceholders 提取文案里的占位符名, 排序去重
//
// notifyPlaceholders extracts the placeholder names of a message, sorted and deduplicated
func notifyPlaceholders(text string) []string {
	matches := notifyPlaceholderRegexp.FindAllStringSubmatch(text, -1)
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

// codeKeys 返回 notify 代码里声明的全部消息键
//
// codeKeys returns every notify message key the code declares
func codeKeys() []string {
	return []string{
		keyErrorNoChannels,
		keyErrorVarFile,
		keyErrorVar,
		keyErrorReserved,
		keyErrorTitle,
		keyErrorBody,
		keyErrorClient,
		keyErrorCanceled,
	}
}

// TestCatalog_KeysExistInBothLanguages 覆盖每个键在两种语言都存在且非空
//
// TestCatalog_KeysExistInBothLanguages covers every key being present and non-empty in both languages
func TestCatalog_KeysExistInBothLanguages(t *testing.T) {
	catalogs := map[string]map[string]string{}
	for _, lang := range notifyCatalogLanguages {
		catalogs[lang] = loadNotifyCatalog(t, lang)
	}
	for _, key := range codeKeys() {
		for _, lang := range notifyCatalogLanguages {
			if text := catalogs[lang][key]; text == "" {
				t.Errorf("%s is missing or empty in %s", key, lang)
			}
		}
	}
}

// TestCatalog_PlaceholderParity 覆盖两种语言的占位符集合一致
//
// TestCatalog_PlaceholderParity covers the placeholder sets matching between the two languages
func TestCatalog_PlaceholderParity(t *testing.T) {
	en := loadNotifyCatalog(t, "en")
	zh := loadNotifyCatalog(t, "zh-CN")
	if len(en) != len(zh) {
		t.Fatalf("key count differs: en %d, zh-CN %d", len(en), len(zh))
	}
	for key, enText := range en {
		zhText, ok := zh[key]
		if !ok {
			t.Errorf("zh-CN is missing key %q", key)
			continue
		}
		if got, want := notifyPlaceholders(zhText), notifyPlaceholders(enText); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("key %q: zh-CN placeholders %v, en placeholders %v", key, got, want)
		}
		if !strings.HasPrefix(key, "notify.") {
			t.Errorf("key %q does not carry the notify prefix", key)
		}
		if strings.HasSuffix(strings.TrimSpace(enText), ".") && !strings.Contains(enText, ". ") {
			t.Errorf("key %q: single-sentence copy ends with a period: %q", key, enText)
		}
		if strings.Contains(enText, "%s") || strings.Contains(enText, "%d") {
			t.Errorf("key %q: message uses a printf verb: %q", key, enText)
		}
	}
}

// TestCatalog_DataCoversPlaceholders 覆盖每种错误文案的 Data 键能填满占位符
//
// TestCatalog_DataCoversPlaceholders covers the Data of each error message filling its placeholders
func TestCatalog_DataCoversPlaceholders(t *testing.T) {
	en := loadNotifyCatalog(t, "en")
	samples := map[string][]string{
		keyErrorVarFile:  {"Path", "Detail"},
		keyErrorVar:      {"Value", "Reason"},
		keyErrorReserved: {"Variable"},
		keyErrorTitle:    {"Detail"},
		keyErrorBody:     {"Detail"},
		keyErrorClient:   {"Detail"},
	}
	for key, dataKeys := range samples {
		// 文案里的占位符按名字排序后比较, 与 Data 键的书写顺序无关
		//
		// The placeholders of a message are compared after sorting by name, independently of the
		// order the Data keys were written in
		want := notifyPlaceholders(en[key])
		sorted := append([]string(nil), dataKeys...)
		sort.Strings(sorted)
		if strings.Join(want, ",") != strings.Join(sorted, ",") {
			t.Errorf("%s: message placeholders %v, code Data keys %v", key, want, dataKeys)
		}
	}
	for _, key := range []string{keyErrorNoChannels, keyErrorCanceled} {
		end := 0
		if strings.Contains(en[key], "{{") {
			end = 1
		}
		if end != 0 {
			t.Errorf("%s: unexpected placeholders %v", key, notifyPlaceholders(en[key]))
		}
	}
}

// TestProblems_Renderable 覆盖代码里构造的每种 Problem 都带已注册的键与非空 Data
//
// TestProblems_Renderable covers every Problem the code builds carrying a registered key and the
// Data its message expects
func TestProblems_Renderable(t *testing.T) {
	en := loadNotifyCatalog(t, "en")
	problems := []*apperr.Problem{
		{ID: keyErrorNoChannels},
		{ID: keyErrorVarFile, Data: map[string]any{"Path": "example.json", "Detail": "example"}},
		{ID: keyErrorVar, Data: map[string]any{"Value": "example", "Reason": "example"}},
		{ID: keyErrorReserved, Data: map[string]any{"Variable": "title"}},
		{ID: keyErrorTitle, Data: map[string]any{"Detail": "example"}},
		{ID: keyErrorBody, Data: map[string]any{"Detail": "example"}},
		{ID: keyErrorClient, Data: map[string]any{"Detail": "example"}},
		{ID: keyErrorCanceled},
	}
	for _, prob := range problems {
		text, ok := en[prob.ID]
		if !ok {
			t.Errorf("%s is not registered in the en catalog", prob.ID)
			continue
		}
		for _, name := range notifyPlaceholders(text) {
			if _, ok := prob.Data[name]; !ok {
				t.Errorf("%s: Data is missing %q", prob.ID, name)
			}
		}
		if prob.ID != keyErrorNoChannels && prob.ID != keyErrorCanceled && prob.Data == nil {
			t.Errorf("%s: Data is nil", prob.ID)
		}
	}
}

// TestCatalog_UnknownKeysRejected 覆盖 catalog 里没有代码未声明的键
//
// TestCatalog_UnknownKeysRejected covers the catalog holding no key the code never declares
func TestCatalog_UnknownKeysRejected(t *testing.T) {
	declared := map[string]bool{}
	for _, key := range codeKeys() {
		declared[key] = true
	}
	for _, lang := range notifyCatalogLanguages {
		for key := range loadNotifyCatalog(t, lang) {
			if !declared[key] {
				t.Errorf("%s: key %q has no constant in the code", lang, key)
			}
		}
	}
}

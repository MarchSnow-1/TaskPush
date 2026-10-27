package i18n

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"text/template"
	"text/template/parse"
)

// sampleCatalog 返回一份最小目录树, 供加载行为测试使用
//
// sampleCatalog returns a minimal catalog tree for the loading behaviour tests
func sampleCatalog() fstest.MapFS {
	return fstest.MapFS{
		"catalog/en/a.json":               &fstest.MapFile{Data: []byte(`{"example.key.one":"first {{.Value}}"}`)},
		"catalog/en/b.json":               &fstest.MapFile{Data: []byte(`{"example.key.two":"second"}`)},
		"catalog/zh-CN/a.json":            &fstest.MapFile{Data: []byte(`{"example.key.one":"第一 {{.Value}}"}`)},
		"catalog/zh-CN/b.json":            &fstest.MapFile{Data: []byte(`{"example.key.two":"第二"}`)},
		"catalog/zh-CN/ignore.txt":        &fstest.MapFile{Data: []byte(`not json`)},
		"catalog/en/sub/not-catalog.json": &fstest.MapFile{Data: []byte(`{"should.not.load":"x"}`)},
	}
}

// TestLoadCatalog 覆盖按语言合并、只读取 catalog/<语言>/<文件>.json 两层结构
//
// TestLoadCatalog covers the per-language merge and the rule that only the two-level path
// catalog/<language>/<file>.json is read
func TestLoadCatalog(t *testing.T) {
	cat, err := loadCatalog(sampleCatalog())
	if err != nil {
		t.Fatalf("loadCatalog: %v", err)
	}
	if got := cat.languages(); len(got) != 2 || got[0] != "en" || got[1] != "zh-CN" {
		t.Fatalf("languages() = %v, want [en zh-CN]", got)
	}
	if _, ok := cat.lookup("en", "example.key.one"); !ok {
		t.Fatal("en lost example.key.one")
	}
	if _, ok := cat.lookup("zh-CN", "example.key.two"); !ok {
		t.Fatal("zh-CN lost example.key.two")
	}
	// 更深层的目录不参与加载, 嵌入模式也只匹配两层路径
	// A deeper directory is not loaded, since the embedding pattern matches two levels only
	if _, ok := cat.lookup("en", "should.not.load"); ok {
		t.Fatal("a nested file was loaded into the en catalog")
	}
}

// TestLoadCatalog_DuplicateKey 覆盖同一语言重复键的启动错误
//
// TestLoadCatalog_DuplicateKey covers the startup error for a duplicate key within one language
func TestLoadCatalog_DuplicateKey(t *testing.T) {
	tree := fstest.MapFS{
		"catalog/en/a.json": &fstest.MapFile{Data: []byte(`{"example.key.one":"first"}`)},
		"catalog/en/b.json": &fstest.MapFile{Data: []byte(`{"example.key.one":"second"}`)},
	}
	_, err := loadCatalog(tree)
	if err == nil {
		t.Fatal("a duplicate key loaded without an error")
	}
	if !strings.Contains(err.Error(), "duplicate message key") || !strings.Contains(err.Error(), "example.key.one") {
		t.Fatalf("error %q should name the duplicate key", err)
	}
}

// TestLoadCatalog_Errors 覆盖空目录、非法 JSON、非法模板与空键
//
// TestLoadCatalog_Errors covers an empty directory, invalid JSON, an invalid template and an
// empty key
func TestLoadCatalog_Errors(t *testing.T) {
	cases := []struct {
		name   string
		fs     fstest.MapFS
		wanted string
	}{
		{
			name:   "no catalog file",
			fs:     fstest.MapFS{"catalog/en/readme.txt": &fstest.MapFile{Data: []byte("text")}},
			wanted: "no catalog file",
		},
		{
			name:   "invalid json",
			fs:     fstest.MapFS{"catalog/en/a.json": &fstest.MapFile{Data: []byte(`{"key":`)}},
			wanted: "parse",
		},
		{
			name:   "non-string value",
			fs:     fstest.MapFS{"catalog/en/a.json": &fstest.MapFile{Data: []byte(`{"example.key":1}`)}},
			wanted: "parse",
		},
		{
			name:   "invalid template",
			fs:     fstest.MapFS{"catalog/en/a.json": &fstest.MapFile{Data: []byte(`{"example.key":"{{.Unclosed"}`)}},
			wanted: "compile",
		},
		{
			name:   "empty key",
			fs:     fstest.MapFS{"catalog/en/a.json": &fstest.MapFile{Data: []byte(`{"":"text"}`)}},
			wanted: "empty key",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := loadCatalog(tc.fs); err == nil || !strings.Contains(err.Error(), tc.wanted) {
				t.Fatalf("loadCatalog error = %v, want it to mention %q", err, tc.wanted)
			}
		})
	}
}

// TestNew_LanguageNormalization 覆盖空串、别名与大小写的语言归一
//
// TestNew_LanguageNormalization covers normalisation of an empty tag, an alias and mixed case
func TestNew_LanguageNormalization(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "", want: "en"},
		{in: "en", want: "en"},
		{in: "EN", want: "en"},
		{in: "zh", want: "zh-CN"},
		{in: "zh-CN", want: "zh-CN"},
		{in: "ZH-cn", want: "zh-CN"},
		{in: " zh-CN ", want: "zh-CN"},
	}
	for _, tc := range cases {
		tr, err := New(tc.in)
		if err != nil {
			t.Fatalf("New(%q): %v", tc.in, err)
		}
		if got := tr.Lang(); got != tc.want {
			t.Fatalf("New(%q).Lang() = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestNew_UnsupportedLanguage 覆盖非法语言返回 *UnsupportedLanguageError 及其字段
//
// TestNew_UnsupportedLanguage covers an unsupported language returning *UnsupportedLanguageError
// with its fields filled
func TestNew_UnsupportedLanguage(t *testing.T) {
	tr, err := New("fr")
	if tr != nil {
		t.Fatal("New returned a translator for an unsupported language")
	}
	var unsupported *UnsupportedLanguageError
	if !errors.As(err, &unsupported) {
		t.Fatalf("New error = %T, want *UnsupportedLanguageError", err)
	}
	if unsupported.Lang != "fr" {
		t.Fatalf("Lang = %q, want the caller value fr", unsupported.Lang)
	}
	if got := strings.Join(unsupported.Supported, ","); got != "en,zh-CN" {
		t.Fatalf("Supported = %v, want [en zh-CN]", unsupported.Supported)
	}
	if !strings.Contains(unsupported.Error(), "fr") {
		t.Fatalf("Error() = %q, want it to name the requested language", unsupported.Error())
	}
}

// TestSupported 覆盖支持语言列表的内容与副本语义
//
// TestSupported covers the content of the supported list and its copy semantics
func TestSupported(t *testing.T) {
	got := Supported()
	if len(got) != 2 || got[0] != "en" || got[1] != "zh-CN" {
		t.Fatalf("Supported() = %v, want [en zh-CN]", got)
	}
	got[0] = "example"
	if Supported()[0] != "en" {
		t.Fatal("Supported() exposed the package-level slice to callers")
	}
}

// TestT_Render 覆盖两种语言的占位符渲染与嵌入目录里的核心键
//
// TestT_Render covers placeholder rendering in both languages and the core keys from the
// embedded catalog
func TestT_Render(t *testing.T) {
	en, err := New("en")
	if err != nil {
		t.Fatalf("New(en): %v", err)
	}
	zh, err := New("zh-CN")
	if err != nil {
		t.Fatalf("New(zh-CN): %v", err)
	}

	if got := en.T("i18n.error.unknown_key", map[string]any{"Key": "example.key"}); got != "unknown message key example.key" {
		t.Fatalf("en T = %q", got)
	}
	if got := zh.T("channel.error.unknown_type", map[string]any{"Type": "example", "Available": "a, b"}); got != "未知渠道类型 example (可用: a, b)" {
		t.Fatalf("zh T = %q", got)
	}
	if got := en.T("channel.error.invalid_settings", map[string]any{"Field": "url", "Detail": "example detail"}); got != "invalid settings at url: example detail" {
		t.Fatalf("en T = %q", got)
	}
}

// TestT_MissingKeyFallback 覆盖 zh-CN 缺键回退 en, 以及 en 也缺时返回键名并记录警告
//
// TestT_MissingKeyFallback covers a zh-CN key missing from its own catalog falling back to en,
// and a key missing from en returning its name with a warning recorded
func TestT_MissingKeyFallback(t *testing.T) {
	// 直接在内部目录上注入一个只在 en 存在的键, 避免依赖其它 catalog 文件
	// One key that exists in en only is injected into the internal catalog, which keeps this test
	// independent of the other catalog files
	cat, err := loadEmbeddedCatalog()
	if err != nil {
		t.Fatalf("loadEmbeddedCatalog: %v", err)
	}
	tpl, err := template.New("example.test.fallback").Option("missingkey=error").Parse("fallback {{.Value}}")
	if err != nil {
		t.Fatalf("compile test template: %v", err)
	}
	cat.langs["en"]["example.test.fallback"] = tpl
	t.Cleanup(func() { delete(cat.langs["en"], "example.test.fallback") })

	zh, err := New("zh")
	if err != nil {
		t.Fatalf("New(zh): %v", err)
	}
	if got := zh.T("example.test.fallback", map[string]any{"Value": "x"}); got != "fallback x" {
		t.Fatalf("missing-key fallback = %q, want the en text", got)
	}

	tr, err := New("en")
	if err != nil {
		t.Fatalf("New(en): %v", err)
	}
	if got := tr.T("example.key.absent", nil); got != "example.key.absent" {
		t.Fatalf("absent key = %q, want the key itself", got)
	}
	if keys := tr.missingKeys(); len(keys) != 1 || keys[0] != "example.key.absent" {
		t.Fatalf("missingKeys() = %v, want one warning for the absent key", keys)
	}
}

// TestT_MissingData 覆盖占位符数据缺失时返回键名而不是半成品文案
//
// TestT_MissingData covers a missing placeholder value returning the key rather than
// half-rendered copy
func TestT_MissingData(t *testing.T) {
	tr, err := New("en")
	if err != nil {
		t.Fatalf("New(en): %v", err)
	}
	cases := []struct {
		name string
		id   string
		data map[string]any
	}{
		{name: "nil data", id: "channel.error.unknown_type", data: nil},
		{name: "missing field", id: "channel.error.unknown_type", data: map[string]any{"Type": "example"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tr.T(tc.id, tc.data); got != tc.id {
				t.Fatalf("T(%q) = %q, want the key itself when data is incomplete", tc.id, got)
			}
		})
	}
}

// TestT_Plural 覆盖英文 _one/_other 选择、只提供 _other 的兜底, 以及中文统一取 _other
//
// TestT_Plural covers the English _one/_other choice, the fallback when only _other exists,
// and Chinese taking _other for every count
func TestT_Plural(t *testing.T) {
	tree := fstest.MapFS{
		"catalog/en/x.json": &fstest.MapFile{Data: []byte(
			`{"example.count_one":"1 attempt","example.count_other":"{{.Count}} attempts","example.other_only_other":"{{.Count}} items"}`,
		)},
		"catalog/zh-CN/x.json": &fstest.MapFile{Data: []byte(
			`{"example.count_other":"{{.Count}} 次","example.other_only_other":"{{.Count}} 项"}`,
		)},
	}
	loaded, err := loadCatalog(tree)
	if err != nil {
		t.Fatalf("loadCatalog: %v", err)
	}

	en := &Translator{lang: "en", catalog: loaded}
	if got := en.T("example.count", map[string]any{"Count": 1}); got != "1 attempt" {
		t.Fatalf("count 1 = %q, want the _one text", got)
	}
	if got := en.T("example.count", map[string]any{"Count": 3}); got != "3 attempts" {
		t.Fatalf("count 3 = %q, want the _other text", got)
	}
	if got := en.T("example.other_only", map[string]any{"Count": 1}); got != "1 items" {
		t.Fatalf("only _other defined = %q, want the _other text", got)
	}

	zh := &Translator{lang: "zh-CN", catalog: loaded}
	if got := zh.T("example.count", map[string]any{"Count": 1}); got != "1 次" {
		t.Fatalf("zh count 1 = %q, want the _other text", got)
	}
}

// TestT_Concurrent 覆盖加载完成后并发调用 T
//
// TestT_Concurrent covers calling T from several goroutines once loading has finished
func TestT_Concurrent(t *testing.T) {
	tr, err := New("en")
	if err != nil {
		t.Fatalf("New(en): %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if got := tr.T("channel.error.unknown_type", map[string]any{"Type": "example", "Available": "a"}); got == "" {
					t.Error("concurrent T returned an empty string")
				}
			}
		}()
	}
	wg.Wait()
}

// TestT_NilTranslator 覆盖 nil 接收者的 T 返回键名而不是 panic
//
// TestT_NilTranslator covers a nil receiver returning the key rather than panicking
func TestT_NilTranslator(t *testing.T) {
	var tr *Translator
	if got := tr.T("example.key", nil); got != "example.key" {
		t.Fatalf("nil Translator T = %q, want the key", got)
	}
	if got := tr.Lang(); got != "" {
		t.Fatalf("nil Translator Lang = %q, want empty", got)
	}
}

// TestEmbeddedCatalog_Consistency 覆盖 en 与 zh-CN 的键集合一致、占位符一致
// 这是所有 catalog 文件的全局约束, 新增 catalog 文件时该测试即守门人
//
// TestEmbeddedCatalog_Consistency covers the key sets and placeholder sets matching between en
// and zh-CN
// That is a global constraint over every catalog file, so this test is the gatekeeper whenever a
// catalog file is added
func TestEmbeddedCatalog_Consistency(t *testing.T) {
	cat, err := loadEmbeddedCatalog()
	if err != nil {
		t.Fatalf("loadEmbeddedCatalog: %v", err)
	}
	enKeys := catalogKeys(cat, "en")
	zhKeys := catalogKeys(cat, "zh-CN")
	if len(enKeys) == 0 || len(zhKeys) == 0 {
		t.Fatal("the embedded catalog is empty")
	}

	// en 有而 zh-CN 没有的键只允许 _one 后缀 (复数规则允许中文缺 _one)
	// A key in en but not zh-CN may only carry the _one suffix, which the plural rule allows
	// Chinese to omit
	for _, key := range enKeys {
		if _, ok := cat.lookup("zh-CN", key); !ok && !strings.HasSuffix(key, pluralOneSuffix) {
			t.Errorf("zh-CN is missing key %q", key)
		}
	}
	for _, key := range zhKeys {
		if _, ok := cat.lookup("en", key); !ok {
			t.Errorf("en is missing key %q", key)
		}
	}

	for _, key := range enKeys {
		enTpl, ok := cat.lookup("en", key)
		if !ok {
			t.Fatalf("key %q missing from en", key)
		}
		zhTpl, ok := cat.lookup("zh-CN", key)
		if !ok || strings.HasSuffix(key, pluralOneSuffix) {
			continue
		}
		enNames := placeholderNames(enTpl)
		zhNames := placeholderNames(zhTpl)
		if strings.Join(enNames, ",") != strings.Join(zhNames, ",") {
			t.Errorf("key %q: en placeholders %v, zh-CN placeholders %v", key, enNames, zhNames)
		}
	}
}

// catalogKeys 返回一个语言的全部键, 排序后输出
//
// catalogKeys returns every key of one language in sorted order
func catalogKeys(cat *messageCatalog, lang string) []string {
	keys := make([]string, 0, len(cat.langs[lang]))
	for key := range cat.langs[lang] {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// placeholderNames 提取模板里的占位符名, 排序去重后输出
// 只关心数据字段 (.X), 不追踪函数调用或内置变量
//
// placeholderNames extracts the placeholder names of a template, deduplicated and sorted
// Only data fields (.X) are of interest, while function calls and built-in variables are not
// followed
func placeholderNames(tpl *template.Template) []string {
	seen := map[string]bool{}
	var walk func(node parse.Node)
	walk = func(node parse.Node) {
		if node == nil {
			return
		}
		switch n := node.(type) {
		case *parse.ListNode:
			if n == nil {
				return
			}
			for _, child := range n.Nodes {
				walk(child)
			}
		case *parse.ActionNode:
			walk(n.Pipe)
		case *parse.IfNode:
			walk(n.Pipe)
			walk(n.List)
			walk(n.ElseList)
		case *parse.RangeNode:
			walk(n.Pipe)
			walk(n.List)
			walk(n.ElseList)
		case *parse.PipeNode:
			for _, cmd := range n.Cmds {
				walk(cmd)
			}
		case *parse.CommandNode:
			for _, arg := range n.Args {
				walk(arg)
			}
		case *parse.FieldNode:
			addPlaceholder(seen, n.Ident)
		case *parse.VariableNode:
			addPlaceholder(seen, n.Ident)
		case *parse.ChainNode:
			// {{.A.B}} 解析为 ChainNode, 字段从其基础节点继续提取
			// {{.A.B}} parses into a ChainNode, and the fields keep being extracted from its base node
			walk(n.Node)
		}
	}
	walk(tpl.Root)
	return sortedPlaceholderNames(seen)
}

// addPlaceholder 把字段首段记入待去重集合, 跳过空的
//
// addPlaceholder records the first field segment into the deduplication set, skipping empty ones
func addPlaceholder(seen map[string]bool, idents []string) {
	if len(idents) > 0 && idents[0] != "" {
		seen[idents[0]] = true
	}
}

// sortedPlaceholderNames 把去重集合转为排序切片
//
// sortedPlaceholderNames turns the deduplication set into a sorted slice
func sortedPlaceholderNames(seen map[string]bool) []string {
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

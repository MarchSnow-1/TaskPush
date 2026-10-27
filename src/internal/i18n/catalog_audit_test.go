package i18n

// 本文件是 catalog 的守门测试: 原始 JSON 完整性, 两语言键与占位符一致,
// 消息可编译执行, 生产源码引用的键存在, 以及 internal/cli 不直接拼接用户文案
//
// This file is the catalog gate: raw JSON integrity, matching keys and placeholders
// across both languages, compilable and executable messages, every key referenced by production
// source being present, and no user copy assembled directly inside internal/cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

var (
	// auditCatalogKeyLiteral 匹配源码里的完整消息键, 至少包含 area.kind.name 三段
	//
	// auditCatalogKeyLiteral matches a complete message key in source, with at least the
	// area.kind.name segments
	auditCatalogKeyLiteral = regexp.MustCompile(
		`^(?:i18n|cli|config|template|common|notify|state|channel)\.[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)+$`,
	)
	// auditCatalogKeyPrefix 匹配源码里用于拼接的动态键前缀
	//
	// auditCatalogKeyPrefix matches a dynamic key prefix concatenated in source
	auditCatalogKeyPrefix = regexp.MustCompile(
		`^(?:i18n|cli|config|template|common|notify|state|channel)\.`,
	)
	// auditFormatVerb 匹配 Go 格式串里的一个动词
	//
	// auditFormatVerb matches one verb inside a Go format string
	auditFormatVerb = regexp.MustCompile(`%[+#0\-. 0-9*]*[A-Za-z]`)
)

// TestCatalogAudit_RawFiles 覆盖原始 catalog 的重复键, 空键, 空值与 printf 动词
//
// TestCatalogAudit_RawFiles covers duplicate keys, empty keys, empty values and printf verbs in
// the raw catalog
func TestCatalogAudit_RawFiles(t *testing.T) {
	messages := auditRawCatalog(t)
	for _, lang := range []string{"en", "zh-CN"} {
		if len(messages[lang]) == 0 {
			t.Errorf("%s catalog is empty", lang)
		}
	}
}

// TestCatalogAudit_KeyParityAndPlaceholders 覆盖 en/zh-CN 键集合与逐键占位符一致
//
// TestCatalogAudit_KeyParityAndPlaceholders covers the en and zh-CN key sets and the per-key
// placeholder sets matching
func TestCatalogAudit_KeyParityAndPlaceholders(t *testing.T) {
	raw := auditRawCatalog(t)
	en := raw["en"]
	zh := raw["zh-CN"]
	if len(en) == 0 || len(zh) == 0 {
		t.Fatalf("catalog is empty: en=%d zh-CN=%d", len(en), len(zh))
	}

	for _, key := range sortedStringSet(en) {
		if _, ok := zh[key]; !ok && !strings.HasSuffix(key, pluralOneSuffix) {
			t.Errorf("zh-CN is missing key %q", key)
		}
	}
	for _, key := range sortedStringSet(zh) {
		if _, ok := en[key]; !ok {
			t.Errorf("en is missing key %q", key)
		}
	}
	for _, key := range sortedStringSet(en) {
		if strings.HasPrefix(key, "channel.") && !auditChannelKey(key) {
			t.Errorf("channel key %q does not follow channel.<type>.<kind>.<name>", key)
		}
	}
	for _, key := range sortedStringSet(en) {
		if !strings.HasSuffix(key, pluralOneSuffix) {
			continue
		}
		base := strings.TrimSuffix(key, pluralOneSuffix)
		if _, ok := en[base+pluralOtherSuffix]; !ok {
			t.Errorf("%s has %q without %q", "en", key, base+pluralOtherSuffix)
		}
		if _, ok := zh[base+pluralOtherSuffix]; !ok {
			t.Errorf("%s has %q but zh-CN lacks %q", "en", key, base+pluralOtherSuffix)
		}
	}

	cat, err := loadEmbeddedCatalog()
	if err != nil {
		t.Fatalf("loadEmbeddedCatalog: %v", err)
	}
	for _, key := range sortedStringSet(en) {
		enTpl, ok := cat.lookup("en", key)
		if !ok {
			t.Errorf("compiled en catalog lost key %q", key)
			continue
		}
		zhTpl, ok := cat.lookup("zh-CN", key)
		if !ok {
			// 中文允许省略 _one 档, 由 _other 或英文回退负责该分支
			//
			// Chinese may omit the _one bucket, leaving that branch to _other or the English
			// fallback
			if strings.HasSuffix(key, pluralOneSuffix) {
				continue
			}
			t.Errorf("compiled zh-CN catalog lost key %q", key)
			continue
		}
		enPlaceholders := strings.Join(placeholderNames(enTpl), ",")
		zhPlaceholders := strings.Join(placeholderNames(zhTpl), ",")
		if enPlaceholders != zhPlaceholders {
			t.Errorf("key %q: en placeholders %q, zh-CN placeholders %q", key, enPlaceholders, zhPlaceholders)
		}
	}

	t.Logf("catalog audit: en=%d keys, zh-CN=%d keys", len(en), len(zh))
}

// TestCatalogAudit_MessagesCompileAndExecute 覆盖每个键的模板都能用占位符样例执行
//
// TestCatalogAudit_MessagesCompileAndExecute covers every key compiling and executing with sample
// placeholder data
func TestCatalogAudit_MessagesCompileAndExecute(t *testing.T) {
	cat, err := loadEmbeddedCatalog()
	if err != nil {
		t.Fatalf("loadEmbeddedCatalog: %v", err)
	}
	for _, lang := range []string{"en", "zh-CN"} {
		for _, key := range catalogKeys(cat, lang) {
			tpl, ok := cat.lookup(lang, key)
			if !ok {
				t.Errorf("%s lost compiled key %q", lang, key)
				continue
			}
			data := make(map[string]any)
			for _, name := range placeholderNames(tpl) {
				data[name] = "example"
			}
			var out strings.Builder
			if err := tpl.Execute(&out, data); err != nil {
				t.Errorf("%s key %q failed to execute: %v", lang, key, err)
			}
		}
	}
}

// TestCatalogAudit_SourceReferences 覆盖生产源码里的静态键与动态描述键都存在
//
// TestCatalogAudit_SourceReferences covers static keys and dynamic description keys referenced by
// production source being present
func TestCatalogAudit_SourceReferences(t *testing.T) {
	cat, err := loadEmbeddedCatalog()
	if err != nil {
		t.Fatalf("loadEmbeddedCatalog: %v", err)
	}
	exact, prefixes := auditSourceCatalogKeys(t)
	if len(exact) == 0 {
		t.Fatal("the production source scan found no catalog keys")
	}
	for key, position := range exact {
		if _, ok := cat.lookup("en", key); !ok {
			t.Errorf("%s references %q, which is missing from en", position, key)
		}
		if _, ok := cat.lookup("zh-CN", key); !ok && !strings.HasSuffix(key, pluralOneSuffix) {
			t.Errorf("%s references %q, which is missing from zh-CN", position, key)
		}
	}
	for prefix, position := range prefixes {
		switch prefix {
		case "cli.desc.command.", "cli.desc.flag.", "channel.":
		default:
			t.Errorf("%s uses an unregistered dynamic key prefix %q", position, prefix)
		}
	}

	// CLI 的命令/flag 描述键由固定 help 键拼接, 渠道描述键由 Factory.Describe 提供,
	// 这些动态面在这里补回比对, 静态扫描只负责发现前缀本身
	//
	// CLI command and flag description keys are assembled from the fixed help keys and channel
	// description keys come from Factory.Describe; the dynamic side is checked back here while the
	// static scan only discovers the prefixes themselves
	for _, key := range catalogKeys(cat, "en") {
		switch {
		case strings.HasPrefix(key, "cli.help.command."):
			desc := "cli.desc.command." + strings.TrimPrefix(key, "cli.help.command.")
			if _, ok := cat.lookup("en", desc); !ok {
				t.Errorf("%s has no matching %s", key, desc)
			}
		case strings.HasPrefix(key, "cli.help.flag."):
			desc := "cli.desc.flag." + strings.TrimPrefix(key, "cli.help.flag.")
			if _, ok := cat.lookup("en", desc); !ok {
				t.Errorf("%s has no matching %s", key, desc)
			}
		}
	}
}

// TestCatalogAudit_CLIOutputHasNoLiteralCopy 覆盖 internal/cli 的输出格式串不夹带用户文案
//
// TestCatalogAudit_CLIOutputHasNoLiteralCopy covers internal/cli output format strings carrying no
// user copy directly
func TestCatalogAudit_CLIOutputHasNoLiteralCopy(t *testing.T) {
	root := auditModuleRoot(t)
	files := auditGoFiles(t, filepath.Join(root, "internal", "cli"))
	for _, path := range files {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := auditCallName(call.Fun)
			text, ok := auditHardcodedCallText(name, call)
			if !ok {
				return true
			}
			position := fset.Position(call.Pos())
			t.Errorf("%s: %s contains direct output copy %q", position, name, text)
			return true
		})
	}
}

// auditRawCatalog 读取原始嵌入 JSON, 检查重复键, 空键, 空值与 printf 动词
//
// auditRawCatalog reads the raw embedded JSON and checks duplicate keys, empty keys, empty values
// and printf verbs
func auditRawCatalog(t *testing.T) map[string]map[string]string {
	t.Helper()
	paths, err := fs.Glob(embeddedCatalog, catalogPattern)
	if err != nil {
		t.Fatalf("glob embedded catalog: %v", err)
	}
	messages := map[string]map[string]string{}
	sources := map[string]map[string]string{}
	for _, path := range paths {
		segments := strings.Split(path, "/")
		if len(segments) != 3 {
			t.Errorf("unexpected catalog path %q", path)
			continue
		}
		lang := segments[1]
		raw, err := fs.ReadFile(embeddedCatalog, path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		fileMessages, err := auditDecodeMessages(raw)
		if err != nil {
			t.Errorf("decode %s: %v", path, err)
			continue
		}
		if messages[lang] == nil {
			messages[lang] = map[string]string{}
			sources[lang] = map[string]string{}
		}
		for key, text := range fileMessages {
			if strings.TrimSpace(key) == "" {
				t.Errorf("%s has an empty message key", path)
			}
			if strings.TrimSpace(text) == "" {
				t.Errorf("%s key %q has an empty message", path, key)
			}
			if auditFormatVerb.MatchString(text) {
				t.Errorf("%s key %q uses a printf verb: %q", path, key, text)
			}
			if previous, ok := sources[lang][key]; ok {
				t.Errorf("duplicate message key %q in %s and %s", key, previous, path)
			}
			messages[lang][key] = text
			sources[lang][key] = path
		}
	}
	return messages
}

// auditDecodeMessages 用 JSON token 流读取字符串映射, 因而同一文件里的重复键也能被发现
//
// auditDecodeMessages reads a string map through the JSON token stream, so a duplicate key inside
// one file is detected as well
func auditDecodeMessages(data []byte) (map[string]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("the top level is not a JSON object")
	}

	messages := map[string]string{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("a message key is not a string")
		}
		if _, duplicate := messages[key]; duplicate {
			return nil, fmt.Errorf("duplicate message key %q", key)
		}
		var value string
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		messages[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("trailing content after the JSON object")
		}
		return nil, err
	}
	return messages, nil
}

// auditSourceCatalogKeys 扫描生产 Go 源码, 返回完整键与动态前缀
//
// auditSourceCatalogKeys scans production Go source and returns complete keys and dynamic prefixes
func auditSourceCatalogKeys(t *testing.T) (map[string]string, map[string]string) {
	t.Helper()
	root := auditModuleRoot(t)
	files := auditGoFiles(t, filepath.Join(root, "internal"))
	exact := map[string]string{}
	prefixes := map[string]string{}
	for _, path := range files {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literals, ok := node.(*ast.BasicLit)
			if !ok || literals.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literals.Value)
			if err != nil || !auditCatalogKeyPrefix.MatchString(value) {
				return true
			}
			position := fset.Position(literals.Pos()).String()
			if strings.HasSuffix(value, ".") {
				prefixes[value] = position
				return true
			}
			if auditCatalogKeyLiteral.MatchString(value) {
				exact[value] = position
			}
			return true
		})
	}
	return exact, prefixes
}

// sortedStringSet 返回映射键的排序切片, 让失败输出在不同运行间稳定
//
// sortedStringSet returns the map keys as a sorted slice, which keeps failure output stable between
// runs
func sortedStringSet(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// auditChannelKey 判断渠道键是否符合 channel.<type>.<kind>.<name> 的形态
//
// auditChannelKey reports whether a channel key follows channel.<type>.<kind>.<name>
func auditChannelKey(key string) bool {
	segments := strings.Split(key, ".")
	if len(segments) < 3 || segments[0] != "channel" || segments[1] == "" {
		return false
	}
	if segments[1] == "error" {
		return len(segments) >= 3 && segments[2] != ""
	}
	if len(segments) == 3 {
		return segments[2] == "desc" || segments[2] == "default_markdown_title"
	}
	switch segments[2] {
	case "field", "error", "warn", "preview":
		return true
	}
	return false
}

// auditGoFiles 返回目录下所有非测试 Go 文件, 跳过 testdata 与 vendor
//
// auditGoFiles returns every non-test Go file below a directory, skipping testdata and vendor
func auditGoFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case "testdata", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(files)
	return files
}

// auditModuleRoot 从当前测试目录向上找到包含 go.mod 的模块根
//
// auditModuleRoot walks up from the current test directory to the module root holding go.mod
func auditModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		info, statErr := os.Stat(filepath.Join(dir, "go.mod"))
		if statErr == nil && !info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("cannot find go.mod above %s", dir)
		}
		dir = parent
	}
}

// auditCallName 返回 fmt.Fprintf 这样的限定调用名, 其它调用返回空串
//
// auditCallName returns a qualified call name such as fmt.Fprintf and an empty name for another call
func auditCallName(expr ast.Expr) string {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg, ok := selector.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return pkg.Name + "." + selector.Sel.Name
}

// auditHardcodedCallText 返回直接写在输出调用里的用户文案
//
// auditHardcodedCallText returns the user copy written directly into an output call
func auditHardcodedCallText(name string, call *ast.CallExpr) (string, bool) {
	switch name {
	case "fmt.Fprintf":
		if len(call.Args) < 2 {
			return "", false
		}
		format, ok := auditStringLiteral(call.Args[1])
		if ok && auditFormatHasCopy(format) {
			return format, true
		}
	case "fmt.Sprintf":
		if len(call.Args) < 1 {
			return "", false
		}
		format, ok := auditStringLiteral(call.Args[0])
		if ok && auditFormatHasCopy(format) {
			return format, true
		}
	case "fmt.Fprint":
		return auditLiteralArgument(call.Args, 1)
	case "fmt.Fprintln":
		return auditLiteralArgument(call.Args, 1)
	case "fmt.Sprint":
		return auditLiteralArgument(call.Args, 0)
	case "fmt.Sprintln":
		return auditLiteralArgument(call.Args, 0)
	}
	return "", false
}

// auditLiteralArgument 返回从 start 开始的第一个非空字符串字面量
//
// auditLiteralArgument returns the first non-empty string literal starting at start
func auditLiteralArgument(args []ast.Expr, start int) (string, bool) {
	for i := start; i < len(args); i++ {
		text, ok := auditStringLiteral(args[i])
		if ok && strings.TrimSpace(text) != "" {
			return text, true
		}
	}
	return "", false
}

// auditStringLiteral 把一个字符串字面量解码成普通字符串
//
// auditStringLiteral decodes a string literal into an ordinary string
func auditStringLiteral(expr ast.Expr) (string, bool) {
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

// auditFormatHasCopy 在去掉格式动词后, 判断格式串是否还剩字母等用户文案
//
// auditFormatHasCopy reports whether a format string still holds letters or other user copy after
// its format verbs are removed
func auditFormatHasCopy(format string) bool {
	stripped := strings.ReplaceAll(format, "%%", "")
	stripped = auditFormatVerb.ReplaceAllString(stripped, "")
	for _, r := range stripped {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return true
		}
	}
	return false
}

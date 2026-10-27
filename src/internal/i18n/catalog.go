package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"text/template"
)

// ---------------------------------------------------------------------------
// 消息目录加载: 嵌入 catalog/<语言>/<文件>.json, 按语言合并
// 同一个语言的重复键会中止加载, 避免两份文件对同一个键给出不同文案时静默取其一
//
// Loading the message catalogs: catalog/<language>/<file>.json is embedded and merged per
// language
// A duplicate key in one language stops the load, which keeps two files disagreeing about a
// key from silently resolving to whichever one happens to be read last
// ---------------------------------------------------------------------------

// catalogPattern 匹配语言目录下的任意 JSON 文件
// 通配符覆盖后续新增的 catalog 文件, 新增文件不需要改这里的代码
//
// catalogPattern matches any JSON file inside a language directory
// The wildcard covers catalog files added later, so a new file needs no change here
const catalogPattern = "catalog/*/*.json"

//go:embed catalog/*/*.json
var embeddedCatalog embed.FS

// messageCatalog 是加载完成的只读消息表: 语言 → 键 → 已编译模板
// 模板在加载时编译一次, 加载完成后只做 Execute, 因此并发调用 T 不需要写锁
//
// messageCatalog is the loaded read-only message table: language to key to compiled template
// Templates are compiled once while loading and only executed afterwards, so concurrent T
// calls need no write lock
type messageCatalog struct {
	langs map[string]map[string]*template.Template
}

// lookup 在指定语言里按键取模板
//
// lookup returns the template for a key in one language
func (c *messageCatalog) lookup(lang, id string) (*template.Template, bool) {
	byKey, ok := c.langs[lang]
	if !ok {
		return nil, false
	}
	tpl, ok := byKey[id]
	return tpl, ok
}

// languages 返回目录里已加载的语言标签, 顺序稳定
//
// languages returns the language tags loaded from the catalog directory in a stable order
func (c *messageCatalog) languages() []string {
	tags := make([]string, 0, len(c.langs))
	for tag := range c.langs {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}

// loadCatalog 读取目录树并合并成按语言分组的消息表
// 顶层必须是 JSON 对象, 值与键都按字符串处理
// 同一语言内重复键返回错误, 并指出冲突的两份文件
//
// loadCatalog reads the directory tree and merges it into a message table grouped by language
// The top level has to be a JSON object, and keys and values are read as strings
// A duplicate key within one language returns an error naming the two conflicting files
func loadCatalog(fsys fs.FS) (*messageCatalog, error) {
	paths, err := fs.Glob(fsys, catalogPattern)
	if err != nil {
		return nil, fmt.Errorf("i18n: glob %s: %w", catalogPattern, err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("i18n: no catalog file matched %s", catalogPattern)
	}

	cat := &messageCatalog{langs: map[string]map[string]*template.Template{}}
	sources := map[string]map[string]string{}
	for _, path := range paths {
		segments := strings.Split(path, "/")
		if len(segments) != 3 {
			return nil, fmt.Errorf("i18n: unexpected catalog path %q", path)
		}
		lang := segments[1]

		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return nil, fmt.Errorf("i18n: read %s: %w", path, err)
		}
		var messages map[string]string
		if err := json.Unmarshal(data, &messages); err != nil {
			return nil, fmt.Errorf("i18n: parse %s: %w", path, err)
		}

		if cat.langs[lang] == nil {
			cat.langs[lang] = map[string]*template.Template{}
			sources[lang] = map[string]string{}
		}
		// 按排序后的键遍历, 让重复键的报错与加载顺序无关
		// Keys are walked in sorted order, which keeps a duplicate-key error independent of file order
		keys := make([]string, 0, len(messages))
		for key := range messages {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		for _, key := range keys {
			if key == "" {
				return nil, fmt.Errorf("i18n: %s holds a message with an empty key", path)
			}
			if prev, dup := sources[lang][key]; dup {
				return nil, fmt.Errorf("i18n: duplicate message key %q in %s and %s", key, prev, path)
			}
			tpl, err := template.New(key).Option("missingkey=error").Parse(messages[key])
			if err != nil {
				return nil, fmt.Errorf("i18n: compile %s key %q: %w", path, key, err)
			}
			cat.langs[lang][key] = tpl
			sources[lang][key] = path
		}
	}
	return cat, nil
}

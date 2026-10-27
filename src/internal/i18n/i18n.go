package i18n

import (
	"fmt"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// 翻译器: 语言归一 + 按当前语言渲染消息键
// i18n 是叶子包, 不导入 apperr; Problem 到文案的转换由 CLI 完成
//
// The translator: language normalisation and message rendering in the active language
// i18n is a leaf package and does not import apperr, so converting a Problem into copy
// stays with the CLI
// ---------------------------------------------------------------------------

const (
	// supportedLanguageEN 是默认语言, 也是缺失键最终回退到的语言
	// supportedLanguageEN is the default language and the one a missing key falls back to
	supportedLanguageEN = "en"
	// supportedLanguageZHCN 是简体中文的规范标签
	// supportedLanguageZHCN is the canonical tag of Simplified Chinese
	supportedLanguageZHCN = "zh-CN"
	// supportedLanguageZHAlias 是简体中文的短标签, 归一为规范标签
	// supportedLanguageZHAlias is the short tag of Simplified Chinese, normalised to the canonical one
	supportedLanguageZHAlias = "zh"
)

// supportedLanguages 是支持的语言标签, 顺序固定为 en 与 zh-CN
//
// supportedLanguages holds the supported language tags, fixed in the order en then zh-CN
var supportedLanguages = []string{supportedLanguageEN, supportedLanguageZHCN}

var (
	// catalogOnce 保证嵌入目录只加载一次, 加载结果由 catalogErr 保存
	// catalogOnce keeps the embedded directory loaded once, with the outcome held in catalogErr
	catalogOnce sync.Once
	// loadedCatalog 是加载完成的消息表, 加载成功后按只读方式使用
	// loadedCatalog is the loaded message table, used read-only once loading succeeds
	loadedCatalog *messageCatalog
	// catalogErr 记录加载失败原因, 供每次 New 原样返回
	// catalogErr records a loading failure so every New call can return it
	catalogErr error
)

// UnsupportedLanguageError 表示请求的语言不在支持列表里
// Lang 保存调用方传来的原值, Supported 保存当时支持的语言标签
//
// UnsupportedLanguageError reports a requested language that is not in the supported list
// Lang holds the value the caller passed in, and Supported holds the supported tags at that moment
type UnsupportedLanguageError struct {
	Lang      string
	Supported []string
}

// Error 返回英文调试文本, 由 CLI 通过 errors.As 转成用户可见的本地化错误
//
// Error returns English debugging text, which the CLI turns into a localised user-visible
// error through errors.As
func (e *UnsupportedLanguageError) Error() string {
	return fmt.Sprintf("unsupported language %q (supported: %s)", e.Lang, strings.Join(e.Supported, ", "))
}

// Translator 按一种语言渲染消息, 加载完成后可被多个 goroutine 并发调用
//
// Translator renders messages in one language and is safe for concurrent calls once loading
// has finished
type Translator struct {
	lang    string
	catalog *messageCatalog
	// 未命中最终回退语言时记录警告, 只记第一次, 避免同一缺键刷满日志
	// A miss in the final fallback language records a warning once per key, which keeps a
	// repeated miss from filling the log
	missingOnce sync.Map
}

// Supported 返回支持的语言标签副本, 顺序为 en 与 zh-CN
// 返回副本让调用方无法改写包级列表
//
// Supported returns a copy of the supported language tags in the order en then zh-CN
// The copy keeps a caller from rewriting the package-level list
func Supported() []string {
	out := make([]string, len(supportedLanguages))
	copy(out, supportedLanguages)
	return out
}

// normalizeLanguage 归一语言标签: 空串与仅空白的串回默认语言, 大小写不敏感, zh 归一为 zh-CN
// 比较基于全小写形式, 因此 "ZH-CN" 与 "zh-cn" 都命中简体中文
// 不支持的语言原样返回, 由 New 构造 UnsupportedLanguageError
//
// normalizeLanguage normalises a language tag: an empty or whitespace-only string takes the
// default language, matching is case-insensitive and zh normalises to zh-CN
// The comparison runs on the lowercased form, so both "ZH-CN" and "zh-cn" hit Simplified Chinese
// An unsupported value comes back as it was, and New turns it into an UnsupportedLanguageError
func normalizeLanguage(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "":
		return supportedLanguageEN
	case supportedLanguageEN:
		return supportedLanguageEN
	case supportedLanguageZHAlias, strings.ToLower(supportedLanguageZHCN):
		return supportedLanguageZHCN
	}
	return lang
}

// loadEmbeddedCatalog 加载一次嵌入目录并缓存结果
//
// loadEmbeddedCatalog loads the embedded directory once and caches the outcome
func loadEmbeddedCatalog() (*messageCatalog, error) {
	catalogOnce.Do(func() {
		loadedCatalog, catalogErr = loadCatalog(embeddedCatalog)
	})
	return loadedCatalog, catalogErr
}

// New 归一语言并返回翻译器
// 空串取 en, zh 取 zh-CN, 比较大小写不敏感; 不支持的语言返回 *UnsupportedLanguageError
// 嵌入目录加载失败 (重复键/JSON 非法/模板非法) 时返回该错误, 让问题在启动阶段暴露
//
// New normalises the language and returns a translator
// An empty string takes en, zh takes zh-CN, and matching is case-insensitive, while an
// unsupported language returns an *UnsupportedLanguageError
// A failed embedded-catalog load, through a duplicate key, invalid JSON or an invalid
// template, surfaces that error here so the problem appears at startup
func New(lang string) (*Translator, error) {
	catalog, err := loadEmbeddedCatalog()
	if err != nil {
		return nil, err
	}
	normalized := normalizeLanguage(lang)
	if !isSupported(normalized) {
		return nil, &UnsupportedLanguageError{Lang: lang, Supported: Supported()}
	}
	return &Translator{lang: normalized, catalog: catalog}, nil
}

// isSupported 判断归一后的标签是否在支持列表里
//
// isSupported reports whether a normalised tag is in the supported list
func isSupported(lang string) bool {
	for _, tag := range supportedLanguages {
		if tag == lang {
			return true
		}
	}
	return false
}

// Lang 返回归一后的语言标签
//
// Lang returns the normalised language tag
func (t *Translator) Lang() string {
	if t == nil {
		return ""
	}
	return t.lang
}

// T 用给定数据渲染消息键
// 顺序为当前语言精确命中、当前语言的单复数档位、en 的精确命中、en 的单复数档位
// 全部未命中时记录一次警告并返回 id 本身; 模板执行失败 (占位符数据缺失) 同样返回 id
//
// T renders a message key with the given data
// The order is an exact hit in the active language, its plural buckets, an exact hit in en,
// then the en plural buckets
// When nothing matches it records one warning and returns id as it was, and a failed template
// execution, such as missing placeholder data, returns id as well
func (t *Translator) T(id string, data map[string]any) string {
	if t == nil || t.catalog == nil {
		return id
	}
	for _, lang := range t.candidateLanguages() {
		for _, key := range append([]string{id}, pluralKeyCandidates(lang, id, data)...) {
			tpl, ok := t.catalog.lookup(lang, key)
			if !ok {
				continue
			}
			var sb strings.Builder
			if err := tpl.Execute(&sb, data); err != nil {
				// 占位符数据缺失属于程序错误: 返回键名让测试与审计能发现, 不输出半成品文案
				// Missing placeholder data is a program error, so the key name comes back for
				// tests and audits to catch instead of emitting half-rendered copy
				t.warnMissing(id)
				return id
			}
			return sb.String()
		}
	}
	t.warnMissing(id)
	return id
}

// candidateLanguages 返回查找顺序: 当前语言在前, 非 en 时追加默认语言
//
// candidateLanguages returns the lookup order: the active language first, then the default
// language when the active one is not en
func (t *Translator) candidateLanguages() []string {
	if t.lang == supportedLanguageEN {
		return []string{supportedLanguageEN}
	}
	return []string{t.lang, supportedLanguageEN}
}

// warnMissing 记录一次缺键警告, 同一个键只记第一次
// 记录结果保存在 Translator 内部, 便于测试断言; 不向标准输出或标准错误写内容
//
// warnMissing records one missing-key warning and only the first one per key
// The record stays inside the Translator so tests can assert on it, and nothing is written to
// standard output or standard error
func (t *Translator) warnMissing(id string) {
	t.missingOnce.LoadOrStore(id, struct{}{})
}

// missingKeys 返回已记录警告的键, 顺序不保证
//
// missingKeys returns the keys whose warning was recorded, in no particular order
func (t *Translator) missingKeys() []string {
	var keys []string
	t.missingOnce.Range(func(key, _ any) bool {
		if s, ok := key.(string); ok {
			keys = append(keys, s)
		}
		return true
	})
	return keys
}

package i18n

import (
	"encoding/json"
	"strconv"
)

// ---------------------------------------------------------------------------
// 英文单复数: 只覆盖 _one/_other 两档, 不做完整 CLDR 规则
// 中文等其它语言统一取 _other 档
//
// English plurals: only the _one and _other buckets are covered, without the full CLDR rules
// Other languages such as Chinese take the _other bucket for every count
// ---------------------------------------------------------------------------

const (
	// pluralOneSuffix 英文数量为 1 时使用的键后缀
	// pluralOneSuffix is the key suffix English uses for a count of one
	pluralOneSuffix = "_one"
	// pluralOtherSuffix 其余数量使用的键后缀, 也是非英文语言的唯一档位
	// pluralOtherSuffix is the key suffix for every other count and the only bucket a
	// non-English language takes
	pluralOtherSuffix = "_other"
)

// pluralKeyCandidates 返回 id 之外还应尝试的单复数键, 顺序为优先到次选
// 数量为 1 的英文先试 _one, 再退回 _other, 这样只提供 _other 的英文文案仍然可用
// 其它语言只给出 _other
//
// pluralKeyCandidates returns the plural keys to try besides id, from the most to the least
// preferred
// English with a count of one tries _one first and falls back to _other, which keeps a
// message that only defines _other usable
// Every other language offers _other alone
func pluralKeyCandidates(lang, id string, data map[string]any) []string {
	if lang != supportedLanguageEN {
		return []string{id + pluralOtherSuffix}
	}
	if count, ok := pluralCount(data); ok && count == 1 {
		return []string{id + pluralOneSuffix, id + pluralOtherSuffix}
	}
	return []string{id + pluralOtherSuffix}
}

// pluralCount 从占位符数据里取 Count, 取不到或不是数值时返回 false
// 支持 JSON 解码与常见数值类型; 其它类型按 false 处理, 调用方走 _other 档
//
// pluralCount reads Count from the placeholder data and reports false when it is absent or
// not a number
// The JSON decoding and common numeric types are supported, while any other type reports
// false so the caller takes the _other bucket
func pluralCount(data map[string]any) (float64, bool) {
	if data == nil {
		return 0, false
	}
	switch v := data["Count"].(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float64:
		return v, true
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

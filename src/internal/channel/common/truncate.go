package common

import (
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// 长度截断: 各平台的上限口径不同 (钉钉/企业微信按字节, Telegram 按 UTF-16 code unit,
// ServerChan 按字符), 所以这里给出三种计量, 语义统一
// limit <= 0 一律视为不限制, 原样返回且 truncated=false
// truncated=true 时调用方必须发警告 (channel.<type>.warn.truncated), 不得静默发送
//
// Length truncation: the platforms measure against different limits (DingTalk and WeCom in bytes,
// Telegram in UTF-16 code units, ServerChan in characters), so three measures live here with one
// shared semantics
// A limit of zero or less means no limit: the text comes back unchanged with truncated=false
// When truncated is true the caller has to raise a warning (channel.<type>.warn.truncated)
// instead of sending silently
// ---------------------------------------------------------------------------

// TruncateBytes 按 UTF-8 字节数截断, 切点回退到字符边界, 因此输出永远是合法 UTF-8
// 一个字符本身放不进 limit 时整个字符被丢掉, 不留半个字符
//
// TruncateBytes cuts by UTF-8 bytes and steps the cut point back to a character boundary, so the
// output is always valid UTF-8
// A character that does not fit inside the limit is dropped whole rather than left as half of it
func TruncateBytes(s string, limit int) (string, bool) {
	if limit <= 0 {
		return s, false
	}
	if len(s) <= limit {
		return s, false
	}
	cut := limit
	// 回退到 UTF-8 起始字节, 因为续字节的形态是 10xxxxxx
	// Stepping back to a UTF-8 start byte, since a continuation byte has the form 10xxxxxx
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

// TruncateRunes 按 rune 个数截断, 与实际字数无关 (中文与 emoji 各算 1)
//
// TruncateRunes cuts by rune count, independent of the visible characters (a Chinese character
// and an emoji each count as 1)
func TruncateRunes(s string, limit int) (string, bool) {
	if limit <= 0 {
		return s, false
	}
	count := 0
	for i := range s {
		if count == limit {
			return s[:i], true
		}
		count++
	}
	return s, false
}

// TruncateUTF16 按 UTF-16 code unit 截断: BMP 内字符占 1 个单元, BMP 外字符 (如 emoji) 占 2 个单元
// 放不进预算时整个字符被丢掉, 所以不会留下半个代理对
//
// TruncateUTF16 cuts by UTF-16 code units: a character in the BMP takes one unit and one outside
// it, an emoji for instance, takes two
// A character that does not fit the budget is dropped whole, which keeps a lone surrogate from
// ever being left behind
func TruncateUTF16(s string, limit int) (string, bool) {
	if limit <= 0 {
		return s, false
	}
	units := 0
	for i, r := range s {
		width := 1
		if r > 0xFFFF {
			width = 2
		}
		if units+width > limit {
			return s[:i], true
		}
		units += width
	}
	return s, false
}

// JoinTitleBody 把标题与正文拼成一条文本: 两者都在时以空行分隔, 只有一个时返回它, 都空时空串
// 不做 TrimSpace, 调用方需要判空时自己先 trim
//
// JoinTitleBody joins a title and a body into one text: a blank line between them when both are
// present, the one that exists when only one does, and an empty string when both are empty
// It does not TrimSpace, so a caller that has to test for emptiness trims first
func JoinTitleBody(title, body string) string {
	if title == "" {
		return body
	}
	if body == "" {
		return title
	}
	return title + "\n\n" + body
}

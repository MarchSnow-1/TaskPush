package common

import (
	"regexp"
	"strings"
)

// ---------------------------------------------------------------------------
// 错误文本脱敏: 错误会进 CI 日志, 而地址常带凭据, 所以打印前必须把 scheme://... 打码
// SanitizeError 只改写 Error() 文本, Unwrap 保留原始错误, errors.Is / errors.As 因此照旧穿透
// 调用方打印时只用 .Error(), 不要用 %+v 展开原始错误链, 那会把带凭据的地址重新暴露出来
//
// Error-text redaction: an error ends up in the CI log while an address often carries credentials,
// so a scheme://... has to be masked before printing
// SanitizeError rewrites the Error() text only and keeps Unwrap returning the original error, which
// leaves errors.Is and errors.As reaching through as before
// Callers print with .Error() and avoid %+v deep formatting of the original chain, which would
// expose the addresses holding credentials again
// ---------------------------------------------------------------------------

// urlInTextRegexp 匹配文本里的地址: scheme 形态合法后跟 "://" 与到下一个空白或引号为止的内容
// 替换文本与 MaskURL 的兜底值同为 <redacted-url>
//
// urlInTextRegexp matches an address inside free text: a well-formed scheme plus "://" and
// whatever follows up to the next space or quote
// The replacement shares the value of the MaskURL fallback, <redacted-url>
var urlInTextRegexp = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.\-]*://[^ \t\n\r"']+`)

// sanitizedError 是脱敏包装: Error() 输出打码后的文本, Unwrap() 返回原错误
// 它不是 *apperr.Problem, 因为 Problem 的 Error() 会拼上 Cause 原文, 那正是这里的凭据来源
//
// sanitizedError is the redaction wrapper: Error() prints the masked text while Unwrap() returns
// the original error
// It is deliberately not an *apperr.Problem, whose Error() appends the Cause text, which is the
// very credential source being masked here
type sanitizedError struct {
	msg string
	err error
}

// Error 返回脱敏后的文本
//
// Error returns the redacted text
func (e *sanitizedError) Error() string { return e.msg }

// Unwrap 返回原始错误, 让 errors.Is / errors.As 继续穿透
//
// Unwrap returns the original error so errors.Is and errors.As keep reaching through
func (e *sanitizedError) Unwrap() error { return e.err }

// SanitizeError 把错误文本里的地址打码, nil 进 nil 出
// 渠道在返回网络/请求错误前调用它, 原始错误只经 Unwrap 可达
//
// SanitizeError masks the addresses inside an error text and returns nil for a nil input
// A channel calls it before returning a network or request error, leaving the original error
// reachable through Unwrap only
func SanitizeError(err error) error {
	if err == nil {
		return nil
	}
	return &sanitizedError{
		msg: SanitizeString(err.Error()),
		err: err,
	}
}

// SanitizeString 按同一规则打码字符串, 供 Data 里的自由文本使用
// 匹配到的地址若紧跟句读 (如 ": " 前的冒号、行末的逗号或句号), 这些字符会留在打码结果之外,
// 因为错误文本里的标点属于句子而不属于地址: "https://example.com/x: dial failed" 打成
// "<redacted-url>: dial failed"
// 配对括号按平衡判断: 地址自身含左括号时 (如 path 里的 "a(b)"), 末尾右括号算地址的一部分
// 没有匹配到地址时原样返回
//
// SanitizeString masks a string by the same rule, for the free text inside Problem.Data
// Punctuation right after a matched address, such as the colon before a clause or a comma at the
// end of a line, stays outside the masked result, since it belongs to the sentence rather than to
// the address: "https://example.com/x: dial failed" becomes "<redacted-url>: dial failed"
// A closing bracket is matched against its opener, so a trailing ")" counts as part of the address
// when the address itself holds an "(", as in a path of "a(b)"
// A string with no address in it comes back unchanged
func SanitizeString(s string) string {
	return urlInTextRegexp.ReplaceAllStringFunc(s, func(match string) string {
		body := trimTrailingPunctuation(match)
		return redactedURL + match[len(body):]
	})
}

// trimTrailingPunctuation 返回 match 去掉尾部句读的前缀, 供脱敏后原样接回
// 冒号/逗号/分号/句号/问号/叹号总是算句读; 右括号只在没有对应左括号时才算
//
// trimTrailingPunctuation returns the prefix of match with its trailing punctuation removed, so
// the punctuation can be appended after the redaction text
// A colon, comma, semicolon, full stop, question mark or exclamation mark always counts as
// punctuation, while a closing bracket counts only when its opener is absent
func trimTrailingPunctuation(match string) string {
	pairs := map[byte]byte{')': '(', ']': '[', '}': '{'}
	cut := len(match)
	for cut > 0 {
		c := match[cut-1]
		_, isCloser := pairs[c]
		switch {
		case c == ':' || c == ',' || c == ';' || c == '.' || c == '?' || c == '!':
			cut--
		case isCloser && !strings.ContainsRune(match[:cut-1], rune(pairs[c])):
			cut--
		case isCloser:
			return match[:cut]
		default:
			return match[:cut]
		}
	}
	return match[:cut]
}

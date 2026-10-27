package common

import (
	"testing"
)

// ---------------------------------------------------------------------------
// Header 校验测试: 名单按 RFC 7230 tchar, 单值禁 CR/LF/NUL, 覆盖注入面
//
// Header validation tests: the name follows the RFC 7230 tchar set and a value forbids CR, LF and
// NUL, covering the injection surface
// ---------------------------------------------------------------------------

func TestValidHeaderName(t *testing.T) {
	valid := []string{
		"X-Api-Key",
		"Content-Type",
		"Priority",
		"X-Tag_1",
		"a",
		"0",
		"!#$%&'*+-.^_`|~", // 全部 tchar 符号 / every tchar symbol
		"X-Custom-9",
	}
	for _, s := range valid {
		if !ValidHeaderName(s) {
			t.Errorf("ValidHeaderName(%q) = false, want true", s)
		}
	}

	invalid := []string{
		"",
		"X Api",                 // 空格不是 tchar / a space is not a tchar
		"X:Api",                 // 冒号分隔名单与值 / the colon separates a name from its value
		"X/Api",                 // 斜杠是分隔符 / a slash is a separator
		"X(Api)",                // 括号属于分隔符 / parentheses are separators
		"X;Api",                 // 分号是分隔符 / a semicolon is a separator
		"X@Api",                 // at 符号是分隔符 / an at sign is a separator
		"X,Api",                 // 逗号是分隔符 / a comma is a separator
		"X\"Api",                // 引号不属于 tchar / a quote is not a tchar
		"X\\Api",                // 反斜杠不属于 tchar / a backslash is not a tchar
		"X\tApi",                // HTAB 不是 tchar / HTAB is not a tchar
		"X\nApi",                // 换行不是 tchar / a newline is not a tchar
		"header\r\nInjected: 1", // 换行注入 / CRLF injection
		"X-Ünicode",             // 非 ASCII / non-ASCII
		"  X-Api",               // 前导空格 / a leading space
	}
	for _, s := range invalid {
		if ValidHeaderName(s) {
			t.Errorf("ValidHeaderName(%q) = true, want false", s)
		}
	}
}

func TestValidHeaderValue(t *testing.T) {
	valid := []string{
		"",
		"application/json",
		"Bearer example",
		"leading and trailing space ",
		"tab\there",            // HTAB 允许 / HTAB is allowed
		"中文值",                  // obs-text 交给 net/http / obs-text is left to net/http
		"emoji 😀",              // 非 ASCII 字节一律放行 / every non-ASCII byte passes
		`quotes "and" slashes`, // 双引号与正斜杠都在可见 ASCII 内 / both the double quote and the slash are printable ASCII
		"~!#$%&'*+-.^_`|()",    // 可见标点无论是否 tchar 都放行 / printable punctuation passes whether or not it is a tchar
	}
	for _, s := range valid {
		if !ValidHeaderValue(s) {
			t.Errorf("ValidHeaderValue(%q) = false, want true", s)
		}
	}

	invalid := []string{
		"line\r\nX-Injected: 1", // CR/LF 头部注入 / CR/LF header injection
		"line\nsecond",
		"line\rsecond",
		"NUL\x00inside",
		"bell\x07",
		"vertical\x0btab",
		"form\x0cfeed",
		"esc\x1b[0m",
		"del\x7f",
	}
	for _, s := range invalid {
		if ValidHeaderValue(s) {
			t.Errorf("ValidHeaderValue(%q) = true, want false", s)
		}
	}
}

// TestValidHeaderValueMatchesNetHTTP 钉住 ValidHeaderValue 的取值分类: 控制字符被拒, HTAB 与非 ASCII 放行
// 分类口径取自 net/http 写出请求时的检查, 本用例只走取值表, 不构造真实请求
//
// TestValidHeaderValueMatchesNetHTTP pins the value classification of ValidHeaderValue: a control
// character is rejected while HTAB and non-ASCII pass
// The classification comes from the check net/http applies while writing a request, and this case
// walks the value table only without building a real request
func TestValidHeaderValueMatchesNetHTTP(t *testing.T) {
	cases := []struct {
		value string
		ok    bool
	}{
		{"application/json", true},
		{"带中文", true},
		{"tab\there", true},
		{"evil\r\nX-Injected: 1", false},
		{"evil\nX-Injected: 1", false},
		{"nul\x00", false},
	}
	for _, c := range cases {
		if got := ValidHeaderValue(c.value); got != c.ok {
			t.Errorf("ValidHeaderValue(%q) = %v, want %v", c.value, got, c.ok)
		}
	}
}

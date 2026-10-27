package common

import (
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// 截断测试: 三种计量各自验证边界, 并统一检查 limit <= 0 的「不限制」语义与不产生半个字符
//
// Truncation tests: each of the three measures gets its own boundary checks, together with the
// shared limit <= 0 "no limit" rule and the promise that no half character is ever produced
// ---------------------------------------------------------------------------

func TestTruncateBytes(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		limit int
		want  string
		trunc bool
	}{
		{"ascii fits", "example", 10, "example", false},
		{"ascii exact", "example", 7, "example", false},
		{"ascii cut", "example", 3, "exa", true},
		{"chinese multi byte", "你好世界", 7, "你好", true}, // 3+3=6, 再切落在 "世" 的第二个字节
		{"chinese boundary", "你好世界", 6, "你好", true},   // 边界正好落在字符起始字节
		{"chinese fits", "你好", 6, "你好", false},
		{"emoji dropped whole", "a😀b", 2, "a", true}, // "😀" 占 4 字节, 放不进就整个丢弃
		{"limit zero", "example", 0, "example", false},
		{"limit negative", "example", -1, "example", false},
		{"empty", "", 5, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, trunc := TruncateBytes(c.in, c.limit)
			if got != c.want || trunc != c.trunc {
				t.Errorf("TruncateBytes(%q, %d) = (%q, %v), want (%q, %v)",
					c.in, c.limit, got, trunc, c.want, c.trunc)
			}
			if !utf8.ValidString(got) {
				t.Errorf("TruncateBytes(%q, %d) produced invalid UTF-8: %q", c.in, c.limit, got)
			}
			if c.limit > 0 && len(got) > c.limit {
				t.Errorf("TruncateBytes(%q, %d) length %d beyond the limit", c.in, c.limit, len(got))
			}
		})
	}
}

func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		limit int
		want  string
		trunc bool
	}{
		{"ascii cut", "example", 3, "exa", true},
		{"chinese by rune", "你好世界", 2, "你好", true},
		{"emoji one rune", "😀😀😀", 2, "😀😀", true},
		{"mixed", "a你😀b", 2, "a你", true},
		{"fits", "你好", 5, "你好", false},
		{"exact", "你好", 2, "你好", false},
		{"limit zero", "example", 0, "example", false},
		{"limit negative", "example", -3, "example", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, trunc := TruncateRunes(c.in, c.limit)
			if got != c.want || trunc != c.trunc {
				t.Errorf("TruncateRunes(%q, %d) = (%q, %v), want (%q, %v)",
					c.in, c.limit, got, trunc, c.want, c.trunc)
			}
			if c.limit > 0 && len([]rune(got)) > c.limit {
				t.Errorf("TruncateRunes(%q, %d) rune count beyond the limit", c.in, c.limit)
			}
		})
	}
}

// TestTruncateUTF16 覆盖 BMP 内外宽度差与代理对完整性
//
// TestTruncateUTF16 covers the BMP/non-BMP width difference and surrogate-pair integrity
func TestTruncateUTF16(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		limit int
		want  string
		trunc bool
	}{
		{"bmp one unit", "example", 3, "exa", true},
		{"emoji two units", "😀", 1, "", true},
		{"emoji two units fit", "😀", 2, "😀", false},
		{"emoji pair cut", "a😀b", 2, "a", true},
		{"mixed budget", "你😀", 3, "你😀", false},
		{"mixed over budget", "你😀", 2, "你", true},
		{"limit zero", "example", 0, "example", false},
		{"limit negative", "example", -1, "example", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, trunc := TruncateUTF16(c.in, c.limit)
			if got != c.want || trunc != c.trunc {
				t.Errorf("TruncateUTF16(%q, %d) = (%q, %v), want (%q, %v)",
					c.in, c.limit, got, trunc, c.want, c.trunc)
			}
			if len(utf16.Encode([]rune(got))) > c.limit && c.limit > 0 {
				t.Errorf("TruncateUTF16(%q, %d) = %q, beyond the UTF-16 budget", c.in, c.limit, got)
			}
		})
	}
}

// TestTruncateUTF16NoLoneSurrogate 确认输出里不会留下半个代理对
// utf16.Encode 对孤立代理项会写出 U+FFFD, 因此比对回环即可发现拆分
//
// TestTruncateUTF16NoLoneSurrogate confirms no half surrogate pair survives in the output
// utf16.Encode turns a lone surrogate into U+FFFD, so a round trip reveals any split
func TestTruncateUTF16NoLoneSurrogate(t *testing.T) {
	in := strings.Repeat("x😀", 40)
	for limit := 1; limit <= 12; limit++ {
		got, _ := TruncateUTF16(in, limit)
		if strings.ContainsRune(got, utf8.RuneError) {
			t.Fatalf("limit %d produced a replacement rune in %q", limit, got)
		}
		back := string(utf16.Decode(utf16.Encode([]rune(got))))
		if back != got {
			t.Errorf("limit %d round trip = %q, want %q", limit, back, got)
		}
	}
}

// TestTruncateInvalidUTF8Input 覆盖坏输入不 panic: 截断辅助函数的调用方可能拿到模板渲染结果
//
// TestTruncateInvalidUTF8Input covers that malformed input does not panic, since a caller may
// hand over a rendered template
func TestTruncateInvalidUTF8Input(t *testing.T) {
	in := "a\xffb"
	if got, _ := TruncateBytes(in, 2); len(got) > 2 {
		t.Errorf("TruncateBytes on invalid UTF-8 = %q, want at most 2 bytes", got)
	}
	if got, _ := TruncateRunes(in, 1); got != "a" {
		t.Errorf("TruncateRunes on invalid UTF-8 = %q, want %q", got, "a")
	}
	if got, _ := TruncateUTF16(in, 1); got != "a" {
		t.Errorf("TruncateUTF16 on invalid UTF-8 = %q, want %q", got, "a")
	}
}

func TestJoinTitleBody(t *testing.T) {
	cases := []struct {
		name  string
		title string
		body  string
		want  string
	}{
		{"both", "title", "body", "title\n\nbody"},
		{"title only", "title", "", "title"},
		{"body only", "", "body", "body"},
		{"both empty", "", "", ""},
		{"whitespace counts as present", " ", " ", " \n\n "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := JoinTitleBody(c.title, c.body); got != c.want {
				t.Errorf("JoinTitleBody(%q, %q) = %q, want %q", c.title, c.body, got, c.want)
			}
		})
	}
}

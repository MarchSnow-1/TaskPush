package cli

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// 表格用例: 显示宽度计算与中英混合列对齐
// text/tabwriter 按 rune 计数, 因此本包先把每列补到显示宽度, 再交给它
//
// Table cases: the display-width calculation and the alignment of mixed Chinese and English
// columns
// text/tabwriter counts runes, so this package pads each column to its display width before handing
// it over
// ---------------------------------------------------------------------------

// TestDisplayWidth 覆盖 ASCII, CJK, 全角, 组合与零宽字符的宽度
//
// TestDisplayWidth covers the width of ASCII, CJK, fullwidth, combining and zero-width characters
func TestDisplayWidth(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{in: "", want: 0},
		{in: "example", want: 7},
		{in: "ID", want: 2},
		{in: "发送成功", want: 8},
		{in: "中文", want: 4},
		{in: "a中b", want: 4},
		{in: "Ａ", want: 2},
		{in: "。", want: 2},
		{in: "e\u0301", want: 1},
		{in: "\u200b", want: 0},
		{in: "あア한", want: 6},
	}
	for _, tc := range cases {
		if got := displayWidth(tc.in); got != tc.want {
			t.Errorf("displayWidth(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestPad 覆盖按显示宽度补齐
//
// TestPad covers padding by display width
func TestPad(t *testing.T) {
	if got := pad("中文", 6); got != "中文  " {
		t.Fatalf("pad(%q) = %q, want two trailing spaces", "中文", got)
	}
	if got := pad("example", 3); got != "example" {
		t.Fatalf("pad truncated a longer text to %q", got)
	}
	if got := pad("中文", 4); got != "中文" {
		t.Fatalf("pad(%q) = %q, want no change", "中文", got)
	}
}

// TestTable_MixedLanguageAlignment 覆盖中英混合表格每一列的对齐
// 断言每个数据行的列起始显示位置与表头一致
//
// TestTable_MixedLanguageAlignment covers the per-column alignment of a mixed-language table
// It asserts that every data row starts each column at the same display position as the header
func TestTable_MixedLanguageAlignment(t *testing.T) {
	var out strings.Builder
	tb := newTable("ID", "TYPE", "DETAIL")
	tb.addRow("ops", "webhook", "发送成功")
	tb.addRow("long-id", "example", "sent")
	tb.render(&out)

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("rendered %d lines, want 3:\n%s", len(lines), out.String())
	}
	headerStarts := columnStarts(lines[0])
	for _, line := range lines[1:] {
		starts := columnStarts(line)
		if len(starts) != len(headerStarts) {
			t.Fatalf("line %q has %d columns, header has %d", line, len(starts), len(headerStarts))
		}
		for i := range starts {
			if starts[i] != headerStarts[i] {
				t.Fatalf("line %q column %d starts at %d, header at %d\n%s", line, i, starts[i], headerStarts[i], out.String())
			}
		}
	}
}

// columnStarts 返回一行里各列起始的显示位置
// 列之间以两个及以上空格分隔, 列内不出现连续空格
//
// columnStarts returns the display position where each column of a line starts
// Columns are separated by two or more spaces, and a column holds no run of spaces itself
func columnStarts(line string) []int {
	var starts []int
	position := 0
	inColumn := false
	spaces := 0
	for _, r := range line {
		if r == ' ' {
			spaces++
			position++
			continue
		}
		if !inColumn || spaces >= tabPadding {
			starts = append(starts, position)
			inColumn = true
		}
		spaces = 0
		position += runeWidth(r)
	}
	return starts
}

// TestTable_TrailingSpaces 覆盖数据行末尾不留下多余空白
//
// TestTable_TrailingSpaces covers a data row leaving no trailing whitespace
func TestTable_TrailingSpaces(t *testing.T) {
	var out strings.Builder
	tb := newTable("ID", "DETAIL")
	tb.addRow("ops", "sent")
	tb.render(&out)

	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if line != strings.TrimRight(line, " ") {
			t.Fatalf("line %q carries trailing spaces", line)
		}
	}
}

// TestTable_ShortRow 覆盖列数不足时按表头补齐
//
// TestTable_ShortRow covers a row with missing cells being padded to the header
func TestTable_ShortRow(t *testing.T) {
	var out strings.Builder
	tb := newTable("A", "B", "C")
	tb.addRow("only")
	tb.render(&out)
	if !strings.Contains(out.String(), "only") {
		t.Fatalf("rendered output lost the cell:\n%s", out.String())
	}
}

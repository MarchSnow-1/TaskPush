package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"unicode"
)

// ---------------------------------------------------------------------------
// CJK 宽度表格: text/tabwriter 默认按字节数对齐, 中日韩与全角字符显示宽度是两列
// 这里用 displayWidth 提供显示宽度函数给 tabwriter, 并用 pad 构造分隔线
//
// CJK-aware tables: text/tabwriter aligns by byte count by default, while CJK and fullwidth glyphs
// take two columns
// displayWidth supplies the width function tabwriter measures with, and pad builds the divider line
// ---------------------------------------------------------------------------

// tabPadding 是列之间保留的空格数
//
// tabPadding is the number of spaces kept between columns
const tabPadding = 2

// displayWidth 计算一个字符串的终端显示宽度: ASCII 记 1, CJK 与全角记 2, 组合与零宽字符记 0
//
// displayWidth measures the terminal width of a string: ASCII counts 1, CJK and fullwidth glyphs
// count 2, and combining or zero-width characters count 0
func displayWidth(s string) int {
	width := 0
	for _, r := range s {
		width += runeWidth(r)
	}
	return width
}

// runeWidth 返回单个字符的显示宽度
// 组合记号与零宽字符不占宽度, 表意文字与全角形式占两列, 其余占一列
//
// runeWidth returns the display width of one character
// A combining mark or a zero-width character takes no width, an ideograph or a fullwidth form takes
// two columns, and everything else takes one
func runeWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r), unicode.Is(unicode.Cf, r):
		return 0
	case r < 0x20, r >= 0x7F && r < 0xA0:
		// 控制字符不计宽度, 表格数据不应包含它们
		//
		// Control characters take no width, and table data should not carry them
		return 0
	case isWide(r):
		return 2
	}
	return 1
}

// isWide 判断字符是否属于东亚宽度为宽或全角的常见区块, 每个区间后的注释标出该区间含义
// 未覆盖的区块按窄字符记一列, 这是保守选择: 少算宽度只会让表格多留空白, 不会错位
//
// isWide reports whether a character belongs to the common wide or fullwidth blocks of the East
// Asian width table, and the comment after each range names what that range holds
// A block left out counts one column as a narrow character, which is the conservative choice: an
// undercount only leaves extra padding, never a misaligned column
func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo initial sounds
		r >= 0x2E80 && r <= 0x303E, // CJK radicals and symbols
		r >= 0x3041 && r <= 0x33FF, // kana and CJK compatibility symbols
		r >= 0x3400 && r <= 0x4DBF, // CJK extension A
		r >= 0x4E00 && r <= 0x9FFF, // CJK unified ideographs
		r >= 0xA000 && r <= 0xA4CF, // Yi syllables
		r >= 0xAC00 && r <= 0xD7A3, // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF, // CJK compatibility ideographs
		r >= 0xFE30 && r <= 0xFE4F, // CJK compatibility forms
		r >= 0xFF00 && r <= 0xFF60, // fullwidth forms
		r >= 0xFFE0 && r <= 0xFFE6: // fullwidth symbols
		return true
	}
	return false
}

// pad 按显示宽度把文本补到目标列宽
// 文本已经达到或超过目标宽度时原样返回
//
// pad widens a text to the target column width by display width
// A text already at or beyond the target width comes back unchanged
func pad(s string, width int) string {
	gap := width - displayWidth(s)
	if gap <= 0 {
		return s
	}
	return s + strings.Repeat(" ", gap)
}

// newTableWriter 构造一个使用 CJK 宽度设定的 tabwriter
// tabwriter 只接受固定的填充字符宽度, 因此列宽先用 pad 按显示宽度补齐, 这里填充宽度取 1
//
// newTableWriter builds a tabwriter with the CJK width setup
// tabwriter accepts a fixed padding character width only, so pad fills each column to its display
// width beforehand and the padding width here stays 1
func newTableWriter(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 1, tabPadding, ' ', 0)
}

// table 收集表头与数据行, 渲染时按显示宽度对齐
//
// table collects headers and data rows and aligns them by display width when rendering
type table struct {
	headers []string
	rows    [][]string
}

// newTable 构造一个表格
//
// newTable builds one table
func newTable(headers ...string) *table {
	return &table{headers: headers}
}

// addRow 追加一行, 列数与表头不一致时按表头补齐空单元格
//
// addRow appends one row, padding missing cells with empty strings when the column count differs
// from the header
func (tb *table) addRow(cells ...string) {
	if len(cells) < len(tb.headers) {
		padded := make([]string, len(tb.headers))
		copy(padded, cells)
		cells = padded
	}
	tb.rows = append(tb.rows, cells)
}

// render 输出表格: 表头与数据行
// 每列先用 pad 补到该列的最大显示宽度, 再交给 tabwriter; 补齐之后一列的 rune 数与显示宽度
// 相等, tabwriter 的按 rune 计数因此与终端效果一致
// 数据行的最后一列不补宽度, 行尾不会留下多余空白
//
// render writes the table: the header and the data rows
// Every column is padded to its maximum display width before tabwriter sees it; once padded, the
// rune count of a column equals its display width, so the rune-based measurement of tabwriter
// matches what the terminal shows
// The last column of a data row gains no padding, which keeps trailing spaces off the line
func (tb *table) render(w io.Writer) {
	widths := columnWidths(tb)
	tw := newTableWriter(w)
	writeRow(tw, padRow(tb.headers, widths))
	for _, row := range tb.rows {
		writeRow(tw, trimRow(padRow(row, widths)))
	}
	tw.Flush()
}

// trimRow 去掉数据行末尾的空单元格, tabwriter 因此不会为空的最后一列补分隔空白
//
// trimRow drops the trailing empty cells of a data row, which keeps tabwriter from padding a
// separator for an empty last column
func trimRow(cells []string) []string {
	end := len(cells)
	for end > 0 && cells[end-1] == "" {
		end--
	}
	return cells[:end]
}

// padRow 把一行的每个单元格补到目标宽度, 最后一列保持原样
//
// padRow widens every cell of one row to the target width and leaves the last column as it was
func padRow(row []string, widths []int) []string {
	out := make([]string, len(widths))
	for i := range widths {
		cell := ""
		if i < len(row) {
			cell = row[i]
		}
		if i == len(widths)-1 {
			out[i] = cell
			continue
		}
		out[i] = pad(cell, widths[i])
	}
	return out
}

// columnWidths 返回每一列的最大显示宽度, 最后一列不参与
// 最后一列之后没有内容, 因此不需要为它补齐宽度, 这样行尾不会留下多余空白
//
// columnWidths returns the maximum display width of every column, and the last column takes no part
// Nothing follows the last column, so it needs no padding width, which keeps trailing spaces off
// the line
func columnWidths(tb *table) []int {
	widths := make([]int, len(tb.headers))
	if len(widths) == 0 {
		return widths
	}
	measure := func(row []string) {
		for i, cell := range row {
			// 最后一列不参与宽度计算, 它由渲染层直接写到行尾
			//
			// The last column takes no part in the width calculation and is written to the line end
			// by the rendering layer
			if i >= len(widths)-1 {
				break
			}
			if width := displayWidth(cell); width > widths[i] {
				widths[i] = width
			}
		}
	}
	measure(tb.headers)
	for _, row := range tb.rows {
		measure(row)
	}
	return widths
}

// writeRow 写出一行制表符分隔的单元格
//
// writeRow writes one row of tab-separated cells
func writeRow(w io.Writer, cells []string) {
	fmt.Fprintln(w, strings.Join(cells, "\t"))
}

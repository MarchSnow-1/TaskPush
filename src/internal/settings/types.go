package settings

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// 配置标量类型: 放在叶子包供 config 与渠道共用
// 它们只负责把 JSON 文本解析成目标类型, 失败时返回普通 error, 由调用方包装成自己的 Problem
// 因此在 config -> channel 与 channel/* -> settings 之间不会出现环
//
// The configuration scalar types live in this leaf package so config and the channels share them
// They only parse JSON text into the target type and return a plain error on failure, which the
// caller wraps into its own Problem
// That keeps a cycle from forming between config -> channel and channel/* -> settings
// ---------------------------------------------------------------------------

// Duration 是以字符串形式读写的时长
// 只接受 Go 的 time.ParseDuration 写法, 如 "15s"、"1m30s"; 数字形式被拒绝, 因为单位在配置里必须显式
//
// Duration is a time span read and written as a string
// Only the Go time.ParseDuration syntax is accepted, such as "15s" or "1m30s", while a number
// is rejected because the unit has to be explicit in the configuration
type Duration time.Duration

// UnmarshalJSON 解析时长字符串
// 只接受 JSON 字符串, 前后空白先去除; 数字, 布尔与 null 都返回错误
// 空串同样返回错误, 让调用方给出字段路径
//
// UnmarshalJSON parses a duration string
// Only a JSON string is accepted, with surrounding whitespace trimmed first, while a number, a
// boolean and null return an error
// An empty string returns an error as well, which lets the caller supply the field path
func (d *Duration) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("duration must be a string: %w", err)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("duration is empty")
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("duration %q: %w", raw, err)
	}
	*d = Duration(parsed)
	return nil
}

// MarshalJSON 把时长写成字符串, 与 UnmarshalJSON 的输入形式对称
//
// MarshalJSON writes the duration as a string, symmetric with what UnmarshalJSON accepts
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// Duration 返回标准库的时长值
//
// Duration returns the standard-library time span value
func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

// String 返回 time.ParseDuration 形式的文本
//
// String returns the text in time.ParseDuration form
func (d Duration) String() string {
	return time.Duration(d).String()
}

// StringList 是可用数组或分隔字符串给出的字符串列表
//
// StringList is a list of strings given as either a JSON array or a delimited string
type StringList []string

// listSeparators 是分隔字符串里被识别的分隔符
// 半角逗号对应英文写法, 全角逗号与顿号对应中文写法, 三种都可混用
//
// listSeparators holds the separators recognised inside a delimited string
// The ASCII comma covers the English form, while the fullwidth comma and the ideographic comma
// cover the Chinese form, and the three may be mixed
var listSeparators = []string{",", "，", "、"}

// UnmarshalJSON 解析字符串列表
// JSON 数组要求元素都是字符串; JSON 字符串按逗号类分隔符切分, TrimSpace 后丢弃空项
// 数字, 布尔, null 与对象返回错误, 类型判定基于解码后的值而不是文本首字符
//
// UnmarshalJSON parses a string list
// A JSON array requires every element to be a string, while a JSON string is split on the
// comma-like separators, trimmed and stripped of empty items
// A number, a boolean, null and an object return an error, and the type verdict comes from the
// decoded value rather than the first character of the text
func (l *StringList) UnmarshalJSON(data []byte) error {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("string list: %w", err)
	}
	switch val := v.(type) {
	case []any:
		out := make(StringList, 0, len(val))
		for _, item := range val {
			s, ok := item.(string)
			if !ok {
				return fmt.Errorf("string list element %v is %T, want a string", item, item)
			}
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		*l = out
		return nil
	case string:
		*l = splitList(val)
		return nil
	}
	return fmt.Errorf("string list must be an array or a string, got %T", v)
}

// splitList 按逗号类分隔符切分字符串, 去掉首尾空白与空项
//
// splitList splits a string on the comma-like separators, dropping surrounding whitespace and
// empty items
func splitList(raw string) StringList {
	parts := []string{raw}
	for _, sep := range listSeparators {
		next := make([]string, 0, len(parts))
		for _, part := range parts {
			next = append(next, strings.Split(part, sep)...)
		}
		parts = next
	}
	out := make(StringList, 0, len(parts))
	for _, part := range parts {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// MarshalJSON 把列表写成 JSON 数组, 空列表写成 [] 而不是 null
//
// MarshalJSON writes the list as a JSON array, where an empty list becomes [] rather than null
func (l StringList) MarshalJSON() ([]byte, error) {
	if l == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]string(l))
}

// Strings 返回列表的切片形式
// 返回副本让调用方无法改写底层数组
//
// Strings returns the slice form of the list
// The copy keeps a caller from rewriting the underlying array
func (l StringList) Strings() []string {
	out := make([]string, len(l))
	copy(out, l)
	return out
}

// Int 是可以由 JSON 数字或十进制字符串给出的整数
//
// Int is an integer given as either a JSON number or a decimal string
type Int int

// UnmarshalJSON 解析整数
// 接受 JSON 数字 (整数形式) 与字符串; 两种写法统一先 TrimSpace 再走 strconv.Atoi, 因此 " 42 " 可用
// 而 "4.2" 与 "1e3" 不可用, 数字的单位与进位必须显式
// Atoi 按目标平台的 int 位宽判定范围, 因此数字与字符串两条分支在 32 位目标上对同一个超范围值
// 给出一致的错误, 不会像 64 位中转那样静默截断
//
// UnmarshalJSON parses an integer
// A JSON number in integer form and a string are both accepted, and both forms are trimmed before
// strconv.Atoi, so " 42 " works while "4.2" and "1e3" do not, keeping the unit and the base
// explicit
// Atoi judges the range by the target platform's int width, so the number and the string branch
// report one consistent error for the same out-of-range value on a 32-bit target instead of
// truncating it silently the way a 64-bit intermediate does
func (i *Int) UnmarshalJSON(data []byte) error {
	var v any
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("int: %w", err)
	}
	switch val := v.(type) {
	case json.Number:
		return parseIntText(i, val.String())
	case string:
		return parseIntText(i, val)
	}
	return fmt.Errorf("int must be a number or a string, got %T", v)
}

// parseIntText 把一个十进制文本解析成 Int, 两条输入分支共用它以保证判定一致
// 先 TrimSpace 再走 strconv.Atoi, 超出目标平台 int 范围时返回错误而不是截断
//
// parseIntText parses one decimal text into an Int, shared by both input branches so their
// verdicts agree
// It trims first and then runs strconv.Atoi, returning an error above the target platform's int
// range instead of truncating
func parseIntText(i *Int, raw string) error {
	trimmed := strings.TrimSpace(raw)
	parsed, err := strconv.Atoi(trimmed)
	if err != nil {
		return fmt.Errorf("int %q: %w", trimmed, err)
	}
	*i = Int(parsed)
	return nil
}

// MarshalJSON 把整数写成 JSON 数字, 与 UnmarshalJSON 的数字形式对称
//
// MarshalJSON writes the integer as a JSON number, symmetric with the number form
// UnmarshalJSON accepts
func (i Int) MarshalJSON() ([]byte, error) {
	return json.Marshal(int(i))
}

// Int 返回标准库的整数
//
// Int returns the standard-library integer
func (i Int) Int() int {
	return int(i)
}

// Bool 是可以由 JSON 布尔或字符串给出的布尔值
//
// Bool is a boolean given as either a JSON boolean or a string
type Bool bool

// UnmarshalJSON 解析布尔值
// 接受 JSON 布尔与字符串 "true"/"false"/"1"/"0", 字符串比较大小写不敏感并先 TrimSpace
// JSON 数字与其它写法返回错误, 由调用方包装, 这样 "0"/"1" 不会静默变成数字比较
//
// UnmarshalJSON parses a boolean
// A JSON boolean and the strings "true", "false", "1" and "0" are accepted, with string forms
// compared case-insensitively and trimmed first
// A JSON number and any other form return an error for the caller to wrap, which keeps "0" and
// "1" from silently becoming numeric comparisons
func (b *Bool) UnmarshalJSON(data []byte) error {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("bool: %w", err)
	}
	switch val := v.(type) {
	case bool:
		*b = Bool(val)
		return nil
	case string:
		switch strings.ToLower(strings.TrimSpace(val)) {
		case "true", "1":
			*b = true
			return nil
		case "false", "0":
			*b = false
			return nil
		}
		return fmt.Errorf("bool %q: want true, false, 1 or 0", val)
	}
	return fmt.Errorf("bool must be a boolean or a string, got %T", v)
}

// MarshalJSON 把布尔值写成 JSON 布尔
//
// MarshalJSON writes the boolean as a JSON boolean
func (b Bool) MarshalJSON() ([]byte, error) {
	return json.Marshal(bool(b))
}

// Bool 返回标准库的布尔值
//
// Bool returns the standard-library boolean
func (b Bool) Bool() bool {
	return bool(b)
}

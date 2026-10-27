package settings

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

// decodeInto 是测试的公共入口: 把 JSON 文本解码进 target
//
// decodeInto is the shared test entry point decoding JSON text into target
func decodeInto(t *testing.T, raw string, target any) {
	t.Helper()
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		t.Fatalf("Unmarshal(%s) = %v, want nil", raw, err)
	}
}

// TestDuration_Unmarshal 覆盖合法字符串、空白与各种单位写法
//
// TestDuration_Unmarshal covers valid strings, whitespace and the different unit forms
func TestDuration_Unmarshal(t *testing.T) {
	cases := []struct {
		raw  string
		want time.Duration
	}{
		{raw: `"15s"`, want: 15 * time.Second},
		{raw: `"1m30s"`, want: 90 * time.Second},
		{raw: `"0s"`, want: 0},
		{raw: `"1500ms"`, want: 1500 * time.Millisecond},
		{raw: `"1h"`, want: time.Hour},
		{raw: `" 15s "`, want: 15 * time.Second},
		{raw: `"-5s"`, want: -5 * time.Second},
	}
	for _, tc := range cases {
		var d Duration
		decodeInto(t, tc.raw, &d)
		if got := d.Duration(); got != tc.want {
			t.Fatalf("Unmarshal(%s) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

// TestDuration_Unmarshal_Rejects 覆盖数字、布尔、null、空串与非法写法的拒绝
//
// TestDuration_Unmarshal_Rejects covers a number, a boolean, null, an empty string and invalid
// syntax being rejected
func TestDuration_Unmarshal_Rejects(t *testing.T) {
	cases := []string{`15`, `15.5`, `true`, `null`, `""`, `"  "`, `"15"`, `"seconds"`}
	for _, raw := range cases {
		var d Duration
		if err := json.Unmarshal([]byte(raw), &d); err == nil {
			t.Fatalf("Unmarshal(%s) = nil, want an error", raw)
		}
	}
}

// TestDuration_Marshal 覆盖输出为字符串并保持与输入形式对称
//
// TestDuration_Marshal covers the string output staying symmetric with the accepted input form
func TestDuration_Marshal(t *testing.T) {
	cases := []struct {
		d    Duration
		want string
	}{
		{d: Duration(15 * time.Second), want: `"15s"`},
		{d: Duration(0), want: `"0s"`},
		{d: Duration(90 * time.Second), want: `"1m30s"`},
	}
	for _, tc := range cases {
		data, err := json.Marshal(tc.d)
		if err != nil {
			t.Fatalf("Marshal(%v): %v", tc.d, err)
		}
		if string(data) != tc.want {
			t.Fatalf("Marshal(%v) = %s, want %s", tc.d, data, tc.want)
		}
	}

	// 结构体里的 round-trip: 编码结果再解码应得到同一个时长
	// A round-trip inside a struct: decoding the encoded result yields the same span again
	type payload struct {
		Timeout Duration `json:"timeout"`
	}
	var p payload
	decodeInto(t, `{"timeout":"2m"}`, &p)
	encoded, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Contains(encoded, []byte(`"timeout":"2m0s"`)) {
		t.Fatalf("encoded = %s, want the timeout as a string", encoded)
	}
}

// TestDuration_String 覆盖 String 与标准库格式一致
//
// TestDuration_String covers String matching the standard-library format
func TestDuration_String(t *testing.T) {
	d := Duration(1500 * time.Millisecond)
	if got := d.String(); got != "1.5s" {
		t.Fatalf("String() = %q, want 1.5s", got)
	}
}

// TestStringList_Array 覆盖数组形式、TrimSpace 与空项丢弃
//
// TestStringList_Array covers the array form with trimming and empty items dropped
func TestStringList_Array(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{raw: `["a","b"]`, want: []string{"a", "b"}},
		{raw: `[" a ", " b "]`, want: []string{"a", "b"}},
		{raw: `["a","","b"]`, want: []string{"a", "b"}},
		{raw: `["  "]`, want: []string{}},
		{raw: `[]`, want: []string{}},
	}
	for _, tc := range cases {
		var l StringList
		decodeInto(t, tc.raw, &l)
		got := l.Strings()
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Fatalf("Unmarshal(%s) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

// TestStringList_String 覆盖分隔字符串形式与三种分隔符混用
//
// TestStringList_String covers the delimited string form and the three separators being mixed
func TestStringList_String(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{raw: `"a,b"`, want: []string{"a", "b"}},
		{raw: `"a，b"`, want: []string{"a", "b"}},
		{raw: `"a、b"`, want: []string{"a", "b"}},
		{raw: `"a, b， c 、d"`, want: []string{"a", "b", "c", "d"}},
		{raw: `" a , , b "`, want: []string{"a", "b"}},
		{raw: `""`, want: []string{}},
		{raw: `"   "`, want: []string{}},
		{raw: `","`, want: []string{}},
	}
	for _, tc := range cases {
		var l StringList
		decodeInto(t, tc.raw, &l)
		got := l.Strings()
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Fatalf("Unmarshal(%s) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

// TestStringList_Rejects 覆盖数字元素、数字标量与对象的拒绝
//
// TestStringList_Rejects covers a numeric element, a numeric scalar and an object being rejected
func TestStringList_Rejects(t *testing.T) {
	cases := []string{`[1,2]`, `["a",2]`, `1`, `{"a":"b"}`, `null`, `true`}
	for _, raw := range cases {
		var l StringList
		if err := json.Unmarshal([]byte(raw), &l); err == nil {
			t.Fatalf("Unmarshal(%s) = nil, want an error", raw)
		}
	}
}

// TestStringList_Marshal 覆盖输出为 JSON 数组且空列表不是 null
//
// TestStringList_Marshal covers the JSON-array output with an empty list staying an array
func TestStringList_Marshal(t *testing.T) {
	data, err := json.Marshal(StringList{"a", "b"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(data) != `["a","b"]` {
		t.Fatalf("Marshal = %s, want [\"a\",\"b\"]", data)
	}
	data, err = json.Marshal(StringList{})
	if err != nil {
		t.Fatalf("Marshal(empty): %v", err)
	}
	if string(data) != `[]` {
		t.Fatalf("Marshal(empty) = %s, want []", data)
	}
	data, err = json.Marshal(StringList(nil))
	if err != nil {
		t.Fatalf("Marshal(nil): %v", err)
	}
	if string(data) != `[]` {
		t.Fatalf("Marshal(nil) = %s, want []", data)
	}
}

// TestStringList_StringsCopy 覆盖 Strings 返回副本
//
// TestStringList_StringsCopy covers Strings returning a copy
func TestStringList_StringsCopy(t *testing.T) {
	l := StringList{"a", "b"}
	got := l.Strings()
	got[0] = "example-mutated"
	if l[0] != "a" {
		t.Fatal("Strings() exposed the underlying array to callers")
	}
}

// TestInt 覆盖数字与字符串两种输入及边界
//
// TestInt covers both the number and the string input forms along with the edges
func TestInt(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{raw: `42`, want: 42},
		{raw: `"42"`, want: 42},
		{raw: `" 42 "`, want: 42},
		{raw: `0`, want: 0},
		{raw: `"0"`, want: 0},
		{raw: `-7`, want: -7},
		{raw: `"-7"`, want: -7},
	}
	for _, tc := range cases {
		var i Int
		decodeInto(t, tc.raw, &i)
		if got := i.Int(); got != tc.want {
			t.Fatalf("Unmarshal(%s) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

// TestInt_Rejects 覆盖小数、指数、非数字字符串与其它 JSON 类型的拒绝
//
// TestInt_Rejects covers a decimal, an exponent, a non-numeric string and other JSON types
// being rejected
func TestInt_Rejects(t *testing.T) {
	cases := []string{`4.2`, `"4.2"`, `1e3`, `"1e3"`, `"abc"`, `""`, `true`, `null`, `[]`, `{"a":1}`}
	for _, raw := range cases {
		var i Int
		if err := json.Unmarshal([]byte(raw), &i); err == nil {
			t.Fatalf("Unmarshal(%s) = nil, want an error", raw)
		}
	}
}

// TestInt_RangeMatchesAcrossForms 覆盖数字与字符串两种写法对同一个超范围值给出同样的结论
// 目标平台的 int 位宽决定边界: 能表示的最大值必须成功, 超出它的值在两条分支都必须报错
// 这条用例防止数字分支退回 64 位中转而在 32 位目标上静默截断
//
// TestInt_RangeMatchesAcrossForms covers the number and the string form reaching the same verdict
// for one out-of-range value
// The target platform's int width sets the boundary: the largest representable value has to
// succeed, while a value above it has to fail in both branches
// The case keeps the number branch from falling back to a 64-bit intermediate and truncating
// silently on a 32-bit target
func TestInt_RangeMatchesAcrossForms(t *testing.T) {
	// 本平台 int 能表示的最大值, 两种写法都必须成功
	// The largest value the platform int represents, which both forms have to accept
	maxIntText := strconv.Itoa(math.MaxInt)
	for _, raw := range []string{maxIntText, `"` + maxIntText + `"`} {
		var i Int
		decodeInto(t, raw, &i)
		if got := i.Int(); got != math.MaxInt {
			t.Fatalf("Unmarshal(%s) = %d, want %d", raw, got, math.MaxInt)
		}
	}

	// 超出 int 范围的写法在两条分支都必须报错, 并且错误形态一致
	// 候选值里按目标平台的 int 位宽挑出真正放不下的那些, 64 位平台上 int64 上限本身是合法的
	//
	// Out-of-range forms have to fail in both branches with one consistent error shape
	// The candidates that truly do not fit are picked by the target platform's int width, since
	// the int64 maximum itself is valid on a 64-bit platform
	candidates := []string{`99999999999999999999`, `9223372036854775808`, `4294967296`, `2147483648`}
	overflow := 0
	for _, number := range candidates {
		if _, err := strconv.Atoi(number); err == nil {
			continue
		}
		overflow++
		var fromNumber, fromString Int
		numberErr := json.Unmarshal([]byte(number), &fromNumber)
		stringErr := json.Unmarshal([]byte(`"`+number+`"`), &fromString)
		if numberErr == nil || stringErr == nil {
			t.Fatalf("Unmarshal(%s) = %v and its string form = %v, want both to fail on a %d-bit int",
				number, numberErr, stringErr, strconv.IntSize)
		}
		if fromNumber != 0 || fromString != 0 {
			t.Fatalf("Unmarshal(%s) = %d and its string form = %d, want the zero value kept",
				number, fromNumber, fromString)
		}
		wantPrefix := `int "` + number + `": `
		if !strings.HasPrefix(numberErr.Error(), wantPrefix) {
			t.Fatalf("number error = %q, want it to start with %q", numberErr.Error(), wantPrefix)
		}
		if !strings.HasPrefix(stringErr.Error(), wantPrefix) {
			t.Fatalf("string error = %q, want it to start with %q", stringErr.Error(), wantPrefix)
		}
		if numberErr.Error() != stringErr.Error() {
			t.Fatalf("number error = %q, string error = %q, want one identical message",
				numberErr.Error(), stringErr.Error())
		}
	}
	if overflow == 0 {
		t.Fatalf("no candidate exceeds the %d-bit int, so the range check was never exercised", strconv.IntSize)
	}
}

// TestInt_Marshal 覆盖输出为 JSON 数字
//
// TestInt_Marshal covers the JSON-number output
func TestInt_Marshal(t *testing.T) {
	data, err := json.Marshal(Int(42))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(data) != "42" {
		t.Fatalf("Marshal = %s, want 42", data)
	}
}

// TestBool 覆盖布尔与四种字符串写法
//
// TestBool covers the boolean form and the four string forms
func TestBool(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{raw: `true`, want: true},
		{raw: `false`, want: false},
		{raw: `"true"`, want: true},
		{raw: `"False"`, want: false},
		{raw: `"TRUE"`, want: true},
		{raw: `"1"`, want: true},
		{raw: `"0"`, want: false},
		{raw: `" true "`, want: true},
	}
	for _, tc := range cases {
		var b Bool
		decodeInto(t, tc.raw, &b)
		if got := b.Bool(); got != tc.want {
			t.Fatalf("Unmarshal(%s) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

// TestBool_Rejects 覆盖数字与非法字符串的拒绝
//
// TestBool_Rejects covers a number and an invalid string being rejected
func TestBool_Rejects(t *testing.T) {
	cases := []string{`1`, `0`, `"2"`, `"on"`, `" yes "`, `""`, `null`, `[]`}
	for _, raw := range cases {
		var b Bool
		if err := json.Unmarshal([]byte(raw), &b); err == nil {
			t.Fatalf("Unmarshal(%s) = nil, want an error", raw)
		}
	}
}

// TestBool_Marshal 覆盖输出为 JSON 布尔
//
// TestBool_Marshal covers the JSON-boolean output
func TestBool_Marshal(t *testing.T) {
	data, err := json.Marshal(Bool(true))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(data) != "true" {
		t.Fatalf("Marshal = %s, want true", data)
	}
}

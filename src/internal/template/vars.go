package template

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// 变量来源合并: 内置变量 → JSON 文件/stdin → CLI key=value
// 优先级: CLI 显式赋值 > 文件/stdin > 内置默认
//
// How the variable sources are merged: built-ins, then a JSON file or stdin, then CLI key=value pairs
// Priority: an explicit CLI assignment outranks the file or stdin, which outranks the built-in default
// ---------------------------------------------------------------------------

// BuildVars 构造内置时间变量.startMs 非 0 时额外生成 elapsed 计时变量 (start→post 计时模式)
// BuildVars builds the built-in time variables, and a non-zero startMs adds the elapsed timing variables used by the start-to-post mode
func BuildVars(now time.Time, startMs int64) *Vars {
	v := NewVars()
	v.Set("time", now.Format("15:04:05"))
	v.Set("date", now.Format("2006-01-02"))
	v.Set("datetime", now.Format("2006-01-02 15:04:05"))
	if startMs > 0 {
		elapsed := now.UnixMilli() - startMs
		if elapsed < 0 {
			elapsed = 0
		}
		// 数值变量存数值类型: {{if elapsed_ms}} 在 0 时为假 (与 README 真值语义一致)
		// The numeric variables hold a numeric type, so {{if elapsed_ms}} reads as false at 0, matching the truthiness semantics in the README
		v.Set("elapsed_ms", elapsed)
		v.Set("elapsed_s", float64(elapsed)/1000.0)
		// elapsed 是人类可读文本 ("2m 30s"), 保持字符串
		// elapsed stays a string, since it is human-readable text such as "2m 30s"
		v.Set("elapsed", formatElapsed(elapsed))
	}
	return v
}

// formatElapsed 把毫秒耗时格式化为可读文本 (如 "500ms" / "12s" / "2m 30s" / "1h 5m 12s")
// formatElapsed formats a duration in milliseconds as readable text, such as "500ms", "12s", "2m 30s" or "1h 5m 12s"
func formatElapsed(ms int64) string {
	if ms < 1000 {
		return strconv.FormatInt(ms, 10) + "ms" // 亚秒显示毫秒, 避免与 elapsed_s 并存误导 / below one second the value stays in milliseconds, which keeps it from being mistaken for the seconds in elapsed_s
	}
	totalSeconds := ms / 1000
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60
	var parts []string
	if hours > 0 {
		parts = append(parts, strconv.FormatInt(hours, 10)+"h")
	}
	if minutes > 0 {
		parts = append(parts, strconv.FormatInt(minutes, 10)+"m")
	}
	if seconds > 0 || len(parts) == 0 {
		parts = append(parts, strconv.FormatInt(seconds, 10)+"s")
	}
	return strings.Join(parts, " ")
}

// maxVarsFileBytes vars 文件/stdin 读取上限 (1MB), 防止意外超大输入
// maxVarsFileBytes caps a vars file or stdin read at 1MB, which guards against an accidentally huge input
const maxVarsFileBytes = 1 << 20

// LoadVarsFile 从 JSON 文件 (或 "-" 即 stdin) 读取顶层对象键合并进 vars
// 根必须为 JSON 对象; 数组/标量根报错.输入上限 1MB——文件与 stdin 一视同仁,
// 都是边读边限流, 不会先把超大文件整个读进内存再拒绝
//
// LoadVarsFile reads a JSON file, or "-" for stdin, and merges its top-level object keys into vars
// The root has to be a JSON object, and an array or scalar root is an error
// The 1MB input cap applies to a file and to stdin alike, and both are read through a limited reader, so
// an oversized input is not read into memory in full before being rejected
func LoadVarsFile(path string, vars *Vars) error {
	var r io.Reader
	if path == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("read vars %s: %w", path, err)
		}
		defer f.Close()
		r = f
	}
	data, err := io.ReadAll(io.LimitReader(r, maxVarsFileBytes+1))
	if err != nil {
		return fmt.Errorf("read vars %s: %w", path, err)
	}
	if len(data) > maxVarsFileBytes {
		return fmt.Errorf("vars 输入超过 1MB 上限")
	}
	return MergeVarsJSON(data, vars)
}

// MergeVarsJSON 解析 JSON 并合并进 vars(根必须为 JSON 对象; 非对象根报错)
// 数字用 json.Number 保留原始字面量: 大整数 (>2^53) 不经 float64 丢精度,
// 与 CLI --var 的保真承诺一致
//
// MergeVarsJSON parses JSON and merges it into vars, and the root has to be a JSON object, since a non-object root is an error
// Numbers use json.Number to keep the original literal, so a large integer above 2^53 does not lose precision through float64,
// which matches the fidelity promise of the CLI --var
func MergeVarsJSON(data []byte, vars *Vars) error {
	data = bytes.TrimSpace(data)
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM(Windows 记事本等保存的 JSON) / a UTF-8 BOM, such as the one Windows Notepad leaves on a saved JSON file
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var root map[string]any
	if err := dec.Decode(&root); err != nil {
		return fmt.Errorf("parse vars json: %w", err)
	}
	// 顶层只接受一个 JSON 对象: 尾随内容 (如 {"a":1}{"b":2}) 显式报错而不是静默丢弃
	// Only one JSON object is accepted at the top level, so trailing content such as {"a":1}{"b":2} is an explicit error rather than being dropped silently
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("parse vars json: 顶层存在多余内容 (只接受一个 JSON 对象)")
	}
	for k, val := range root {
		vars.Set(k, val)
	}
	return nil
}

// SetCLIVars 解析 CLI 传入的 key=value(可重复), 写入 vars
// 值做保真类型推断: "true"/"false" → bool, 无前导零整数 → int64, 小数 → float64,
// 其余 (前导零编号/超精度) 保持字符串; {{if v}} 真值语义与 JSON 源一致
//
// SetCLIVars parses the repeatable key=value pairs the CLI passes in and writes them into vars
// Values go through fidelity-preserving type inference: "true"/"false" become a bool, an integer without a
// leading zero becomes an int64 and a decimal becomes a float64, while everything else, such as a leading-zero
// code or an over-precise number, stays a string; {{if v}} reads the same as a value that came from JSON
func SetCLIVars(pairs []string, vars *Vars) error {
	for _, pair := range pairs {
		key, val, ok := strings.Cut(pair, "=")
		if !ok {
			return fmt.Errorf("invalid var %q (expect key=value)", pair)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return fmt.Errorf("invalid var %q (empty key)", pair)
		}
		vars.Set(key, inferScalar(val))
	}
	return nil
}

// ---------------------------------------------------------------------------
// 变量解析与保留名辅助
// 引擎不读取环境变量, 变量值也不递归渲染
//
// Variable resolution and reserved-name helpers
// The engine reads no environment variable, and it never re-renders variable values
// ---------------------------------------------------------------------------

// ReservedVars 保留变量名: title/body 由调度层以 Literal 写入, 其余为内置时间与计时变量
// env 同样保留以占住命名空间; 这些名字不允许通过 --var/变量文件定义, 拒绝逻辑在 notify
//
// ReservedVars lists the reserved names: title and body are written by the scheduling layer as
// a Literal, while the rest are the built-in time and elapsed variables
// env stays reserved as well, which occupies the namespace; these names cannot be defined through
// --var or a vars file, and the rejection itself lives in notify
var ReservedVars = []string{
	"title", "body",
	"time", "date", "datetime",
	"elapsed", "elapsed_ms", "elapsed_s",
	"env",
}

// IsReservedVar 报告 name 是否为保留名: 精确命中保留列表, 或位于 env 命名空间
// (name 为 env, 或以 "env." 开头, 如 env.FOO)
//
// IsReservedVar reports whether name is a reserved name: an exact hit on the reserved list, or a
// member of the env namespace, meaning name is env itself or starts with "env.", such as env.FOO
func IsReservedVar(name string) bool {
	for _, r := range ReservedVars {
		if name == r {
			return true
		}
	}
	return name == "env" || strings.HasPrefix(name, "env.")
}

// resolve 解析模板路径: 整键优先 (允许含点的键, 如 --var a.b=1), 未命中再按点路径逐级访问
// 与 Vars.Resolve 的实现相同, 区别只是可见性: 渲染路径走这个内部入口, 对外断言走 Resolve
//
// resolve resolves a template path: a whole key wins, which allows keys containing dots such as
// --var a.b=1, and a miss then walks the dotted path one segment at a time
// It matches Vars.Resolve and differs in visibility only: the render path uses this internal entry
// while an outside assertion uses Resolve
func (v *Vars) resolve(path string) (any, bool) {
	if v == nil || path == "" {
		return nil, false
	}
	if val, ok := v.flat[path]; ok {
		return val, true
	}
	parts := strings.Split(path, ".")
	cur, ok := v.flat[parts[0]]
	if !ok {
		return nil, false
	}
	return resolvePath(cur, parts[1:])
}

// resolveExpr 在 resolve 之上支持 range 体内的相对形式:
// {{.}} 当前项, {{.field}} 当前项字段, {{@index}} 从 0 起的索引
// 相对形式只在 range 体内生效, 体外按普通路径解析 (通常未解析)
//
// resolveExpr adds the relative forms inside a range body on top of resolve:
// {{.}} for the current item, {{.field}} for one of its fields and {{@index}} for the index starting at 0
// A relative form binds only inside a range body, while outside it resolves as an ordinary path, usually unresolved
func (v *Vars) resolveExpr(path string, cur *loopCtx) (any, bool) {
	if cur != nil {
		switch {
		case path == "@index":
			return cur.index, true
		case path == ".":
			return cur.item, true
		case strings.HasPrefix(path, "."):
			rest := path[1:]
			if rest == "" {
				return cur.item, true
			}
			return resolvePath(cur.item, strings.Split(rest, "."))
		}
	}
	return v.resolve(path)
}

// inferScalar 对 CLI 字符串值做标量类型推断 (保真优先: 只有渲染结果与原文一致时才转成数值类型):
//   - "true"/"false" → bool
//   - 无前导零/正号且可无损解析为 int64 的整数 → int64
//   - 小数/指数形式且渲染 round-trip 与原串一致 → float64
//   - 其余 (前导零编号, 正号, 尾零小数如 "1.50", NaN/Inf, 超精度整数) 保持字符串
//
// inferScalar infers the scalar type of a CLI string value, preferring fidelity: the value becomes a numeric
// type only when rendering it reproduces the original text
//   - "true" and "false" become a bool
//   - an integer without a leading zero or a plus sign that parses losslessly as an int64 becomes an int64
//   - a decimal or exponent form whose rendering round-trips to the original string becomes a float64
//   - everything else stays a string: a leading-zero code, a plus sign, a trailing-zero decimal such as
//     "1.50", NaN, Inf and an integer beyond the int64 range
func inferScalar(val string) any {
	if val == "true" || val == "false" {
		return val == "true"
	}
	// 正号 ("+5") 不是 JSON/常规数字写法: 保持字符串, 避免改写为 "5"
	// A leading plus sign such as "+5" is not how JSON or ordinary numbers are written, so the value stays a string rather than being rewritten to "5"
	if strings.HasPrefix(val, "+") {
		return val
	}
	// 整数: 允许可选负号; 禁止前导零 ("007"/"00123" 保持字符串)
	// Integer: an optional minus sign is allowed, while a leading zero is not, so "007" and "00123" stay strings
	s := strings.TrimPrefix(val, "-")
	isInt := s != ""
	for _, r := range s {
		if r < '0' || r > '9' {
			isInt = false
			break
		}
	}
	if isInt && !(len(s) > 1 && s[0] == '0') {
		// 与小数分支同样的 round-trip 校验: 只有渲染能还原原文才转 int64
		// 否则 "-0" 会被改写成 "0", 与"保持原样"的保真承诺相违
		//
		// The same round-trip check the decimal branch applies: the value becomes an int64 only when rendering
		// can reproduce the original text
		// Otherwise "-0" would be rewritten to "0", which contradicts the "keep it as written" fidelity promise
		if n, err := strconv.ParseInt(val, 10, 64); err == nil && strconv.FormatInt(n, 10) == val {
			return n
		}
		return val // 超 int64 范围或无法还原原文: 保持字符串 / beyond the int64 range or unable to reproduce the original text, so the value stays a string
	}
	// 小数/指数: 仅当 float64 渲染能精确还原原串才转数值;
	// NaN/±Inf 是 ParseFloat 接受但非真实数值的输入, 这条分支保持字符串
	//
	// Decimal and exponent forms become a number only when rendering the float64 reproduces the original string exactly
	// NaN and ±Inf are inputs ParseFloat accepts but are not real numbers, so this branch keeps them as strings
	if !isInt {
		if f, err := strconv.ParseFloat(val, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			if strconv.FormatFloat(f, 'f', -1, 64) == val {
				return f
			}
		}
	}
	return val
}

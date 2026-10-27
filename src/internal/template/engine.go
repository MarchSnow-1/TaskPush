package template

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// 模板引擎共享层: 变量容器 Vars, 节点类型 Node, 真值/渲染语义, 以及解析渲染共用的常量
// {{ }} 的解析与渲染在 render.go; 本文件只提供变量容器与值语义, 不含渲染入口
//
// 支持的写法:
//   {{name}}              → 变量值
//   {{name|default:...}}  → 变量缺失或为假值 (空串/0/空列表/false/nil) 时输出 default 文本
//   {{if expr}}...{{else}}...{{end}}  条件 (expr 为空值=假)
//   {{range expr}}...{{end}}          遍历列表; 体内 {{.}}=当前项 {{@index}}=序号 (0起)
//   点路径 {{a.b.0}} 可访问 JSON 结构变量
//
// {{env.KEY}} 不属于模板语法: 模板不读取环境变量, 该形态按未解析变量处理
//
// The shared layer of the template engine: the Vars container, the Node types, the truthiness and
// rendering semantics, and the constants the parser and renderer share
// Parsing and rendering of {{ }} live in render.go, and this file carries the variable container and
// the value semantics with no render entry
//
// Supported forms:
//   {{name}}              -> the variable value
//   {{name|default:...}}  -> the default text when the variable is missing or falsy (empty string / 0 / empty list / false / nil)
//   {{if expr}}...{{else}}...{{end}}  a condition, where an empty value counts as false
//   {{range expr}}...{{end}}          iterates a list; inside the body {{.}} is the current item and {{@index}} is its index starting at 0
//   Dotted paths such as {{a.b.0}} reach into JSON structure variables
//
// {{env.KEY}} is not part of the template grammar: a template reads no environment variable, and that
// form counts as an unresolved variable
// ---------------------------------------------------------------------------

const (
	// maxTemplateLen 渲染输出上限 (64KB).超限视为内容损坏, 渲染入口按模式报错或告警, 不得静默发送
	//
	// maxTemplateLen caps the rendered output at 64KB, and anything past that counts as damaged content
	// The render entries report an error or a warning according to the mode rather than sending silently
	maxTemplateLen = 64 * 1024
	// maxTemplateInputLen 模板输入上限: 与输出上限同源, 避免两份常量漂移
	// maxTemplateInputLen caps the template input and takes its value from the output cap, which keeps the two constants from drifting apart
	maxTemplateInputLen = maxTemplateLen
	// maxParseNesting if/range 字面量嵌套深度上限 (防恶意深嵌套致栈溢出)
	// maxParseNesting caps the nesting depth of literal if/range blocks, which bounds the recursion a maliciously deep template can drive
	maxParseNesting = 100
)

// Node 是模板 AST 节点
// Node is one template AST node
type Node interface{}

type textNode struct{ text string }

type varNode struct {
	path   string
	def    string // 缺省文本 (字面) / the default text, taken literally
	hasDef bool
	orig   string // {{ }} 内的原文 (未解析时按原文写回, 保留用户写的空白) / the raw text inside {{ }}, written back as it stands when the variable is unresolved so the whitespace the user typed survives
}

type ifNode struct {
	path string
	then []Node
	els  []Node
}

type rangeNode struct {
	path string
	body []Node
}

// ---------------------------------------------------------------------------
// 变量容器与解析
//
// The variable container and how variables are resolved
// ---------------------------------------------------------------------------

// Vars 提供变量解析: 内置/CLI/文件/stdin 合并值 + 点路径/切片访问
// 不读取环境变量
//
// Vars resolves variables: values merged from built-ins, the CLI, a file and stdin, plus dotted-path and
// slice access
// It reads no environment variable
type Vars struct {
	flat map[string]any
}

func NewVars() *Vars { return &Vars{flat: map[string]any{}} }

func (v *Vars) Set(key string, val any) { v.flat[key] = val }

// Resolve 解析模板路径 name, 解析顺序: 整键精确命中 (允许含点的键, 如 --var a.b=1 以键 "a.b" 存储),
// 未命中再按点路径逐级访问 ({{a.b.0}}); 找不到返回 nil,false
//
// Resolve 是对外查询入口: notify 与 cli 的测试用它在收到的 Message 上断言变量集合,
// 渲染路径走 resolve 系列
//
// Resolve resolves the template path name in two steps: an exact whole-key hit, which allows keys
// containing dots such as --var a.b=1 stored under the key "a.b", and otherwise a walk of the dotted path
// one segment at a time, as in {{a.b.0}}
// A miss returns nil,false
//
// Resolve is the public lookup entry: the notify and cli tests use it to assert the variable set on a
// received Message, while the render path uses the resolve functions
func (v *Vars) Resolve(name string) (any, bool) {
	if v == nil || name == "" {
		return nil, false
	}
	// 整键优先 (允许含点的键名)
	// A whole key wins, which allows key names containing dots
	if val, ok := v.flat[name]; ok {
		return val, true
	}
	parts := strings.Split(name, ".")
	cur, ok := v.flat[parts[0]]
	if !ok {
		return nil, false
	}
	return resolvePath(cur, parts[1:])
}

// resolvePath 沿路径段逐级访问 map[string]any / []any
// resolvePath walks map[string]any and []any values one path segment at a time
func resolvePath(cur any, parts []string) (any, bool) {
	for _, p := range parts {
		switch t := cur.(type) {
		case map[string]any:
			var ok bool
			cur, ok = t[p]
			if !ok {
				return nil, false
			}
		case []any:
			idx, err := strconv.Atoi(p)
			if err != nil || idx < 0 || idx >= len(t) {
				return nil, false
			}
			cur = t[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}

// Literal 标记"已经是最终内容, 不应再当模板解析"的字符串值
//
// 调度层把 CLI 传入并渲染完成的 title/body 标成 Literal: 这两个值可能合法地包含
// 字面 "{{...}}"(CLI 侧引用保留变量时按字面保留并告警), 标成 Literal 之后渠道模板
// 引用它只得到最终文本, 不会把里面的 {{body}} 当成待展开的标签
// 引擎本身不递归渲染变量值, 因此 Literal 的作用是显式声明"这是内容而不是模板"
//
// Literal marks a string value that is already final content, so it must not be parsed as a template
// The scheduling layer marks the title and body that came in through the CLI and were rendered as Literal,
// because those two values may legitimately contain a literal "{{...}}" (the CLI side keeps a reserved
// variable as written and warns about it), and marking them lets a channel template referencing them
// receive the final text instead of treating the {{body}} inside as a tag to expand
// The engine never re-renders a variable value, so a Literal states explicitly that this is content
// rather than a template
type Literal string

// renderValue 把任意变量值渲染为字符串; 列表用 "," 连接; 对象渲染为 JSON
// renderValue renders any variable value as a string, joining a list with "," and rendering an object as JSON
func renderValue(val any) string {
	switch t := val.(type) {
	case nil:
		return ""
	case string:
		return t
	case Literal:
		return string(t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	case json.Number:
		return t.String() // 保真: 原样输出 JSON 数字字面量 / fidelity: the JSON number literal is emitted exactly as it was written
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			parts = append(parts, renderValue(e))
		}
		return strings.Join(parts, ",")
	case []string:
		// 与 []any 对齐: 生产代码只用 []any/JSON 数组, 这条分支是给 Go 调用方 Set 字符串切片留的,
		// 输出与 JSON 数组来源一致, 逗号分隔且不带 Go 风格的方括号
		//
		// Aligned with []any: production code uses only []any and JSON arrays, and this branch exists
		// for Go callers that Set a string slice, rendering comma-separated without the Go-style brackets
		return strings.Join(t, ",")
	case map[string]any:
		b, _ := json.Marshal(t)
		return string(b)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// truthy 判断变量值真值.为假: nil / false / 空字符串 / 空列表 / 空对象 / 数值 0;
// 其余 (含未知类型) 为真.与 README「真值语义」一致
//
// truthy decides whether a variable value counts as true: nil, false, an empty string, an empty list,
// an empty object and the number 0 are false, and everything else, unknown types included, is true
// This follows the truthiness semantics documented in the README
func truthy(val any) bool {
	switch t := val.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case Literal:
		return t != ""
	case []any:
		return len(t) > 0
	case []string:
		// 与 []any 对齐, 为 Go 调用方 Set 字符串切片保留: 空切片判假, 与 JSON 数组来源的空切片一致
		//
		// Aligned with []any and kept for Go callers that Set a string slice: an empty slice reads as
		// false, matching an empty slice that came from a JSON array
		return len(t) > 0
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i != 0
		}
		if f, err := t.Float64(); err == nil {
			return f != 0
		}
		return t.String() != ""
	case float64:
		return t != 0
	case int:
		return t != 0
	case int64:
		return t != 0
	case map[string]any:
		return len(t) > 0
	default:
		return true
	}
}

// isNumericLiteral 判断表达式是否为纯数字字面量 (如 if 表达式里的 "1"/"0")
// 数字不是合法变量名起点语义, 纯数字作 if/range 条件是无意义的静默判假源, 需显式报错
//
// isNumericLiteral reports whether an expression is a pure numeric literal, such as "1" or "0" in an if expression
// A number is not a legal variable name, and a pure number as an if/range condition is a pointless source of
// a silent false, so this case needs an explicit error
func isNumericLiteral(expr string) bool {
	if expr == "" {
		return false
	}
	for _, r := range expr {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// toSlice 把变量值规整为可遍历列表: 已是列表原样; 普通标量→单元素
// toSlice normalises a variable value into a traversable list: a list is used as it stands and an ordinary scalar becomes a single element
func toSlice(val any) []any {
	switch t := val.(type) {
	case []any:
		return t
	case []string:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = s
		}
		return out
	case nil:
		return nil
	default:
		return []any{t}
	}
}

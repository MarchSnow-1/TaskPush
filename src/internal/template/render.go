package template

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
)

// ---------------------------------------------------------------------------
// 渲染入口: RenderWithMode / RenderJSONLeaf, 以及它们共用的解析器
//
// 语义约束:
//   - 不读取环境变量, {{env.X}} 按未解析变量处理;
//   - 变量值不递归渲染, 值里的 {{...}} 按字面输出;
//   - 严格模式输出位置未解析即报错, check 模式保留原文并告警;
//   - 不符合语法的标签报错, 不按字面静默保留;
//   - range 的缺失变量输出空, 不把标签写回输出;
//   - 支持 \{{ 转义
//
// The render entries, RenderWithMode and RenderJSONLeaf, together with the parser they share
//
// Semantic constraints:
//   - no environment access, so {{env.X}} counts as an unresolved variable;
//   - variable values are never re-rendered, and {{...}} inside a value stays literal;
//   - strict mode reports an unresolved variable at an output position as an error, while check
//     mode keeps the text as written and warns;
//   - a tag outside the grammar is an error rather than a silently preserved literal;
//   - a missing range variable renders as empty rather than writing its tag back;
//   - the \{{ escape is supported
// ---------------------------------------------------------------------------

// Mode 决定未解析变量与输出超限的处理方式
//
// Mode decides how an unresolved variable and an oversized output are handled
type Mode int

const (
	// Strict 用于 send/finish/dry-run: 输出位置未解析报错, 输出超限报错
	//
	// Strict serves send, finish and dry-run: an unresolved variable at an output position is an
	// error, and so is an output beyond the size cap
	Strict Mode = iota

	// Check 用于 check: 未解析变量保留字面并告警, 输出超限截断并告警
	//
	// Check serves the check command: an unresolved variable stays literal with a warning, and an
	// oversized output is truncated with a warning
	Check
)

// 消息键, 每个都以 template.error. 或 template.warn. 开头并在 en 与 zh-CN 两份目录里成对存在
// reserved_var 由 notify 检测保留名时使用, 模板引擎本身不产生该错误
//
// The message keys, each starting with template.error. or template.warn. and existing in both the en
// and the zh-CN catalog
// reserved_var belongs to the reserved-name check notify performs, and the engine itself never raises it
const (
	keyErrorSyntax          = "template.error.syntax"
	keyErrorUnclosed        = "template.error.unclosed"
	keyErrorStrayElse       = "template.error.stray_else"
	keyErrorStrayEnd        = "template.error.stray_end"
	keyErrorUnsupportedExpr = "template.error.unsupported_expr"
	keyErrorTooLarge        = "template.error.too_large"
	keyErrorNesting         = "template.error.nesting"
	keyErrorUnresolved      = "template.error.unresolved"
	keyErrorReservedVar     = "template.error.reserved_var"
	keyWarnUnresolved       = "template.warn.unresolved"
	keyWarnTruncated        = "template.warn.truncated"
)

// fallbackTemplatePath 模板引擎不知道调用方字段 (--title, settings.url 等), Problem.Data 的
// Path 先填该占位; notify 或渠道拿到 Problem 后应覆写为自己的字段路径
//
// fallbackTemplatePath is what Problem.Data carries as Path, because the engine does not know the
// caller's field, such as --title or settings.url
// A caller such as notify or a channel overwrites Path with its own field path once it holds the Problem
const fallbackTemplatePath = "template"

// limitOutputLabel 输出/输入上限的展示文案, 由 maxTemplateLen 推导以避免两个常量漂移
//
// limitOutputLabel renders the input and output cap from maxTemplateLen, which keeps the label from
// drifting away from the constant
func limitOutputLabel() string {
	return strconv.Itoa(maxTemplateLen/1024) + "KB"
}

// limitNestingLabel 嵌套上限的展示文案, 由 maxParseNesting 推导
//
// limitNestingLabel renders the nesting cap from maxParseNesting
func limitNestingLabel() string {
	return strconv.Itoa(maxParseNesting)
}

// problemTemplate 构造模板 Problem, Data 先放入 Path 占位, extra 覆盖同名键
//
// problemTemplate builds a template Problem with the Path placeholder already in Data, and extra
// overwrites same-named keys
func problemTemplate(id string, extra map[string]any) *apperr.Problem {
	data := map[string]any{"Path": fallbackTemplatePath}
	for k, v := range extra {
		data[k] = v
	}
	return &apperr.Problem{ID: id, Data: data}
}

// ReservedVarProblem 构造保留名错误 (ID=template.error.reserved_var)
// 模板引擎不判断调用上下文, Path 先是占位; notify 检测 --var/变量文件时覆写为自己的位置
//
// ReservedVarProblem builds the reserved-name error with ID template.error.reserved_var
// The engine does not know the calling context, so Path starts as the placeholder, and notify
// overwrites it with its own position while checking --var and the vars file
func ReservedVarProblem(variable string) *apperr.Problem {
	return problemTemplate(keyErrorReservedVar, map[string]any{"Variable": variable})
}

// ---------------------------------------------------------------------------
// 解析
// ---------------------------------------------------------------------------

// parser 是解析状态: src 为整份模板, pos 为扫描位置, depth 为 if/range 嵌套深度
//
// parser holds the parse state: src is the whole template, pos is the scan position and depth
// is the if/range nesting depth
type parser struct {
	src   string
	pos   int
	depth int
}

// parseTemplate 把模板解析为节点序列
// 语法违规返回 *apperr.Problem; 未解析变量不是解析错误, 由渲染阶段按模式处理
//
// parseTemplate parses a template into a node sequence
// A grammar violation returns a *apperr.Problem, while an unresolved variable is not a parse error
// and is handled by the render pass according to the mode
func parseTemplate(src string) ([]Node, *apperr.Problem) {
	if len(src) > maxTemplateInputLen {
		return nil, problemTemplate(keyErrorTooLarge, map[string]any{"Limit": limitOutputLabel()})
	}
	p := &parser{src: src}
	nodes, stop, prob := p.parseSeq()
	if prob != nil {
		return nil, prob
	}
	switch stop {
	case "else":
		return nil, problemTemplate(keyErrorStrayElse, nil)
	case "end":
		return nil, problemTemplate(keyErrorStrayEnd, nil)
	}
	return nodes, nil
}

// parseSeq 解析一个语句序列, 直到输入结束或遇到 {{else}}/{{end}}
// 返回值 stop 表示停在哪类标签上, "" 表示输入已到末尾
//
// parseSeq parses one statement sequence, up to the end of the input or up to an {{else}} or
// {{end}} tag
// The stop result names the tag kind it stopped on, and "" means the input was exhausted
func (p *parser) parseSeq() ([]Node, string, *apperr.Problem) {
	var nodes []Node
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			nodes = append(nodes, textNode{text: lit.String()})
			lit.Reset()
		}
	}
	for p.pos < len(p.src) {
		rel := strings.Index(p.src[p.pos:], "{{")
		if rel < 0 {
			lit.WriteString(p.src[p.pos:])
			p.pos = len(p.src)
			break
		}
		abs := p.pos + rel
		if abs > 0 && p.src[abs-1] == '\\' {
			// \{{ 转义: 紧邻的反斜杠只作转义标记, 写出字面 {{ 后继续扫描, 不进入标签解析
			//
			// The \{{ escape: the adjacent backslash only marks the escape, so a literal {{ is
			// written and scanning continues without entering tag parsing
			lit.WriteString(p.src[p.pos : abs-1])
			lit.WriteString("{{")
			p.pos = abs + 2
			continue
		}
		lit.WriteString(p.src[p.pos:abs])
		p.pos = abs + 2
		closeRel := strings.Index(p.src[p.pos:], "}}")
		if closeRel < 0 {
			// 缺少关闭定界符: 归属语法错误, 与"块未闭合"的 unclosed 区分
			//
			// A missing closing delimiter counts as a syntax error, separate from the unclosed key used for an unclosed block
			return nil, "", problemTemplate(keyErrorSyntax, map[string]any{
				"Detail": "unterminated tag: {{" + redactSnippet(p.src[p.pos:]),
			})
		}
		orig := p.src[p.pos : p.pos+closeRel]
		raw := strings.TrimSpace(orig)
		p.pos += closeRel + 2

		switch {
		case raw == "":
			return nil, "", problemTemplate(keyErrorSyntax, map[string]any{"Detail": "empty tag {{}}"})
		case raw == "else":
			flush()
			return nodes, "else", nil
		case raw == "end":
			flush()
			return nodes, "end", nil
		case raw == "elseif" || strings.HasPrefix(raw, "elseif ") || strings.HasPrefix(raw, "else if"):
			// {{else if ...}} 与 {{elseif ...}} 不支持: 显式报错而不是静默判假
			// Expr 与其它片段一样先脱敏再截断, 否则整段原文会带着凭据进 Problem.Data
			//
			// {{else if ...}} and {{elseif ...}} are not supported, so this fails explicitly rather than judging silently
			// Like every other snippet, Expr is redacted and clipped first, otherwise the raw text would carry
			// credentials into Problem.Data
			return nil, "", problemTemplate(keyErrorUnsupportedExpr, map[string]any{"Expr": redactSnippet(raw)})
		case strings.HasPrefix(raw, "if "):
			flush()
			expr := strings.TrimSpace(strings.TrimPrefix(raw, "if "))
			if prob := checkCondExpr(expr); prob != nil {
				return nil, "", prob
			}
			then, stop, prob := p.parseBlock()
			if prob != nil {
				return nil, "", prob
			}
			var els []Node
			if stop == "else" {
				els, stop, prob = p.parseSeq()
				if prob != nil {
					return nil, "", prob
				}
				if stop == "else" {
					// 第二个 {{else}} 没有可归属的分支
					//
					// A second {{else}} has no branch left to belong to
					return nil, "", problemTemplate(keyErrorStrayElse, nil)
				}
			}
			if stop != "end" {
				return nil, "", problemTemplate(keyErrorUnclosed, map[string]any{"Tag": "if " + expr})
			}
			nodes = append(nodes, ifNode{path: expr, then: then, els: els})
		case strings.HasPrefix(raw, "range "):
			flush()
			expr := strings.TrimSpace(strings.TrimPrefix(raw, "range "))
			if prob := checkCondExpr(expr); prob != nil {
				return nil, "", prob
			}
			body, stop, prob := p.parseBlock()
			if prob != nil {
				return nil, "", prob
			}
			if stop == "else" {
				// range 不支持 else 分支, 块内的 {{else}} 按孤立 else 报错
				//
				// A range block has no else branch, so an {{else}} inside it reports a stray else
				return nil, "", problemTemplate(keyErrorStrayElse, nil)
			}
			if stop != "end" {
				return nil, "", problemTemplate(keyErrorUnclosed, map[string]any{"Tag": "range " + expr})
			}
			nodes = append(nodes, rangeNode{path: expr, body: body})
		default:
			vn, prob := parseVarExpr(raw, orig)
			if prob != nil {
				return nil, "", prob
			}
			flush()
			nodes = append(nodes, vn)
		}
	}
	flush()
	return nodes, "", nil
}

// parseBlock 解析 if/range 的块体, 进入时累计嵌套深度, 退出时归还
// 深度超过 maxParseNesting 报 nesting, 限定恶意深嵌套能驱动的递归量
//
// parseBlock parses an if or range body, counts the nesting depth on entry and returns it on exit
// A depth beyond maxParseNesting reports nesting, which bounds the recursion a deeply nested template can drive
func (p *parser) parseBlock() ([]Node, string, *apperr.Problem) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxParseNesting {
		return nil, "", problemTemplate(keyErrorNesting, map[string]any{"Limit": limitNestingLabel()})
	}
	return p.parseSeq()
}

// checkCondExpr 校验 if/range 条件表达式: 只接受变量名与点路径
// 纯数字字面量, 比较运算符与其它表达式一律报 unsupported_expr, 避免条件被静默判假
// Expr 先经 redactSnippet 脱敏并截断: 写坏的表达式可能夹带 URL 与 token
//
// checkCondExpr validates an if or range condition: only a variable name or a dotted path is accepted
// A pure numeric literal, a comparison operator and any other expression report unsupported_expr, which keeps a
// condition from reading as a silent false
// Expr goes through redactSnippet first, since a malformed expression may carry a URL and a token
func checkCondExpr(expr string) *apperr.Problem {
	if !isValidName(expr) || isNumericLiteral(expr) {
		return problemTemplate(keyErrorUnsupportedExpr, map[string]any{"Expr": redactSnippet(expr)})
	}
	return nil
}

// isValidName 判断变量名是否合法: 字母, 数字, 下划线, 点
// 以点开头是 range 体内的相对路径写法, @index 由调用方单独识别, 不是普通名字字符
//
// isValidName reports whether a variable name is legal: letters, digits, underscores and dots
// A leading dot is the relative form inside a range body, while @index is recognised separately by
// the caller and is not an ordinary name character
func isValidName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
		case r == '_' || r == '.':
		default:
			return false
		}
	}
	return true
}

// parseVarExpr 解析输出位置的变量标签 {{name}} 与 {{name|default:文本}}
// 不符合语法的标签报 syntax 而不是按字面保留; default 文本按字面量处理, 不做递归解析
//
// parseVarExpr parses an output-position tag, {{name}} or {{name|default:text}}
// A tag outside the grammar reports syntax instead of staying a literal, and the default text is
// taken literally with no recursive parsing
func parseVarExpr(raw, orig string) (Node, *apperr.Problem) {
	if raw == "." || raw == "@index" {
		return varNode{path: raw, orig: orig}, nil
	}
	if raw == "if" || raw == "range" {
		// {{if}}/{{range}} 缺表达式: 不落成名为 if/range 的普通变量
		//
		// {{if}} or {{range}} without an expression must not degrade into an ordinary variable named if or range
		return nil, problemTemplate(keyErrorSyntax, map[string]any{
			"Detail": "tag needs an expression: {{" + raw + "}}",
		})
	}
	name := raw
	def := ""
	hasDef := false
	if i := strings.Index(raw, "|"); i >= 0 {
		mod := raw[i+1:]
		if !strings.HasPrefix(mod, "default:") {
			return nil, problemTemplate(keyErrorSyntax, map[string]any{
				"Detail": "unknown modifier after |: {{" + redactSnippet(raw) + "}}",
			})
		}
		name = strings.TrimSpace(raw[:i])
		def = mod[len("default:"):]
		hasDef = true
	}
	if !isValidName(name) {
		return nil, problemTemplate(keyErrorSyntax, map[string]any{
			"Detail": "invalid variable name: {{" + redactSnippet(raw) + "}}",
		})
	}
	return varNode{path: name, def: def, hasDef: hasDef, orig: orig}, nil
}

// clipSnippet 把诊断片段截到 80 字节并回退到 rune 边界, 避免超长模板撑大错误信息
//
// clipSnippet clips a diagnostic snippet to 80 bytes, stepping back to a rune boundary so an
// oversized template does not inflate the error text
func clipSnippet(s string) string {
	const maxSnippet = 80
	if len(s) <= maxSnippet {
		return s
	}
	room := maxSnippet
	for room > 0 && !utf8.RuneStart(s[room]) {
		room--
	}
	return s[:room]
}

// redactURLPattern 是叶子包内的最小 URL 脱敏规则: scheme:// 直到第一个空白或引号的整体
// 换成 <redacted-url>, 使 userinfo 与 query 里的凭据不再进入 Problem.Data
//
// redactURLPattern is the minimal URL redaction rule inside this leaf package: one scheme:// form up to
// the first whitespace or quote is replaced as a whole with <redacted-url>, which keeps userinfo and any
// token in the query out of Problem.Data
var redactURLPattern = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s"']+`)

// redactSnippet 把模板片段脱敏后再按 clipSnippet 的口径截到 80 字节
// 错误信息会进 CI 日志, 而模板里合法地会出现带凭据的 URL (webhook 地址等); common.SanitizeString
// 不能复用, 因为 template 是叶子包, 依赖方向只允许标准库与 apperr, 导入 common 会构成
// "叶子包依赖业务包" 的环, 所以这里自带一份最小实现
// 该规则按形态识别: 只要片段长得像 scheme://... 就整体替换, 不解析 URL 结构,
// 因此 user:secret@example.com/p?token=x 与普通地址得到同样的处理
//
// redactSnippet masks one template snippet and then clips it to 80 bytes the way clipSnippet does
// Error text ends up in CI logs, and a template may legitimately carry a URL with credentials, such as a
// webhook address
// common.SanitizeString cannot be reused here, because template is a leaf package whose only allowed
// internal dependency is apperr, and importing common would turn the leaf into a business-package
// dependency, so this file carries its own minimal implementation
// The rule matches by shape: any fragment that looks like scheme://... is replaced as a whole without
// parsing URL structure, so user:secret@example.com/p?token=x is treated like any other address
func redactSnippet(s string) string {
	return clipSnippet(redactURLPattern.ReplaceAllString(s, "<redacted-url>"))
}

// ---------------------------------------------------------------------------
// 渲染
// ---------------------------------------------------------------------------

// loopCtx 是 range 体内的当前项与序号, nil 表示不在 range 体内
//
// loopCtx holds the current item and index inside a range body, and nil means outside one
type loopCtx struct {
	item  any
	index int
}

// renderState 携带渲染的输出缓冲, 模式与告警
//
// renderState carries the output buffer, the mode and the warnings of one render
type renderState struct {
	vars *Vars
	mode Mode
	sb   strings.Builder
	// trunc 只在 check 模式置位: 输出触到上限被截断, 渲染结束后统一补一条警告
	//
	// trunc is set in check mode only, when the output reached the cap and was cut, and one warning is appended once rendering finishes
	trunc bool
	// warned 记录已告警的变量名, 同一变量出现多次只告警一次
	//
	// warned records the variables already reported, so one variable warns once however often it appears
	warned map[string]bool
	warns  []apperr.Problem
}

// RenderWithMode 渲染模板, 返回渲染文本, 警告与错误
// Strict 下未解析变量与输出超限都是错误; Check 下未解析变量保留原文并告警, 输出超限截断并告警
// 语法错误在两种模式下都是错误
//
// RenderWithMode renders a template and returns the text, the warnings and an error
// Under Strict an unresolved variable and an oversized output are both errors, while under Check an unresolved
// variable stays as written with a warning and an oversized output is truncated with a warning
// A syntax error is an error in both modes
func RenderWithMode(tpl string, vars *Vars, mode Mode) (string, []apperr.Problem, error) {
	nodes, prob := parseTemplate(tpl)
	if prob != nil {
		return "", nil, prob
	}
	st := &renderState{vars: vars, mode: mode, warned: map[string]bool{}}
	if prob := renderNodes(st, nodes, nil); prob != nil {
		return "", nil, prob
	}
	if st.trunc {
		st.warns = append(st.warns, apperr.Problem{
			ID:   keyWarnTruncated,
			Data: map[string]any{"Path": fallbackTemplatePath, "Limit": limitOutputLabel()},
		})
	}
	return st.sb.String(), st.warns, nil
}

// leafValueSize 度量一个 JSON 叶子值按输出上限口径的字节数
// 字符串与 Literal 取内容长度; 对象/数组按 json.Marshal 的结果; 其余标量序列化后计量,
// 序列化失败退回 renderValue 的文本长度 (未知类型总有长度, 上限判定不因此漏网)
//
// leafValueSize measures one JSON leaf value in the same unit as the output cap
// A string or Literal counts its content bytes, an object or array is measured through json.Marshal,
// and any other scalar is measured after serialization, falling back to the renderValue text length
// when serialization fails, since an unknown type always has some length and must not slip past the cap
func leafValueSize(val any) int {
	switch t := val.(type) {
	case string:
		return len(t)
	case Literal:
		return len(t)
	case nil:
		return len("null")
	case json.Number:
		return len(t.String())
	case bool:
		if t {
			return len("true")
		}
		return len("false")
	default:
		if b, err := json.Marshal(val); err == nil {
			return len(b)
		}
		return len(renderValue(val))
	}
}

// capLeafValue 对单变量叶子的返回值执行 64KB 输出上限
// 原始值类型原样返回: 上限判定只测量长度, 不把值换成字符串
// Strict 超限报 too_large, 与渲染整段文本的口径一致, webhook 请求体因此不会超 64KB
// Check 超限不截断值而是返回完整原值加一条 truncated 警告: 按字节裁一段 JSON 文本会切坏
// 对象/数组的结构, 连字符串值也会被 JSON 转义规则搅断, 与其发出坏 JSON, 不如把"偏大但完整"
// 的内容交还调用方并显式告警, 满足"截断不静默"
// 字符串叶子理论上可以安全截断 (重新 marshal 后仍是合法 JSON), 这是后续可收紧的方向,
// 当前统一按"完整值+告警"处理以保持行为可预测
//
// capLeafValue enforces the 64KB output cap on the value a single-variable leaf returns
// The raw value keeps its original type: the cap only measures length and never rewrites the value into a string
// Under Strict an oversized value reports too_large, matching how a rendered whole text behaves, so a webhook
// body cannot exceed 64KB
// Under Check the value is not cut: slicing JSON text at a byte boundary would break the structure of an object
// or an array, and a string value would have to survive JSON escaping on top, so rather than emit broken JSON the
// complete value is returned with one explicit truncated warning, which satisfies "truncation never goes silent"
// A string leaf could in principle be cut safely, since re-marshaling stays valid JSON; tightening that is a
// possible next step, and for now every type takes the complete-value-plus-warning rule to keep the behavior
// predictable
func capLeafValue(val any, mode Mode) (any, []apperr.Problem, *apperr.Problem) {
	if leafValueSize(val) <= maxTemplateLen {
		return val, nil, nil
	}
	if mode == Strict {
		return nil, nil, problemTemplate(keyErrorTooLarge, map[string]any{"Limit": limitOutputLabel()})
	}
	warn := apperr.Problem{
		ID:   keyWarnTruncated,
		Data: map[string]any{"Path": fallbackTemplatePath, "Limit": limitOutputLabel()},
	}
	return val, []apperr.Problem{warn}, nil
}

// RenderJSONLeaf 渲染 JSON 正文里的字符串叶子
// 整个叶子恰好是一个变量 (允许前后空白) 时返回变量的原始类型, 混合文本返回渲染后的字符串;
// 未解析变量在 Strict 下报错, 在 Check 下保留字面并告警;
// 单变量路径不走 write, 因此命中后由 capLeafValue 补上 64KB 输出上限的判定
//
// RenderJSONLeaf renders one string leaf of a JSON body
// When the whole leaf is exactly one variable, surrounding whitespace allowed, the raw variable value is returned,
// while mixed text returns the rendered string
// An unresolved variable reports an error under Strict and stays literal with a warning under Check
// The single-variable path never passes through write, so capLeafValue applies the 64KB output cap to its result
func RenderJSONLeaf(tpl string, vars *Vars, mode Mode) (any, []apperr.Problem, error) {
	nodes, prob := parseTemplate(tpl)
	if prob != nil {
		return nil, nil, prob
	}
	if vn, ok := singleVarLeaf(nodes); ok {
		val, resolved := vars.resolveExpr(vn.path, nil)
		if vn.hasDef && (!resolved || !truthy(val)) {
			// 默认文本是字面量, 因此这里返回字符串而不是变量的原始类型
			//
			// The default text is a literal, so it is returned as a string rather than the raw type of the variable
			out, warns, prob := capLeafValue(vn.def, mode)
			if prob != nil {
				return nil, nil, prob
			}
			return out, warns, nil
		}
		if resolved {
			if lit, isLit := val.(Literal); isLit {
				val = string(lit)
			}
			out, warns, prob := capLeafValue(val, mode)
			if prob != nil {
				return nil, nil, prob
			}
			return out, warns, nil
		}
		if mode == Strict {
			return nil, nil, problemTemplate(keyErrorUnresolved, map[string]any{"Variable": vn.path})
		}
		warn := apperr.Problem{
			ID:   keyWarnUnresolved,
			Data: map[string]any{"Path": fallbackTemplatePath, "Variable": vn.path},
		}
		return "{{" + vn.orig + "}}", []apperr.Problem{warn}, nil
	}
	out, warns, err := RenderWithMode(tpl, vars, mode)
	if err != nil {
		return nil, nil, err
	}
	return out, warns, nil
}

// singleVarLeaf 判断节点序列是否恰好是一个变量, 其余节点只允许空白文本
// 命中时返回该变量节点, 供 RenderJSONLeaf 走原始类型保真路径
//
// singleVarLeaf reports whether the node sequence is exactly one variable with only whitespace text around it
// A hit returns that variable node, which lets RenderJSONLeaf take the raw-type fidelity path
func singleVarLeaf(nodes []Node) (varNode, bool) {
	var vn varNode
	found := false
	for _, n := range nodes {
		switch t := n.(type) {
		case varNode:
			if found {
				return varNode{}, false
			}
			vn = t
			found = true
		case textNode:
			if strings.TrimSpace(t.text) != "" {
				return varNode{}, false
			}
		default:
			return varNode{}, false
		}
	}
	return vn, found
}

// stopped 报告 check 模式的渲染是否已因输出触到上限而截断
// 截断后 write 只会丢弃内容, 继续渲染既不改变输出也不改变告警, 因此可以立即结束整棵树的遍历
//
// stopped reports whether a check-mode render already hit the output cap and was truncated
// Once truncated, write only drops content, so continuing the walk changes neither the output nor the
// warnings, which makes ending the whole traversal early safe
func (st *renderState) stopped() bool {
	return st.mode != Strict && st.trunc
}

// renderNodes 渲染节点序列
// if/range 的缺失变量按假/空处理且不报错, 只有输出位置的未解析变量按模式报错或告警
// check 模式下输出截断即停止渲染, 保留已写缓冲与截断告警, 不再展开剩余节点
//
// renderNodes renders a node sequence
// A missing variable in if or range reads as false or empty without an error, and only an unresolved variable at an
// output position reports an error or a warning according to the mode
// In check mode a truncated output stops the render at once, keeping the buffer written so far and the truncation
// warning, and the remaining nodes are never expanded
func renderNodes(st *renderState, nodes []Node, cur *loopCtx) *apperr.Problem {
	for _, n := range nodes {
		// 每个节点前先看截断标志, 让同一层里截断点之后的节点不再处理
		//
		// The truncation flag is checked before every node, so the nodes after the truncation point in
		// this sequence are not processed
		if st.stopped() {
			return nil
		}
		switch t := n.(type) {
		case textNode:
			if prob := st.write(t.text); prob != nil {
				return prob
			}
		case varNode:
			if prob := st.renderVar(t, cur); prob != nil {
				return prob
			}
		case ifNode:
			var val any
			switch t.path {
			case "true":
				val = true
			case "false":
				val = false
			default:
				val, _ = st.vars.resolveExpr(t.path, cur)
			}
			next := t.els
			if truthy(val) {
				next = t.then
			}
			if prob := renderNodes(st, next, cur); prob != nil {
				return prob
			}
		case rangeNode:
			val, ok := st.vars.resolveExpr(t.path, cur)
			if !ok {
				// 缺失变量按空处理: 不输出, 不报错, 不告警, 与 if 的缺失判假同源
				//
				// A missing variable reads as empty: nothing is written and no error or warning is raised, matching the missing-reads-false rule of if
				continue
			}
			for i, item := range toSlice(val) {
				// 内层循环同样要先看截断标志: 只在 renderNodes 循环顶部判断时, 一个空的
				// range 体会把列表剩下的元素逐个走完而不写任何东西
				//
				// The inner loop checks the flag as well: a check only at the top of the renderNodes
				// loop leaves an empty range body walking the rest of the list without writing anything
				if st.stopped() {
					return nil
				}
				inner := &loopCtx{item: item, index: i}
				if prob := renderNodes(st, t.body, inner); prob != nil {
					return prob
				}
			}
		}
	}
	return nil
}

// renderVar 渲染一个输出位置的变量: 已解析且为真的值原样渲染, 假值或缺失走 default,
// 未解析在 Strict 下报错, 在 Check 下保留 {{ }} 内的原文并告警
//
// renderVar renders one variable at an output position: a resolved truthy value is rendered as it stands, a falsy or
// missing value falls back to default, and an unresolved one reports an error under Strict or keeps the raw {{ }}
// text with a warning under Check
func (st *renderState) renderVar(vn varNode, cur *loopCtx) *apperr.Problem {
	val, ok := st.vars.resolveExpr(vn.path, cur)
	if ok && truthy(val) {
		// 变量值不递归渲染: 值里的 {{...}} 按字面写出
		//
		// A variable value is never re-rendered, so {{...}} inside it is written literally
		return st.write(renderValue(val))
	}
	if vn.hasDef {
		return st.write(vn.def)
	}
	if ok {
		// 已解析但为假值且无 default: 输出文本形式, 如空串, 0, false, 空对象
		//
		// A resolved falsy value without a default renders in its text form, such as an empty string, 0, false or an empty object
		return st.write(renderValue(val))
	}
	if st.mode == Strict {
		return problemTemplate(keyErrorUnresolved, map[string]any{"Variable": vn.path})
	}
	if !st.warned[vn.path] {
		st.warned[vn.path] = true
		st.warns = append(st.warns, apperr.Problem{
			ID:   keyWarnUnresolved,
			Data: map[string]any{"Path": fallbackTemplatePath, "Variable": vn.path},
		})
	}
	return st.write("{{" + vn.orig + "}}")
}

// write 写入渲染片段, 并执行输出上限
// Strict 下累计输出超过上限即报 too_large; Check 下截断到上限 (回退到 rune 边界) 并记录截断标志
// 恰好等于上限的输出与空片段都按未截断处理
//
// write appends one rendered fragment and enforces the output cap
// Under Strict a cumulative output beyond the cap reports too_large, while under Check the fragment is cut back to the
// cap at a rune boundary and the truncation flag is recorded
// An output exactly at the cap and an empty fragment both count as untruncated
func (st *renderState) write(s string) *apperr.Problem {
	if st.mode == Strict {
		if st.sb.Len()+len(s) > maxTemplateLen {
			return problemTemplate(keyErrorTooLarge, map[string]any{"Limit": limitOutputLabel()})
		}
		st.sb.WriteString(s)
		return nil
	}
	room := maxTemplateLen - st.sb.Len()
	if room <= 0 {
		if s != "" {
			st.trunc = true
		}
		return nil
	}
	if len(s) > room {
		for room > 0 && !utf8.RuneStart(s[room]) {
			room--
		}
		st.sb.WriteString(s[:room])
		st.trunc = true
		return nil
	}
	st.sb.WriteString(s)
	return nil
}

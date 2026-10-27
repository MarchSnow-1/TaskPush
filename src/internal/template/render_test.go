package template

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
)

// ---------------------------------------------------------------------------
// 模板引擎测试: 覆盖语法解析, 渲染两个模式, 变量解析与输出上限
// 本文件针对 RenderWithMode/RenderJSONLeaf 两个渲染入口与变量解析
//
// Tests for the template engine, covering syntax parsing, both render modes, variable resolution and
// the output cap
// This file targets the two render entries, RenderWithMode and RenderJSONLeaf, together with the
// variable resolution
// ---------------------------------------------------------------------------

// problemFrom 取出 err 链上的 *apperr.Problem, 取不到即判定测试失败
//
// problemFrom extracts the *apperr.Problem from the err chain, failing the test when there is none
func problemFrom(t *testing.T, err error) *apperr.Problem {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		t.Fatalf("error %T (%v) is not *apperr.Problem", err, err)
	}
	return prob
}

// TestRenderWithModeVariablesAndDefault 覆盖变量输出, default 的缺失/假值分支与点路径
//
// TestRenderWithModeVariablesAndDefault covers variable output, the missing and falsy default
// branches, and dotted paths
func TestRenderWithModeVariablesAndDefault(t *testing.T) {
	v := NewVars()
	v.Set("name", "world")
	v.Set("n", float64(42))
	v.Set("zero", 0)
	v.Set("no", false)
	v.Set("empty", "")
	v.Set("nilv", nil)
	v.Set("list", []any{})
	v.Set("obj", map[string]any{})
	v.Set("user", map[string]any{"name": "alice", "tags": []any{"x", "y"}})
	v.Set("a.b", "whole-key")
	v.Set("nested", map[string]any{"a": map[string]any{"b": []any{"deep"}}})

	cases := []struct{ in, want string }{
		{"hello {{name}}", "hello world"},
		{"n={{n}}", "n=42"},
		{"{{ zero }}", "0"},
		{"{{name|default:x}}", "world"},
		{"{{missing|default:fallback}}", "fallback"},
		{"{{empty|default:d}}", "d"},
		{"{{zero|default:0}}", "0"},
		{"{{no|default:true}}", "true"},
		{"{{nilv|default:null}}", "null"},
		{"{{list|default:none}}", "none"},
		{"{{obj|default:none}}", "none"},
		{"{{user.name}}/{{user.tags.1}}", "alice/y"},
		{"{{a.b}}", "whole-key"},
		{"{{nested.a.b.0}}", "deep"},
		{"{{list}}", ""},
		{"plain text no vars", "plain text no vars"},
	}
	for _, c := range cases {
		out, warns, err := RenderWithMode(c.in, v, Strict)
		if err != nil {
			t.Errorf("RenderWithMode(%q) error: %v", c.in, err)
			continue
		}
		if len(warns) != 0 {
			t.Errorf("RenderWithMode(%q) warnings = %v, want none", c.in, warns)
		}
		if out != c.want {
			t.Errorf("RenderWithMode(%q) = %q, want %q", c.in, out, c.want)
		}
	}
}

// TestRenderWithModeConditions 覆盖 if/else 的真值语义, 布尔字面量, 相对条件与嵌套
//
// TestRenderWithModeConditions covers if/else truthiness, the boolean literals, relative conditions
// inside a range and nesting
func TestRenderWithModeConditions(t *testing.T) {
	v := NewVars()
	v.Set("yes", "y")
	v.Set("no", false)
	v.Set("zero", 0)
	v.Set("empty", "")
	v.Set("items", []any{})
	v.Set("nested", map[string]any{"ok": 1})
	v.Set("rows", []any{
		map[string]any{"ok": true, "name": "a"},
		map[string]any{"ok": false, "name": "b"},
	})
	v.Set("vals", []any{"", "x"})

	cases := []struct{ in, want string }{
		{"{{if yes}}T{{else}}F{{end}}", "T"},
		{"{{if no}}T{{else}}F{{end}}", "F"},
		{"{{if zero}}T{{else}}F{{end}}", "F"},
		{"{{if empty}}T{{else}}F{{end}}", "F"},
		{"{{if items}}T{{else}}F{{end}}", "F"},
		{"{{if missing}}T{{else}}F{{end}}", "F"},
		{"{{if true}}T{{else}}F{{end}}", "T"},
		{"{{if false}}T{{else}}F{{end}}", "F"},
		{"{{if yes}}T{{end}}", "T"},
		{"a{{if missing}}T{{end}}b", "ab"},
		{"{{if nested.ok}}T{{else}}F{{end}}", "T"},
		{"{{if true}}a{{if true}}b{{end}}c{{end}}", "abc"},
		{"{{range rows}}{{if .ok}}[{{.name}}]{{else}}({{.name}}){{end}}{{end}}", "[a](b)"},
		{"{{range vals}}{{if .}}Y{{else}}N{{end}}{{end}}", "NY"},
	}
	for _, c := range cases {
		out, warns, err := RenderWithMode(c.in, v, Strict)
		if err != nil {
			t.Errorf("RenderWithMode(%q) error: %v", c.in, err)
			continue
		}
		if len(warns) != 0 {
			t.Errorf("RenderWithMode(%q) warnings = %v, want none", c.in, warns)
		}
		if out != c.want {
			t.Errorf("RenderWithMode(%q) = %q, want %q", c.in, out, c.want)
		}
	}
}

// TestRenderWithModeRange 覆盖列表遍历, @index, {{.}}, 相对字段, 嵌套 range 与缺失/空列表
//
// TestRenderWithModeRange covers list iteration, @index, {{.}}, relative fields, nested ranges and
// the missing and empty list cases
func TestRenderWithModeRange(t *testing.T) {
	v := NewVars()
	v.Set("items", []any{"a", "b", "c"})
	v.Set("users", []any{map[string]any{"name": "x", "meta": map[string]any{"age": 30}}})
	v.Set("rows", []any{[]any{"a", "b"}, []any{"c"}})
	v.Set("words", []string{"x", "y"})
	v.Set("empty", []any{})
	v.Set("single", "solo")
	v.Set("obj", map[string]any{"k": "v"})

	cases := []struct{ in, want string }{
		{"{{range items}}[{{@index}}={{.}}]{{end}}", "[0=a][1=b][2=c]"},
		{"{{range items}}{{.}}{{end}}", "abc"},
		{"{{range users}}{{.name}}:{{.meta.age}}{{end}}", "x:30"},
		{"{{range rows}}{{range .}}{{@index}}{{.}}{{end}}|{{end}}", "0a1b|0c|"},
		{"{{range words}}{{.}}{{end}}", "xy"},
		{"pre{{range missing}}x{{end}}post", "prepost"},
		{"{{range empty}}x{{end}}", ""},
		{"{{range single}}[{{.}}]{{end}}", "[solo]"},
		{"{{range obj}}[{{.k}}]{{end}}", "[v]"},
	}
	for _, c := range cases {
		// 缺失变量在 check 模式下同样按空处理且不产生警告
		//
		// A missing variable reads as empty in check mode as well and produces no warning
		for _, mode := range []Mode{Strict, Check} {
			out, warns, err := RenderWithMode(c.in, v, mode)
			if err != nil {
				t.Errorf("mode %d RenderWithMode(%q) error: %v", mode, c.in, err)
				continue
			}
			if len(warns) != 0 {
				t.Errorf("mode %d RenderWithMode(%q) warnings = %v, want none", mode, c.in, warns)
			}
			if out != c.want {
				t.Errorf("mode %d RenderWithMode(%q) = %q, want %q", mode, c.in, out, c.want)
			}
		}
	}
}

// TestRenderWithModeEscape 覆盖 \{{ 转义: 紧邻 {{ 的一个反斜杠只作转义标记
//
// TestRenderWithModeEscape covers the \{{ escape, where the one backslash adjacent to {{ only
// marks the escape
func TestRenderWithModeEscape(t *testing.T) {
	v := NewVars()
	v.Set("x", "v")

	cases := []struct{ in, want string }{
		{"\\{{x}}", "{{x}}"},
		{"a \\{{if x}}b", "a {{if x}}b"},
		{"\\\\{{x}}", "\\{{x}}"},
		{"{{if true}}a\\{{end}}b{{end}}", "a{{end}}b"},
		{"{{x}}", "v"},
		{"no escape here \\n", "no escape here \\n"},
	}
	for _, c := range cases {
		out, warns, err := RenderWithMode(c.in, v, Strict)
		if err != nil {
			t.Errorf("RenderWithMode(%q) error: %v", c.in, err)
			continue
		}
		if len(warns) != 0 {
			t.Errorf("RenderWithMode(%q) warnings = %v, want none", c.in, warns)
		}
		if out != c.want {
			t.Errorf("RenderWithMode(%q) = %q, want %q", c.in, out, c.want)
		}
	}
}

// TestRenderWithModeUnresolved 覆盖严格模式报错, check 模式保留原文并告警, 以及告警去重
//
// TestRenderWithModeUnresolved covers the strict error, the check-mode literal with a warning, and
// the warning deduplication
func TestRenderWithModeUnresolved(t *testing.T) {
	v := NewVars()
	v.Set("nilv", nil)

	// 严格模式: 输出位置未解析即报未解析变量
	//
	// Strict mode: an unresolved variable at an output position reports the unresolved variable
	out, warns, err := RenderWithMode("a{{ user.age }}b", v, Strict)
	if out != "" {
		t.Errorf("strict output = %q, want empty", out)
	}
	if warns != nil {
		t.Errorf("strict warnings = %v, want nil", warns)
	}
	prob := problemFrom(t, err)
	if prob.ID != keyErrorUnresolved {
		t.Errorf("strict error ID = %q, want %q", prob.ID, keyErrorUnresolved)
	}
	if got := prob.Data["Variable"]; got != "user.age" {
		t.Errorf("strict Variable = %v, want user.age", got)
	}
	if got, ok := prob.Data["Path"].(string); !ok || got == "" {
		t.Errorf("strict Path = %v, want a non-empty placeholder", prob.Data["Path"])
	}

	// check 模式: 保留 {{ }} 内的原文 (含空白), 并记录一条警告
	//
	// Check mode keeps the raw text inside {{ }}, whitespace included, and records one warning
	out, warns, err = RenderWithMode("a{{ user.age }}b", v, Check)
	if err != nil {
		t.Fatalf("check render: %v", err)
	}
	if out != "a{{ user.age }}b" {
		t.Errorf("check output = %q, want the literal tag", out)
	}
	if len(warns) != 1 {
		t.Fatalf("check warnings = %v, want exactly one", warns)
	}
	if warns[0].ID != keyWarnUnresolved {
		t.Errorf("warning ID = %q, want %q", warns[0].ID, keyWarnUnresolved)
	}
	if got := warns[0].Data["Variable"]; got != "user.age" {
		t.Errorf("warning Variable = %v, want user.age", got)
	}
	if got, ok := warns[0].Data["Path"].(string); !ok || got == "" {
		t.Errorf("warning Path = %v, want a non-empty placeholder", warns[0].Data["Path"])
	}

	// 同一变量多次出现只告警一次, 不同变量各告警一次
	//
	// One variable warns once however often it appears, while each different variable warns once
	_, warns, err = RenderWithMode("{{a}} {{a}} {{b}}", v, Check)
	if err != nil {
		t.Fatalf("check render: %v", err)
	}
	if len(warns) != 2 || warns[0].Data["Variable"] != "a" || warns[1].Data["Variable"] != "b" {
		t.Fatalf("deduplicated warnings = %v, want a then b", warns)
	}

	// 已解析的 nil 与未解析不同: 严格模式下输出空串且不报错
	//
	// A resolved nil differs from an unresolved name: strict mode writes an empty string without error
	out, warns, err = RenderWithMode("{{nilv}}", v, Strict)
	if err != nil || out != "" || len(warns) != 0 {
		t.Errorf("resolved nil = %q warns=%v err=%v, want empty output without error", out, warns, err)
	}

	// range 体外的 {{.}} 与 {{@index}} 没有当前项, 按未解析处理
	//
	// Outside a range body {{.}} and {{@index}} have no current item, so they count as unresolved
	for _, path := range []string{".", "@index"} {
		_, _, err := RenderWithMode("{{"+path+"}}", v, Strict)
		prob := problemFrom(t, err)
		if prob.ID != keyErrorUnresolved || prob.Data["Variable"] != path {
			t.Errorf("path %q: ID=%q Variable=%v, want unresolved %s", path, prob.ID, prob.Data["Variable"], path)
		}
	}
}

// TestRenderWithModeSyntaxErrors 覆盖语法错误键: 语法, 未闭合, 孤立 else/end, 不支持表达式
// 语法错误在严格与 check 两种模式下都是错误
//
// TestRenderWithModeSyntaxErrors covers the syntax error keys: syntax, unclosed, stray else and
// end, and unsupported expressions
// A syntax error is an error under both strict and check mode
func TestRenderWithModeSyntaxErrors(t *testing.T) {
	cases := []struct {
		tpl string
		id  string
	}{
		{"{{}}", keyErrorSyntax},
		{"{{name", keyErrorSyntax},
		{"{{a|}}", keyErrorSyntax},
		{"{{a b}}", keyErrorSyntax},
		{"{{if}}", keyErrorSyntax},
		{"{{range}}", keyErrorSyntax},
		{"{{if x}}Y", keyErrorUnclosed},
		{"{{range x}}Y", keyErrorUnclosed},
		{"{{if x}}Y{{range y}}Z", keyErrorUnclosed},
		{"{{else}}", keyErrorStrayElse},
		{"{{end}}", keyErrorStrayEnd},
		{"{{if x}}a{{else}}b{{else}}c{{end}}", keyErrorStrayElse},
		{"{{range x}}{{else}}{{end}}", keyErrorStrayElse},
		{"{{if a==1}}Y{{end}}", keyErrorUnsupportedExpr},
		{"{{if a > 0}}Y{{end}}", keyErrorUnsupportedExpr},
		{"{{if !a}}Y{{end}}", keyErrorUnsupportedExpr},
		{"{{if a && b}}Y{{end}}", keyErrorUnsupportedExpr},
		{"{{if x}}Y{{else if z}}W{{end}}", keyErrorUnsupportedExpr},
		{"{{if x}}Y{{elseif z}}W{{end}}", keyErrorUnsupportedExpr},
		{"{{if 1}}Y{{end}}", keyErrorUnsupportedExpr},
		{"{{range 3}}x{{end}}", keyErrorUnsupportedExpr},
		{"{{range a,b}}x{{end}}", keyErrorUnsupportedExpr},
	}
	for _, c := range cases {
		for _, mode := range []Mode{Strict, Check} {
			out, warns, err := RenderWithMode(c.tpl, NewVars(), mode)
			if err == nil {
				t.Errorf("mode %d RenderWithMode(%q) should fail", mode, c.tpl)
				continue
			}
			if out != "" || warns != nil {
				t.Errorf("mode %d RenderWithMode(%q) = %q warns=%v, want empty output and nil warnings", mode, c.tpl, out, warns)
			}
			if prob := problemFrom(t, err); prob.ID != c.id {
				t.Errorf("RenderWithMode(%q) ID = %q, want %q", c.tpl, prob.ID, c.id)
			}
		}
	}

	// 错误 Data 的占位符必须齐备, 否则 i18n 渲染会缺数据
	//
	// The error Data has to carry every placeholder, otherwise the i18n render lacks data
	_, _, err := RenderWithMode("{{if x}}Y", NewVars(), Strict)
	if got := problemFrom(t, err).Data["Tag"]; got != "if x" {
		t.Errorf("unclosed Tag = %v, want \"if x\"", got)
	}
	_, _, err = RenderWithMode("{{if a==1}}Y{{end}}", NewVars(), Strict)
	if got := problemFrom(t, err).Data["Expr"]; got != "a==1" {
		t.Errorf("unsupported Expr = %v, want \"a==1\"", got)
	}
	_, _, err = RenderWithMode("{{a b}}", NewVars(), Strict)
	if got, ok := problemFrom(t, err).Data["Detail"].(string); !ok || got == "" {
		t.Errorf("syntax Detail = %v, want a non-empty snippet", got)
	}
}

// TestRenderWithModeLimits 覆盖输入/输出 64KB 上限: 两种模式的输入超限都报错,
// 输出超限在严格模式报错, 在 check 模式截断并告警, 恰好等于上限不误报
//
// TestRenderWithModeLimits covers the 64KB input and output caps: an oversized input fails in both
// modes, an oversized output fails under strict mode and is truncated with a warning under check
// mode, and an output exactly at the cap is not reported
func TestRenderWithModeLimits(t *testing.T) {
	// 输入超限: 两种模式都是错误
	//
	// Oversized input: an error in both modes
	big := strings.Repeat("a", maxTemplateInputLen+1)
	for _, mode := range []Mode{Strict, Check} {
		_, _, err := RenderWithMode(big, NewVars(), mode)
		if prob := problemFrom(t, err); prob.ID != keyErrorTooLarge {
			t.Errorf("mode %d oversized input ID = %q, want %q", mode, prob.ID, keyErrorTooLarge)
		}
		if got := problemFrom(t, func() error { _, _, err := RenderWithMode(big, NewVars(), mode); return err }()).Data["Limit"]; got == "" {
			t.Errorf("mode %d oversized input Limit = %v, want a label", mode, got)
		}
	}
	// 输入恰好等于上限可解析
	//
	// An input exactly at the cap parses
	if out, _, err := RenderWithMode(strings.Repeat("a", maxTemplateInputLen), NewVars(), Strict); err != nil || len(out) != maxTemplateInputLen {
		t.Errorf("input at cap: len(out)=%d err=%v, want %d and no error", len(out), err, maxTemplateInputLen)
	}

	v := NewVars()
	v.Set("big", strings.Repeat("x", maxTemplateLen+10))
	// 严格模式输出超限报错
	//
	// Strict mode reports an oversized output
	_, _, err := RenderWithMode("S{{big}}E", v, Strict)
	if prob := problemFrom(t, err); prob.ID != keyErrorTooLarge {
		t.Errorf("strict oversized output ID = %q, want %q", prob.ID, keyErrorTooLarge)
	}
	// check 模式截断到上限并告警
	//
	// Check mode cuts the output at the cap and warns
	out, warns, err := RenderWithMode("S{{big}}E", v, Check)
	if err != nil {
		t.Fatalf("check oversized output: %v", err)
	}
	if len(out) != maxTemplateLen {
		t.Errorf("check output len = %d, want %d", len(out), maxTemplateLen)
	}
	if !strings.HasPrefix(out, "Sxxx") {
		t.Errorf("check output should start with the rendered prefix, got %q", out[:8])
	}
	if len(warns) != 1 || warns[0].ID != keyWarnTruncated {
		t.Fatalf("check warnings = %v, want one %s", warns, keyWarnTruncated)
	}
	if got := warns[0].Data["Limit"]; got != "64KB" {
		t.Errorf("truncated Limit = %v, want 64KB", got)
	}
	if got, ok := warns[0].Data["Path"].(string); !ok || got == "" {
		t.Errorf("truncated Path = %v, want a non-empty placeholder", warns[0].Data["Path"])
	}

	// 输出恰好等于上限不误报, 末尾空变量也不触发截断
	//
	// An output exactly at the cap is not reported, and a trailing empty variable does not truncate
	exact := NewVars()
	exact.Set("a", strings.Repeat("x", maxTemplateLen))
	exact.Set("b", "")
	for _, mode := range []Mode{Strict, Check} {
		out, warns, err := RenderWithMode("{{a}}{{b}}", exact, mode)
		if err != nil || len(out) != maxTemplateLen || len(warns) != 0 {
			t.Errorf("mode %d exact cap: len(out)=%d warns=%v err=%v, want %d without warnings", mode, len(out), warns, err, maxTemplateLen)
		}
	}
}

// TestRenderWithModeNesting 覆盖 if/range 嵌套上限 100
//
// TestRenderWithModeNesting covers the if/range nesting cap of 100
func TestRenderWithModeNesting(t *testing.T) {
	deep := func(n int) string {
		return strings.Repeat("{{if true}}", n) + "x" + strings.Repeat("{{end}}", n)
	}
	if out, _, err := RenderWithMode(deep(maxParseNesting), NewVars(), Strict); err != nil || out != "x" {
		t.Errorf("nesting at cap: out=%q err=%v, want x without error", out, err)
	}
	_, _, err := RenderWithMode(deep(maxParseNesting+1), NewVars(), Strict)
	prob := problemFrom(t, err)
	if prob.ID != keyErrorNesting {
		t.Errorf("nesting error ID = %q, want %q", prob.ID, keyErrorNesting)
	}
	if got := prob.Data["Limit"]; got != "100" {
		t.Errorf("nesting Limit = %v, want 100", got)
	}
}

// TestRenderWithModeNoRecursionAndNoEnv 覆盖变量值不递归渲染, Literal 原样输出与引擎不读环境变量
//
// TestRenderWithModeNoRecursionAndNoEnv covers the rule that variable values are never re-rendered,
// the literal output of Literal, and the fact that the engine reads no environment variable
func TestRenderWithModeNoRecursionAndNoEnv(t *testing.T) {
	v := NewVars()
	v.Set("a", "{{b}}")
	v.Set("b", "x")
	out, warns, err := RenderWithMode("{{a}}", v, Strict)
	if err != nil || len(warns) != 0 {
		t.Fatalf("nested value: out=%q warns=%v err=%v", out, warns, err)
	}
	// 值里的 {{b}} 按字面输出, 不再二次解析, 因此也不产生未解析告警
	//
	// The {{b}} inside the value stays literal with no second parse, so it raises no unresolved warning either
	if out != "{{b}}" {
		t.Errorf("value recursion must not happen, got %q", out)
	}
	if out, _, err := RenderWithMode("{{a}}", v, Check); err != nil || out != "{{b}}" {
		t.Errorf("check nested value = %q err=%v, want literal {{b}}", out, err)
	}

	lit := NewVars()
	lit.Set("body", Literal("任务 {{body}} 完成"))
	if out, _, err := RenderWithMode("{{body}}", lit, Strict); err != nil || out != "任务 {{body}} 完成" {
		t.Errorf("Literal output = %q err=%v, want it as written", out, err)
	}
	emptyLit := NewVars()
	emptyLit.Set("body", Literal(""))
	if out, _, err := RenderWithMode("{{body|default:兜底}}", emptyLit, Strict); err != nil || out != "兜底" {
		t.Errorf("empty Literal default = %q err=%v, want 兜底", out, err)
	}

	const envName = "TASKPUSH_TEMPLATE_EXAMPLE"
	t.Setenv(envName, "from-os")
	if out, _, err := RenderWithMode("{{env."+envName+"}}", NewVars(), Strict); err == nil || out != "" {
		t.Errorf("the engine must not read env: out=%q err=%v", out, err)
	}
	out, warns, err = RenderWithMode("{{env."+envName+"}}", NewVars(), Check)
	if err != nil || out != "{{env."+envName+"}}" || len(warns) != 1 {
		t.Errorf("check env = %q warns=%v err=%v, want literal with one warning", out, warns, err)
	}
}

// TestRenderJSONLeafTypes 覆盖 JSON 叶子的类型保真规则
//
// TestRenderJSONLeafTypes covers the type-fidelity rules of a JSON leaf
func TestRenderJSONLeafTypes(t *testing.T) {
	v := NewVars()
	v.Set("count", int64(5))
	v.Set("ratio", float64(1.5))
	v.Set("exact", json.Number("1.50"))
	v.Set("flag", true)
	v.Set("off", false)
	v.Set("zero", int64(0))
	v.Set("none", nil)
	v.Set("label", "text")
	v.Set("body", Literal("raw {{body}}"))
	v.Set("obj", map[string]any{"k": "v"})
	v.Set("list", []any{"a", "b"})

	cases := []struct {
		tpl  string
		want any
	}{
		{"{{count}}", int64(5)},
		{"  {{count}}  ", int64(5)},
		{"{{ratio}}", float64(1.5)},
		{"{{exact}}", json.Number("1.50")},
		{"{{flag}}", true},
		{"{{off}}", false},
		{"{{zero}}", int64(0)},
		{"{{none}}", nil},
		{"{{label}}", "text"},
		{"{{body}}", "raw {{body}}"},
		{"{{obj}}", map[string]any{"k": "v"}},
		{"{{list}}", []any{"a", "b"}},
		{"count={{count}}", "count=5"},
		{"{{count}} items", "5 items"},
		{"{{count|default:0}}", int64(5)},
		// 变量为假值时 default 生效, 结果是字面量文本而不是数字
		//
		// The default applies to a falsy value, and the result is literal text rather than a number
		{"{{zero|default:0}}", "0"},
		{"{{missing|default:none}}", "none"},
		{"\\{{count}}", "{{count}}"},
		{"{{if flag}}yes{{end}}", "yes"},
	}
	for _, c := range cases {
		got, warns, err := RenderJSONLeaf(c.tpl, v, Strict)
		if err != nil {
			t.Errorf("RenderJSONLeaf(%q) error: %v", c.tpl, err)
			continue
		}
		if len(warns) != 0 {
			t.Errorf("RenderJSONLeaf(%q) warnings = %v, want none", c.tpl, warns)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("RenderJSONLeaf(%q) = %#v (%T), want %#v (%T)", c.tpl, got, got, c.want, c.want)
		}
	}
}

// TestRenderJSONLeafUnresolved 覆盖叶子的未解析分支: 严格报错, check 保留字面并告警
//
// TestRenderJSONLeafUnresolved covers the unresolved branch of a leaf: a strict error, and a literal
// with a warning under check
func TestRenderJSONLeafUnresolved(t *testing.T) {
	v := NewVars()
	got, warns, err := RenderJSONLeaf("{{missing}}", v, Strict)
	if got != nil || warns != nil {
		t.Errorf("strict leaf = %#v warns=%v, want nil and nil", got, warns)
	}
	prob := problemFrom(t, err)
	if prob.ID != keyErrorUnresolved || prob.Data["Variable"] != "missing" {
		t.Errorf("strict leaf problem = %+v, want unresolved missing", prob)
	}

	got, warns, err = RenderJSONLeaf("{{ missing }}", v, Check)
	if err != nil {
		t.Fatalf("check leaf: %v", err)
	}
	if got != "{{ missing }}" {
		t.Errorf("check leaf = %#v, want the literal tag", got)
	}
	if len(warns) != 1 || warns[0].ID != keyWarnUnresolved || warns[0].Data["Variable"] != "missing" {
		t.Errorf("check leaf warnings = %v, want one unresolved warning", warns)
	}

	// 混合文本里的未解析变量同样处理
	//
	// An unresolved variable inside mixed text is handled the same way
	got, warns, err = RenderJSONLeaf("x={{missing}}", v, Check)
	if err != nil || got != "x={{missing}}" || len(warns) != 1 {
		t.Errorf("mixed check leaf = %#v warns=%v err=%v, want literal text with one warning", got, warns, err)
	}

	// 语法错误在叶子里也是错误
	//
	// A syntax error is an error inside a leaf as well
	if _, _, err := RenderJSONLeaf("{{if a==1}}x{{end}}", v, Strict); problemFrom(t, err).ID != keyErrorUnsupportedExpr {
		t.Errorf("leaf syntax error ID = %q, want %q", problemFrom(t, err).ID, keyErrorUnsupportedExpr)
	}
}

// TestRenderJSONLeafOversizedValue 覆盖单变量叶子的 64KB 上限: 这条路径必须走输出上限检查,
// 否则 1MB 的变量经 body.format=json 的叶子 "{{big}}" 会原样返回
// 字符串/Literal/对象/数组/数字都要判定; Strict 报 too_large, Check 返回完整原值加一条截断警告
// Check 下不截断值: 裁坏 JSON 结构比偏大的完整值更糟, 取舍写在 capLeafValue 注释里
//
// TestRenderJSONLeafOversizedValue covers the 64KB cap on a single-variable leaf: this path has to
// go through the output cap, or a 1MB variable behind the leaf "{{big}}" of a body.format=json comes
// back untouched
// Strings, Literals, objects, arrays and numbers all get measured; Strict reports too_large, and Check
// returns the complete original value with one truncation warning
// Check does not cut the value: broken JSON is worse than an oversized but intact one, and the reasoning
// sits in the capLeafValue comment
func TestRenderJSONLeafOversizedValue(t *testing.T) {
	big := strings.Repeat("x", maxTemplateLen+1)
	v := NewVars()
	v.Set("s", big)
	v.Set("lit", Literal(big))
	v.Set("obj", map[string]any{"k": big})
	v.Set("arr", []any{big, big})
	num := strings.Repeat("9", maxTemplateLen+1) // json.Number: 长度按字面量算 / a json.Number, measured by its literal length
	v.Set("num", json.Number(num))

	cases := []struct {
		name string
		tpl  string
	}{
		{"string", "{{s}}"},
		{"literal", "{{lit}}"},
		{"object", "{{obj}}"},
		{"array", "{{arr}}"},
		{"number", "{{num}}"},
	}
	for _, c := range cases {
		// Strict: 超限是错误, 不返回可入正文的值
		if got, warns, err := RenderJSONLeaf(c.tpl, v, Strict); err == nil || got != nil || warns != nil {
			t.Errorf("%s strict: got=%#v warns=%v err=%v, want the too_large error", c.name, got, warns, err)
			continue
		} else if prob := problemFrom(t, err); prob.ID != keyErrorTooLarge || prob.Data["Limit"] != "64KB" {
			t.Errorf("%s strict problem = %+v, want too_large with the 64KB label", c.name, prob)
		}
		// Check: 完整原值 + 恰好一条截断警告, 值不能被换成字符串或裁短
		got, warns, err := RenderJSONLeaf(c.tpl, v, Check)
		if err != nil {
			t.Errorf("%s check: err=%v", c.name, err)
			continue
		}
		if len(warns) != 1 || warns[0].ID != keyWarnTruncated {
			t.Errorf("%s check warnings = %v, want exactly one %s", c.name, warns, keyWarnTruncated)
		}
		switch c.name {
		case "string":
			if s, ok := got.(string); !ok || s != big {
				t.Errorf("%s check value must stay the full string", c.name)
			}
		case "literal":
			// Literal 按既有行为转成 string 返回, 但内容仍是完整原值
			if s, ok := got.(string); !ok || s != big {
				t.Errorf("%s check value must stay the full text", c.name)
			}
		case "object":
			if !reflect.DeepEqual(got, v.flat["obj"]) {
				t.Errorf("%s check value must be the original map, got %#v", c.name, got)
			}
		case "array":
			if !reflect.DeepEqual(got, v.flat["arr"]) {
				t.Errorf("%s check value must be the original slice, got %#v", c.name, got)
			}
		case "number":
			if n, ok := got.(json.Number); !ok || n.String() != num {
				t.Errorf("%s check value must stay the json.Number literal, got %#v", c.name, got)
			}
		}
	}

	// 未超限的标量不受影响, 类型保真与原有断言一致
	//
	// Values inside the cap are untouched, and the type fidelity matches the existing assertions
	small := NewVars()
	small.Set("n", int64(5))
	small.Set("t", true)
	small.Set("obj", map[string]any{"a": 1})
	for _, tpl := range []string{"{{n}}", "{{t}}", "{{obj}}"} {
		if _, warns, err := RenderJSONLeaf(tpl, small, Strict); err != nil || len(warns) != 0 {
			t.Errorf("small leaf %q: warns=%v err=%v, want none", tpl, warns, err)
		}
	}

	// 默认文本无法把 capLeafValue 撑破: 默认文本本身就是模板输入的一部分, 长度超过 64KB 的
	// default 先被 parseTemplate 的输入上限拒绝, 两种模式都报 too_large
	// 这条边界钉住"输入上限在前, 值上限在后"的顺序, capLeafValue 对 def 的判定是防御性兜底
	//
	// The default text cannot actually burst capLeafValue: the default is part of the template input, so a
	// default longer than 64KB is rejected first by the input cap of parseTemplate, and both modes report
	// too_large
	// This case pins the order, input cap before value cap, and the check capLeafValue applies to def is a
	// defensive backstop
	if _, _, err := RenderJSONLeaf("{{missing|default:"+big+"}}", small, Strict); problemFrom(t, err).ID != keyErrorTooLarge {
		t.Errorf("oversized default should report too_large under strict")
	}
	if _, _, err := RenderJSONLeaf("{{missing|default:"+big+"}}", small, Check); problemFrom(t, err).ID != keyErrorTooLarge {
		t.Errorf("oversized default should report too_large under check")
	}
	// 长度恰好到输入上限内且值超限的 default 走不到 capLeafValue 的报错分支, 这里用 63KB 的
	// default 验证未超限路径不受干扰
	//
	// A default inside the input cap but beyond the value cap cannot reach the error branch of
	// capLeafValue, so this 63KB default only verifies the under-cap path stays untouched
	ok63 := strings.Repeat("y", maxTemplateLen-1024)
	if got, warns, err := RenderJSONLeaf("{{missing|default:"+ok63+"}}", small, Strict); err != nil || got != ok63 || len(warns) != 0 {
		t.Errorf("63KB default: got len=%v warns=%v err=%v, want intact without warning", len(got.(string)), warns, err)
	}
}

// TestReservedNames 覆盖保留名列表与检查辅助
//
// TestReservedNames covers the reserved name list and the check helper
func TestReservedNames(t *testing.T) {
	want := []string{"title", "body", "time", "date", "datetime", "elapsed", "elapsed_ms", "elapsed_s", "env"}
	if !reflect.DeepEqual(ReservedVars, want) {
		t.Errorf("ReservedVars = %v, want %v", ReservedVars, want)
	}
	for _, name := range append(append([]string{}, want...), "env.FOO", "env.TASKPUSH_EXAMPLE") {
		if !IsReservedVar(name) {
			t.Errorf("IsReservedVar(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "my-var", "title.x", "elapsed_ms2", "environment"} {
		if IsReservedVar(name) {
			t.Errorf("IsReservedVar(%q) = true, want false", name)
		}
	}

	prob := ReservedVarProblem("title")
	if prob.ID != keyErrorReservedVar {
		t.Errorf("ReservedVarProblem ID = %q, want %q", prob.ID, keyErrorReservedVar)
	}
	if prob.Data["Variable"] != "title" || prob.Data["Path"] == "" {
		t.Errorf("ReservedVarProblem Data = %v, want Variable and Path", prob.Data)
	}
}

// TestProblemKeysRegistered 钉住模板包使用的每个键, 这些键都出现在 en 与 zh-CN 两份目录里
//
// TestProblemKeysRegistered pins every key the template package uses, all of which appear in both
// the en and the zh-CN catalog
func TestProblemKeysRegistered(t *testing.T) {
	registered := []string{
		"template.error.syntax",
		"template.error.unclosed",
		"template.error.stray_else",
		"template.error.stray_end",
		"template.error.unsupported_expr",
		"template.error.too_large",
		"template.error.nesting",
		"template.error.unresolved",
		"template.error.reserved_var",
		"template.warn.unresolved",
		"template.warn.truncated",
	}
	used := []string{
		keyErrorSyntax,
		keyErrorUnclosed,
		keyErrorStrayElse,
		keyErrorStrayEnd,
		keyErrorUnsupportedExpr,
		keyErrorTooLarge,
		keyErrorNesting,
		keyErrorUnresolved,
		keyErrorReservedVar,
		keyWarnUnresolved,
		keyWarnTruncated,
	}
	sort.Strings(registered)
	sort.Strings(used)
	if !reflect.DeepEqual(used, registered) {
		t.Errorf("template problem keys = %v, want %v", used, registered)
	}
	for _, key := range used {
		if !strings.HasPrefix(key, "template.") {
			t.Errorf("key %q must start with template.", key)
		}
	}
}

// nestedRangeTemplate 构造三级嵌套 range 模板与其变量: 每级列表 n 个元素, 叶子写一个 x
// 展开次数是 n^3, 因此 n=800 即 5.12 亿次节点渲染, 会被输出上限截断, 用来覆盖截断后停止展开的分支
//
// nestedRangeTemplate builds a three-level nested range template and its variables: every level holds n
// elements and the leaf writes one x, so the expansion is n^3 nodes, which makes n=800 five hundred and
// twelve million node renders that the output cap cuts, covering the branch that stops expanding after
// truncation
func nestedRangeTemplate(n int) (string, *Vars) {
	list := func() []any {
		out := make([]any, n)
		for i := range out {
			out[i] = i
		}
		return out
	}
	tpl := "{{range l1}}{{range l2}}{{range l3}}" + strings.Repeat("x", 64) + "{{end}}{{end}}{{end}}"
	v := NewVars()
	v.Set("l1", list())
	v.Set("l2", list())
	v.Set("l3", list())
	return tpl, v
}

// TestRenderWithModeTruncateShortCircuits 截断后立即停止渲染: check 模式三级嵌套 range 须在测试
// 设定的耗时上限内返回, 即截断一旦发生就不再遍历后续节点
// 若继续遍历, 每个节点都会递归展开, 代价随 n^3 增长, 上限就会被突破
// 本用例放在 testing.Short() 之下, 只在完整测试里跑, 避免拖慢 -short
//
// TestRenderWithModeTruncateShortCircuits pins the immediate stop after truncation: in check mode the
// three-level nested range has to return within the elapsed bound the test sets, which holds only when
// no node after the truncation point is visited
// Visiting them would expand every node and grow the cost with n^3, breaking that bound
// The case sits behind testing.Short() and runs only in a full test, which keeps -short fast
func TestRenderWithModeTruncateShortCircuits(t *testing.T) {
	if testing.Short() {
		t.Skip("the n=800 nested range expands n^3 nodes, so it is skipped in -short")
	}
	tpl, v := nestedRangeTemplate(800)
	start := time.Now()
	out, warns, err := RenderWithMode(tpl, v, Check)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("check render: %v", err)
	}
	if elapsed > time.Second {
		t.Errorf("nested range took %v, want under 1s: truncation must stop the render", elapsed)
	}
	if len(out) != maxTemplateLen {
		t.Errorf("truncated output len = %d, want %d", len(out), maxTemplateLen)
	}
	if len(warns) != 1 || warns[0].ID != keyWarnTruncated {
		t.Fatalf("warnings = %v, want exactly one %s", warns, keyWarnTruncated)
	}
}

// TestRenderWithModeTruncateKeepsOutputAndOneWarning 功能层面钉住截断结果: 输出仍是 64KB,
// 告警恰好一条, 与列表大小无关; 用例很小, -short 下同样运行
//
// TestRenderWithModeTruncateKeepsOutputAndOneWarning pins the truncation result at the functional level:
// the output is still 64KB and exactly one warning is raised, whatever the list size
// The case is small and runs in -short as well
func TestRenderWithModeTruncateKeepsOutputAndOneWarning(t *testing.T) {
	// 每级 4 个元素, 叶子 4KB: 64 次叶子展开就写满 64KB, 之后还有大量节点未处理
	//
	// Four elements per level with a 4KB leaf fills the 64KB cap after sixteen leaves, while plenty of
	// nodes remain unprocessed
	var leaf strings.Builder
	for leaf.Len() < 4*1024 {
		leaf.WriteString("0123456789abcdef")
	}
	tpl := "{{range l1}}{{range l2}}{{range l3}}" + leaf.String() + "{{end}}{{end}}{{end}}"
	list := []any{0, 1, 2, 3}
	v := NewVars()
	v.Set("l1", list)
	v.Set("l2", list)
	v.Set("l3", list)

	out, warns, err := RenderWithMode(tpl, v, Check)
	if err != nil {
		t.Fatalf("check render: %v", err)
	}
	if len(out) != maxTemplateLen {
		t.Errorf("output len = %d, want %d", len(out), maxTemplateLen)
	}
	if len(warns) != 1 || warns[0].ID != keyWarnTruncated {
		t.Fatalf("warnings = %v, want exactly one %s", warns, keyWarnTruncated)
	}
	if got := warns[0].Data["Limit"]; got != "64KB" {
		t.Errorf("truncated Limit = %v, want 64KB", got)
	}

	// 同样的模板在严格模式下报 too_large 而不是截断, 证明短路只在 check 模式生效
	//
	// The same template reports too_large instead of truncating under strict mode, which shows the
	// short-circuit applies to check mode only
	if _, _, err := RenderWithMode(tpl, v, Strict); problemFrom(t, err).ID != keyErrorTooLarge {
		t.Errorf("strict render should report %s", keyErrorTooLarge)
	}
}

// leakyTemplate 是一份含凭据的坏模板: userinfo 里有 secret, query 里有 TOPSECRET
// 各语法错误分支都会把模板片段写进 Problem.Data, 这些凭据因此可能进 CI 日志
//
// leakyTemplate is a broken template that carries credentials: secret in the userinfo and TOPSECRET in
// the query
// Every syntax error branch writes a template snippet into Problem.Data, which is how those credentials
// could reach the CI log
func leakyTemplate(tag string) string {
	return "{{" + tag + " https://user:secret@example.com/p?token=TOPSECRET}}"
}

// TestRenderWithModeRedactsSnippetCredentials 语法错误片段必须脱敏: 三种语法错误分支与
// unsupported_expr 的 Data 都不得含模板里的 userinfo 与 token, 片段一律经 redactSnippet 处理
//
// TestRenderWithModeRedactsSnippetCredentials requires the syntax-error snippets to be redacted: neither the
// three syntax branches nor unsupported_expr may carry the userinfo or the token from the template in Data,
// since every snippet runs through redactSnippet
func TestRenderWithModeRedactsSnippetCredentials(t *testing.T) {
	// 每个用例命中一类分支: unterminated tag / unknown modifier / invalid name / unsupported expr
	//
	// Each case hits one branch: an unterminated tag, an unknown modifier, an invalid name and an unsupported expression
	cases := []struct {
		name string
		tpl  string
		id   string
		key  string
	}{
		{"unterminated", "{{if x}}ok{{https://user:secret@example.com/p?token=TOPSECRET", keyErrorSyntax, "Detail"},
		{"unknown_modifier", leakyTemplate("a|nope:https://user:secret@example.com/p?token=TOPSECRET"), keyErrorSyntax, "Detail"},
		{"invalid_name", leakyTemplate("a b https://user:secret@example.com/p?token=TOPSECRET"), keyErrorSyntax, "Detail"},
		{"unsupported_expr", leakyTemplate("if a==1 https://user:secret@example.com/p?token=TOPSECRET"), keyErrorUnsupportedExpr, "Expr"},
	}
	for _, c := range cases {
		_, _, err := RenderWithMode(c.tpl, NewVars(), Strict)
		prob := problemFrom(t, err)
		if prob.ID != c.id {
			t.Errorf("%s: ID = %q, want %q", c.name, prob.ID, c.id)
		}
		got, ok := prob.Data[c.key].(string)
		if !ok {
			t.Errorf("%s: Data[%s] = %#v, want a string", c.name, c.key, prob.Data[c.key])
			continue
		}
		if strings.Contains(got, "secret") || strings.Contains(got, "TOPSECRET") {
			t.Errorf("%s: Data[%s] leaks credentials: %q", c.name, c.key, got)
		}
		if !strings.Contains(got, "<redacted-url>") {
			t.Errorf("%s: Data[%s] = %q, want the redaction placeholder", c.name, c.key, got)
		}
		if len(got) > 80 {
			t.Errorf("%s: Data[%s] length = %d, want at most 80 bytes", c.name, c.key, len(got))
		}
	}
}

// TestRedactSnippet 钉住脱敏与截断的两个边界: 被替换的 URL 形态, 以及 80 字节上限
// 非 URL 的普通片段按 clipSnippet 的口径处理, 不引入额外的改写
//
// TestRedactSnippet pins the two boundaries of redaction plus clipping: the URL shapes that get replaced
// and the 80-byte cap
// An ordinary snippet with no URL is handled by the clipSnippet rule with no extra rewriting
func TestRedactSnippet(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"a b", "a b"},
		{"https://example.com/hook", "<redacted-url>"},
		{"pre https://user:secret@example.com/p?token=x post", "pre <redacted-url> post"},
		{"http://example.com", "<redacted-url>"},
		{"ssh://example.com/x", "<redacted-url>"},
		{"{{name}}", "{{name}}"},
		// 多个 URL 各自替换
		//
		// Several URLs are each replaced
		{"https://a.example.com x https://b.example.com", "<redacted-url> x <redacted-url>"},
		// 引号结束 URL 匹配, 引号本身保留
		//
		// A quote ends the URL match and the quote itself survives
		{`"https://example.com/a"`, `"<redacted-url>"`},
		// 大小写与 '+' '.' '-' 组成的 scheme 都能识别
		//
		// Schemes built from letters in either case plus '+' '.' '-' are recognised
		{"SVN+SSH://example.com", "<redacted-url>"},
		// 不是 scheme:// 形态的不动
		//
		// Anything that is not a scheme:// shape is left alone
		{"example.com/path", "example.com/path"},
		{"//example.com/path", "//example.com/path"},
	}
	for _, c := range cases {
		if got := redactSnippet(c.in); got != c.want {
			t.Errorf("redactSnippet(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// 超长片段截到 80 字节
	//
	// An oversized snippet is clipped to 80 bytes
	long := strings.Repeat("y", 200)
	if got := redactSnippet(long); len(got) != 80 {
		t.Errorf("redactSnippet(200 y) length = %d, want 80", len(got))
	}
	// URL 后面直接跟非空白字符时, 整段都属于同一个 URL 形态, 会被一并替换
	// 规则按"直到空白或引号"匹配而不是解析 URL 结构, 因此这里期望的是一整个占位符
	//
	// When non-whitespace text follows a URL directly, the whole stretch is one URL shape and is replaced
	// together, because the rule matches up to whitespace or a quote instead of parsing URL structure, so a
	// single placeholder is what this case expects
	if got := redactSnippet("https://example.com/" + long); got != "<redacted-url>" {
		t.Errorf("redactSnippet(url + 200 y) = %q, want one placeholder", got)
	}
	// 空白之后的超长文本才走截断, 此时占位符与后续文本一并受 80 字节限制
	//
	// Only text after whitespace reaches the clipping step, and there the placeholder plus the following
	// text are both bounded by 80 bytes
	if got := redactSnippet("https://example.com x" + long); len(got) != 80 {
		t.Errorf("redactSnippet(url + space + 200 y) length = %d, want 80", len(got))
	}
	if got := clipSnippet(redactSnippet(long)); got != redactSnippet(long) {
		t.Errorf("clipSnippet must be idempotent over redactSnippet output")
	}
}

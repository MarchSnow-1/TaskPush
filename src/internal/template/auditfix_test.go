package template

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// 模板层边界语义的回归测试: 全部走 RenderWithMode 入口
// 每条用例注释写明它钉住的语义, 覆盖空容器判假, 点路径, range 缺失变量, 整数 round-trip 等
//
// Regression tests for the boundary semantics of the template layer, all through the
// RenderWithMode entry
// Each case comment names the semantic it pins, covering falsy empty containers, dot paths, a
// missing range variable and the integer round-trip
// ---------------------------------------------------------------------------

// TestAuditFixLiteralNotReExpanded Literal(调度层写入的最终内容) 按字面输出
// 引擎本身不递归渲染任何变量值, 因此普通字符串变量里的 {{b}} 同样按字面写出, 与 Literal 一致
//
// TestAuditFixLiteralNotReExpanded: a Literal, which is the final content the scheduling layer writes, is
// emitted as it stands
// The engine never re-renders a variable value at all, so the {{b}} inside an ordinary string variable is written
// literally as well, which matches what a Literal does
func TestAuditFixLiteralNotReExpanded(t *testing.T) {
	v := NewVars()
	v.Set("body", Literal("任务 {{body}} 完成"))
	out, warns, err := RenderWithMode("{{body}}", v, Strict)
	if err != nil || len(warns) != 0 {
		t.Fatalf("err=%v warns=%v", err, warns)
	}
	if out != "任务 {{body}} 完成" {
		t.Fatalf("Literal must not be re-expanded, got %q (len=%d)", out, len(out))
	}

	// 引擎不递归: 普通字符串变量里的标签同样按字面输出, 不会二次解析
	//
	// The engine does not recurse: a tag inside an ordinary string variable is written literally too, with no second parse
	nested := NewVars()
	nested.Set("b", "完成")
	nested.Set("a", "任务 {{b}}")
	if out, _, err := RenderWithMode("{{a}}", nested, Strict); err != nil || out != "任务 {{b}}" {
		t.Fatalf("the engine must not recurse a plain string var: %q err=%v", out, err)
	}

	// Literal 参与真值判断: 空 Literal 判假, 能走 |default
	//
	// A Literal takes part in truthiness, so an empty Literal reads as false and reaches |default
	v3 := NewVars()
	v3.Set("body", Literal(""))
	if out, _, err := RenderWithMode("{{body|default:兜底}}", v3, Strict); err != nil || out != "兜底" {
		t.Fatalf("empty Literal should trigger default: %q err=%v", out, err)
	}
}

// TestAuditFixTruthyEmptyContainers 空容器判假: 空 []string, 空 []any 与空 map 三者一致判假,
// 因此 Go 调用方 Set 的字符串切片与 JSON 数组来源的切片真值语义相同
//
// TestAuditFixTruthyEmptyContainers: an empty container reads as false, so an empty []string, an
// empty []any and an empty map all agree, which keeps the truthiness of a Go string slice the same
// as that of a slice coming from a JSON array
func TestAuditFixTruthyEmptyContainers(t *testing.T) {
	if truthy([]string{}) {
		t.Error("empty []string must be falsy")
	}
	if !truthy([]string{"a"}) {
		t.Error("non-empty []string must be truthy")
	}
	if truthy([]any{}) {
		t.Error("empty []any must be falsy")
	}
	if truthy(map[string]any{}) {
		t.Error("empty map must be falsy")
	}
	if !truthy(map[string]any{"k": 1}) {
		t.Error("non-empty map must be truthy")
	}
}

// TestAuditFixRangeSpacingExpr 钉住模板层的这几条语义:
//   - 缺失的 range 变量输出空, 不报错不告警, 也不把标签写回;
//   - 未解析变量在 check 模式下按 {{ }} 内原文保留 (含内部空白);
//   - 非 ASCII 键名可用于 {{if}};
//   - 比较表达式仍报 unsupported_expr;
//   - 带短横线的键名属于非法变量名, 与变量名语法 (字母/数字/下划线/点) 一致
//
// TestAuditFixRangeSpacingExpr pins these template-layer semantics:
//   - a missing range variable renders empty with no error and no warning, and the tag is not
//     written back
//   - an unresolved variable keeps the raw {{ }} text, inner whitespace included, under check mode
//   - a non-ASCII key name works in {{if}}
//   - a comparison expression still reports unsupported_expr
//   - a hyphenated key name is not a legal variable name, matching the name grammar of letters,
//     digits, underscores and dots
func TestAuditFixRangeSpacingExpr(t *testing.T) {
	out, warns, err := RenderWithMode("{{range itmes}}X{{end}}", NewVars(), Check)
	if err != nil {
		t.Fatal(err)
	}
	if out != "" || len(warns) != 0 {
		t.Errorf("missing range path must render empty without a warning, got %q warns=%v", out, warns)
	}

	out, _, err = RenderWithMode("a{{ undefined }}b", NewVars(), Check)
	if err != nil {
		t.Fatal(err)
	}
	if out != "a{{ undefined }}b" {
		t.Errorf("unresolved var must be byte-preserved, got %q", out)
	}

	v := NewVars()
	v.Set("中文", "是")
	if out, _, err := RenderWithMode("{{if 中文}}T{{else}}F{{end}}", v, Strict); err != nil || out != "T" {
		t.Errorf("unicode key in if: %q err=%v", out, err)
	}
	if _, _, err := RenderWithMode("{{if a==1}}T{{end}}", v, Strict); problemFrom(t, err).ID != keyErrorUnsupportedExpr {
		t.Error("comparison expression must still error")
	}
	if _, _, err := RenderWithMode("{{if my-var}}T{{end}}", v, Strict); problemFrom(t, err).ID != keyErrorUnsupportedExpr {
		t.Error("a hyphenated name is not a legal variable name and must report unsupported_expr")
	}
}

// TestAuditFixScalarRoundTripAndJSONStrict
//   - CLI 整数同样做 round-trip: "-0" 按原文保留, 不改写成 "0";
//   - vars 文件顶层尾随内容显式报错, 不静默丢弃
//
// TestAuditFixScalarRoundTripAndJSONStrict
//   - a CLI integer goes through the same round-trip, so "-0" keeps its written form rather than
//     becoming "0" in silence
//   - trailing content at the top level of a vars file is an explicit error rather than being dropped silently
func TestAuditFixScalarRoundTripAndJSONStrict(t *testing.T) {
	if got := inferScalar("-0"); got != "-0" {
		t.Errorf(`inferScalar("-0") = %#v, want string "-0"`, got)
	}
	if got := inferScalar("-5"); got != int64(-5) {
		t.Errorf(`inferScalar("-5") = %#v, want int64(-5)`, got)
	}
	v := NewVars()
	if err := MergeVarsJSON([]byte(`{"a":1}{"b":2}`), v); err == nil {
		t.Error("trailing JSON content must error")
	}
	if err := MergeVarsJSON([]byte("{\"a\":1}  \n"), v); err != nil {
		t.Errorf("trailing whitespace must stay acceptable: %v", err)
	}
}

// TestAuditFixNumericLiteralIfRange 数字字面量作 if/range 条件须显式报错而不是静默判假
//
// TestAuditFixNumericLiteralIfRange: a numeric literal as an if/range condition needs an explicit error
// rather than a silent false
func TestAuditFixNumericLiteralIfRange(t *testing.T) {
	for _, in := range []string{"{{if 1}}T{{else}}F{{end}}", "{{if 0}}T{{else}}F{{end}}", "{{range 3}}x{{end}}"} {
		if _, _, err := RenderWithMode(in, NewVars(), Strict); problemFrom(t, err).ID != keyErrorUnsupportedExpr {
			t.Errorf("RenderWithMode(%q) should report %s", in, keyErrorUnsupportedExpr)
		}
	}
	// 布尔字面量仍受支持 ({{if true}} 走真分支)
	//
	// A boolean literal is still supported, and {{if true}} renders the true branch
	if out, _, err := RenderWithMode("{{if true}}T{{else}}F{{end}}", NewVars(), Strict); err != nil || out != "T" {
		t.Errorf("{{if true}} should render T, got %q err=%v", out, err)
	}
}

// TestAuditFixTruncateGuardNoFalsePositive 恰好 64KB 输出 + 结尾空变量不应误报截断
// check 模式是截断判定真正发生的模式, 因此这里用 Check
//
// TestAuditFixTruncateGuardNoFalsePositive: an output of exactly 64KB followed by an empty variable must not be
// reported as truncated by mistake
// Check is the mode where truncation is actually decided, so this case runs under Check
func TestAuditFixTruncateGuardNoFalsePositive(t *testing.T) {
	v := NewVars()
	v.Set("a", strings.Repeat("x", maxTemplateLen))
	v.Set("b", "") // 空变量: 贡献 0 字节 / an empty variable, which contributes 0 bytes
	out, warns, err := RenderWithMode("{{a}}{{b}}", v, Check)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(warns) != 0 {
		t.Errorf("exact 64KB + empty tail must not be marked truncated (content intact), warns=%v", warns)
	}
	if len(out) != maxTemplateLen {
		t.Errorf("len = %d, want %d", len(out), maxTemplateLen)
	}
}

// TestAuditFixBareElseIfErrors 裸 {{elseif}}/{{else if}}(缺条件) 与 {{else if b}} 都报 unsupported_expr
//
// TestAuditFixBareElseIfErrors: a bare {{elseif}} or {{else if}} with no condition, and {{else if b}} as well,
// all report unsupported_expr
func TestAuditFixBareElseIfErrors(t *testing.T) {
	for _, in := range []string{"{{if a}}x{{elseif}}y{{end}}", "{{if a}}x{{else if}}y{{end}}", "{{if a}}x{{else if b}}y{{end}}"} {
		if _, _, err := RenderWithMode(in, NewVars(), Strict); problemFrom(t, err).ID != keyErrorUnsupportedExpr {
			t.Errorf("RenderWithMode(%q) should report %s", in, keyErrorUnsupportedExpr)
		}
	}
}

// TestAuditFixRenderValueStringSlice []string 与 []any 一样逗号连接
//
// TestAuditFixRenderValueStringSlice: a []string joins with commas just like a []any
func TestAuditFixRenderValueStringSlice(t *testing.T) {
	v := NewVars()
	v.Set("xs", []string{"a", "b", "c"})
	if out, _, err := RenderWithMode("{{xs}}", v, Strict); err != nil || out != "a,b,c" {
		t.Errorf("[]string render = %q err=%v, want a,b,c", out, err)
	}
}

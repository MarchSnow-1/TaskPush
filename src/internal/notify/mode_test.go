package notify

import (
	"context"
	"errors"
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/config"
)

// ---------------------------------------------------------------------------
// 端到端模式测试: 用真实渲染型假渠道验证 Send 走 RenderStrict, Check 走 RenderCheck
// 未解析变量在前者成为错误, 在后者退化为警告, 这正是 common.TemplateMode 映射的行为
//
// End-to-end mode tests: the rendering fake verifies that Send takes RenderStrict and Check takes
// RenderCheck
// An unresolved variable is an error in the first and a warning in the second, which is exactly what
// the common.TemplateMode mapping does
// ---------------------------------------------------------------------------

// TestSend_StrictChannelTemplate 覆盖渠道 settings 里的未解析变量在 Send 下报错进 Result
//
// TestSend_StrictChannelTemplate covers an unresolved variable in a channel template failing under
// Send and landing in Result
func TestSend_StrictChannelTemplate(t *testing.T) {
	resetFake()
	results, err := Send(context.Background(), defaultTestDefaults(),
		[]config.Channel{testRenderChannel("example", "value {{missing}}")}, Options{})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	var prob *apperr.Problem
	if !errors.As(results[0].Error, &prob) {
		t.Fatalf("result error = %v, want *apperr.Problem", results[0].Error)
	}
	if prob.ID != "template.error.unresolved" {
		t.Errorf("ID = %q, want template.error.unresolved", prob.ID)
	}
	if got := recordOf("example").SendCnt; got != 0 {
		t.Errorf("Send call count = %d, want 0", got)
	}
}

// TestSend_ChannelSeesTitleBody 覆盖渠道模板能引用渲染后的 {{title}} 与 {{body}}
//
// TestSend_ChannelSeesTitleBody covers a channel template referencing the rendered title and body
func TestSend_ChannelSeesTitleBody(t *testing.T) {
	resetFake()
	results, err := Send(context.Background(), defaultTestDefaults(),
		[]config.Channel{testRenderChannel("example", "{{title}}/{{body}}")},
		Options{Title: "T", Body: "B"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if results[0].Error != nil {
		t.Fatalf("result error = %v, want nil", results[0].Error)
	}
	if got := recordOf("example").Title; got != "T/B" {
		t.Errorf("channel render = %q, want T/B", got)
	}
}

// TestSend_LiteralValueNotRecursed 覆盖变量值里的 {{...}} 不被递归渲染
// 正文渲染完成后以 template.Literal 写回, 渠道模板引用它只得到最终文本
//
// TestSend_LiteralValueNotRecursed covers {{...}} inside a variable value not being rendered
// recursively
// The body is written back as a template.Literal once rendered, so a channel template referencing it
// receives the final text only
func TestSend_LiteralValueNotRecursed(t *testing.T) {
	resetFake()
	results, err := Send(context.Background(), defaultTestDefaults(),
		[]config.Channel{testRenderChannel("example", "{{body}}")},
		Options{Body: "literal {{fromCLI}} stays", VarPairs: []string{"fromCLI=expanded"}})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if results[0].Error != nil {
		t.Fatalf("result error = %v, want nil", results[0].Error)
	}
	// 正文渲染时 fromCLI 会被展开一次, 展开后的文本再被渠道模板引用时不再二次解析
	//
	// The body expands fromCLI once while rendering, and the expanded text is not parsed a second time
	// when the channel template references it
	if got := recordOf("example").Title; got != "literal expanded stays" {
		t.Errorf("channel render = %q, want the once-expanded text", got)
	}
}

// TestCheck_LenientChannelTemplate 覆盖渠道 settings 里的未解析变量在 Check 下退化为警告
//
// TestCheck_LenientChannelTemplate covers an unresolved variable in a channel template degrading to
// a warning under Check
func TestCheck_LenientChannelTemplate(t *testing.T) {
	resetFake()
	results, err := Check(context.Background(), defaultTestDefaults(),
		[]config.Channel{testRenderChannel("example", "value {{missing}}")}, CheckOptions{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if results[0].Error != nil {
		t.Fatalf("result error = %v, want nil under the lenient mode", results[0].Error)
	}
	found := false
	for _, warn := range results[0].Warnings {
		if warn.ID == "template.warn.unresolved" {
			found = true
		}
	}
	if !found {
		t.Errorf("Warnings = %+v, want the unresolved-variable warning", results[0].Warnings)
	}
	if got := recordOf("example").SendCnt; got != 0 {
		t.Errorf("Send call count = %d, want 0", got)
	}
}

// TestCheck_LenientSyntaxStillError 覆盖语法错误在宽松模式下仍然是错误
//
// TestCheck_LenientSyntaxStillError covers a syntax error staying an error under the lenient mode
func TestCheck_LenientSyntaxStillError(t *testing.T) {
	resetFake()
	results, err := Check(context.Background(), defaultTestDefaults(),
		[]config.Channel{testRenderChannel("example", "{{if}}")}, CheckOptions{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	var prob *apperr.Problem
	if !errors.As(results[0].Error, &prob) {
		t.Fatalf("result error = %v, want *apperr.Problem", results[0].Error)
	}
	if prob.ID != "template.error.syntax" {
		t.Errorf("ID = %q, want template.error.syntax", prob.ID)
	}
}

// TestCheck_VarsAvailable 覆盖 check 合并的变量对渠道模板可见, 且没有计时变量
//
// TestCheck_VarsAvailable covers check's merged variables being visible to the channel template,
// without the timing variables
func TestCheck_VarsAvailable(t *testing.T) {
	resetFake()
	results, err := Check(context.Background(), defaultTestDefaults(),
		[]config.Channel{testRenderChannel("example", "{{fromCLI}} {{date}}")},
		CheckOptions{VarPairs: []string{"fromCLI=ok"}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if results[0].Error != nil {
		t.Fatalf("result error = %v, want nil", results[0].Error)
	}
	record := recordOf("example")
	if len(record.Title) < 2 || record.Title[:2] != "ok" {
		t.Errorf("channel render = %q, want it to start with ok", record.Title)
	}

	resetFake()
	results, err = Check(context.Background(), defaultTestDefaults(),
		[]config.Channel{testRenderChannel("example", "{{elapsed}}")}, CheckOptions{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	// check 不接收计时起点, elapsed 因此未定义: 宽松模式下保留原文并告警
	//
	// check takes no timing origin, so elapsed is undefined: the lenient mode keeps the text and warns
	if len(results[0].Warnings) == 0 {
		t.Error("elapsed under check, want the unresolved warning")
	}
}

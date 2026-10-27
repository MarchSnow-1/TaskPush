package telegram

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/template"
)

// ---------------------------------------------------------------------------
// check 模式下 telegram settings 字段的语义测试
// check 模式把未解析变量保留字面并继续, 保留下来的模板串随后仍要过字段自身的语义校验, 所以
// apiBase 在 check 下可能以 invalid_api_base 结束, 而 chatIds 与 token 的保留字面能继续
// 这些用例固定该边界, 免得实现改动后 check 模式静默接受一个不可用的地址
//
// Semantics of the telegram settings fields under check mode
// Check mode keeps an unresolved variable literal and continues, and the retained template text
// still faces the semantic check of its own field, so apiBase can end in invalid_api_base under
// check while a retained chatIds or token literal lets the preparation continue
// These tests pin that boundary down, which keeps a later change from silently accepting an
// unusable address in check mode
// ---------------------------------------------------------------------------

// TestPrepare_CheckModeTemplatedAPIBase 覆盖 check 模式下未解析 apiBase 的失败边界
// 保留字面后地址不是可用地址, 因此 invalid_api_base 返回, 同时仍带上未解析变量的告警
//
// TestPrepare_CheckModeTemplatedAPIBase covers the failure boundary of an unresolved apiBase under
// check mode
// The retained literal is not a usable address, so invalid_api_base comes back together with the
// unresolved-variable warning
func TestPrepare_CheckModeTemplatedAPIBase(t *testing.T) {
	ch := newTestChannel(t, `{"token":"EXAMPLE","chatIds":["123456789"],"apiBase":"{{base}}"}`)
	prepared, warns, err := ch.Prepare(context.Background(), channel.Message{
		Body: "b", Vars: template.NewVars(), Mode: channel.RenderCheck,
	})
	if prepared != nil {
		t.Fatal("a retained apiBase literal produced a Prepared")
	}
	prob := mustProblem(t, err)
	if prob.ID != keyErrorInvalidBase {
		t.Fatalf("ID = %q, want %q", prob.ID, keyErrorInvalidBase)
	}
	if !apperr.IsPermanent(err) {
		t.Error("an unusable apiBase under check mode is not permanent")
	}
	// URL 占位符经过 MaskURL: 保留的模板串在错误里不以原文出现
	//
	// The URL placeholder passes through MaskURL, so the retained template text does not appear as
	// written
	if url, _ := prob.Data["URL"].(string); url == "{{base}}" {
		t.Fatalf("the untouched template literal reached the error Data: %q", url)
	}
	if len(warns) != 1 || warns[0].ID != "template.warn.unresolved" {
		t.Fatalf("warnings = %v, want one template.warn.unresolved", warns)
	}
	if warns[0].Data["Path"] != "settings.apiBase" {
		t.Fatalf("warning Path = %v, want settings.apiBase", warns[0].Data["Path"])
	}
}

// TestPrepare_CheckModeTemplatedToken 覆盖 check 模式下未解析 token 保留字面
// token 保留字面后仍是合法字符串, 因此 check 模式继续构造 Prepared 并留下告警; 真实发送走的是
// 严格模式, 那里未解析变量直接报错, 不会把字面模板串当凭据发出去
//
// TestPrepare_CheckModeTemplatedToken covers check mode keeping an unresolved token literal
// A retained token literal is still a legal string, so check mode keeps building the Prepared and
// leaves the warning behind; a real send uses the strict mode, where an unresolved variable fails
// outright, so the literal template text is not sent as a credential there
func TestPrepare_CheckModeTemplatedToken(t *testing.T) {
	ch := newTestChannel(t, `{"token":"{{token}}","chatIds":["123456789"]}`)
	prepared, warns, err := ch.Prepare(context.Background(), channel.Message{
		Body: "b", Vars: template.NewVars(), Mode: channel.RenderCheck,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	p := prepared.(*preparedChannel)
	if !strings.Contains(p.endpoint, "/bot") || !strings.HasSuffix(p.endpoint, "/sendMessage") {
		t.Fatalf("endpoint = %q", p.endpoint)
	}
	found := false
	for _, w := range warns {
		if w.ID == "template.warn.unresolved" && w.Data["Path"] == "settings.token" {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %v, want an unresolved warning for settings.token", warns)
	}
	// 预览里同样不出现 token 字面值
	//
	// The preview carries no token literal either
	encoded, err := json.Marshal(prepared.Preview())
	if err != nil {
		t.Fatalf("marshal preview: %v", err)
	}
	if strings.Contains(string(encoded), "{{token}}") {
		t.Fatalf("the preview leaked the token template: %s", encoded)
	}

	// 严格模式下同一实例直接报错, 不会构造出端点
	//
	// Under the strict mode the same instance fails outright and builds no endpoint
	strict, _, strictErr := prepareStrict(ch, "b", "")
	if strict != nil {
		t.Fatal("strict mode produced a Prepared for an unresolved token")
	}
	prob := mustProblem(t, strictErr)
	if prob.ID != "template.error.unresolved" {
		t.Fatalf("strict ID = %q, want template.error.unresolved", prob.ID)
	}
	if prob.Data["Path"] != "settings.token" {
		t.Fatalf("strict Path = %v, want settings.token", prob.Data["Path"])
	}
}

// TestPrepare_CheckModeTemplatedChatIDsStillBuilds 覆盖 check 模式未解析 chatIds 仍构造出请求
// chatIds 保留字面后是合法字符串, 因此 check 模式继续构造 Prepared 并留下告警
//
// TestPrepare_CheckModeTemplatedChatIDsStillBuilds covers check mode still building a request when
// chatIds stays unresolved
// A retained chatIds literal is a legal string, so check mode keeps building the Prepared and
// leaves the warning behind
func TestPrepare_CheckModeTemplatedChatIDsStillBuilds(t *testing.T) {
	ch := newTestChannel(t, `{"token":"EXAMPLE","chatIds":["{{cid}}"]}`)
	prepared, warns, err := ch.Prepare(context.Background(), channel.Message{
		Body: "b", Vars: template.NewVars(), Mode: channel.RenderCheck,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	targets := prepared.(*preparedChannel).targets
	if len(targets) != 1 || targets[0].chatID != "{{cid}}" {
		t.Fatalf("targets = %v", targets)
	}
	if len(warns) != 1 || warns[0].ID != "template.warn.unresolved" {
		t.Fatalf("warnings = %v, want one template.warn.unresolved", warns)
	}
}

package common

import (
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/template"
)

// ---------------------------------------------------------------------------
// 渲染模式映射测试: 两个已定义模式各走一路, 未知取值落到 Strict
//
// Render-mode mapping tests: each defined mode takes its own branch and an unknown value lands on
// Strict
// ---------------------------------------------------------------------------

func TestTemplateMode(t *testing.T) {
	cases := []struct {
		name string
		in   channel.RenderMode
		want template.Mode
	}{
		{"strict", channel.RenderStrict, template.Strict},
		{"check", channel.RenderCheck, template.Check},
		{"zero value", channel.RenderMode(0), template.Strict},
		{"unknown beyond defined", channel.RenderMode(42), template.Strict},
		{"unknown negative", channel.RenderMode(-1), template.Strict},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := TemplateMode(c.in); got != c.want {
				t.Errorf("TemplateMode(%d) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

// TestTemplateModeFeedsRenderWithMode 用一次真实渲染确认映射后的模式行为正确:
// Strict 下未解析变量报错, Check 下保留原文并告警
//
// TestTemplateModeFeedsRenderWithMode renders once for real to confirm the mapped mode behaves
// right: Strict reports an unresolved variable while Check keeps the text and warns
func TestTemplateModeFeedsRenderWithMode(t *testing.T) {
	vars := template.NewVars()

	strict, warns, err := template.RenderWithMode("a{{missing}}b", vars, TemplateMode(channel.RenderStrict))
	if err == nil {
		t.Fatalf("strict render error = nil, want an unresolved-variable error (out %q)", strict)
	}
	if warns != nil {
		t.Errorf("strict render warnings = %v, want none", warns)
	}

	check, warns, err := template.RenderWithMode("a{{missing}}b", vars, TemplateMode(channel.RenderCheck))
	if err != nil {
		t.Fatalf("check render error: %v", err)
	}
	if check != "a{{missing}}b" {
		t.Errorf("check render out = %q, want the text kept", check)
	}
	if len(warns) == 0 {
		t.Error("check render warnings = none, want one for the unresolved variable")
	}
}

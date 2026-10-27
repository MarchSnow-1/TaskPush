package common

import (
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/template"
)

// ---------------------------------------------------------------------------
// 渲染模式转换: RenderMode 定义在 channel 包, template.Mode 定义在 template 包, 渠道每次渲染都
// 要一个 switch, 这里收拢成一处, 免得九个渠道各写一遍
//
// Render-mode conversion: RenderMode lives in the channel package and template.Mode in the
// template package, and every channel needs the same switch before each render, so it is gathered
// here once instead of nine times over
// ---------------------------------------------------------------------------

// TemplateMode 把渠道的渲染模式映射为模板引擎的模式
// RenderStrict → Strict, RenderCheck → Check
// 未知取值落到 Strict, 因为它是零值语义: 宁可多报一个错误, 也不把未解析变量当正文发出去
//
// TemplateMode maps a channel render mode onto the template engine mode
// RenderStrict becomes Strict and RenderCheck becomes Check
// An unknown value lands on Strict, which is what its zero value means: an extra error beats
// sending an unresolved variable out as message content
func TemplateMode(mode channel.RenderMode) template.Mode {
	switch mode {
	case channel.RenderCheck:
		return template.Check
	default:
		return template.Strict
	}
}

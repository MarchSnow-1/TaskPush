package serverchan

/*
serverchan 渠道的渲染层: settings 字符串与列表字段的渲染, 列表按分隔符切分与 | 连接,
以及模板错误与告警的字段路径改写
Prepare 与 Send 在 channel.go, 打码在 mask.go, 配置结构与路由判定在 settings.go

The rendering layer of the serverchan channel: rendering the string and list fields of settings,
cutting a list on its separators into one |-joined string, and rewriting the field path of a
template error or warning
Prepare and Send sit in channel.go, masking in mask.go, and the configuration shapes with the route
decision in settings.go
*/

import (
	"errors"
	"strconv"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/settings"
	"github.com/MarchSnow-1/TaskPush/internal/template"
)

// renderField 渲染一个 settings 字符串字段
// 模板错误与告警的 Path 先被改写为该字段路径, 因为模板引擎只填占位路径
//
// renderField renders one settings string field
// The Path inside a template error or warning is rewritten to this field path first, because the
// template engine only fills a placeholder path
func renderField(value, field string, vars *template.Vars, mode template.Mode) (string, []apperr.Problem, error) {
	out, warns, err := template.RenderWithMode(value, vars, mode)
	if err != nil {
		rewritePath(err, field)
		return "", nil, err
	}
	for i := range warns {
		setPath(&warns[i], field)
	}
	return out, warns, nil
}

// renderList 逐项渲染列表字段, 再把每一项按逗号类分隔符切成 | 连接的字符串
// 变量可以整体提供列表: "{{tags}}" 渲染成 "a,b" 后仍被切成 a|b; 单项渲染为空时跳过
//
// renderList renders a list field item by item and then cuts every item on the comma-like
// separators into one |-joined string
// A variable may supply the whole list, since "{{tags}}" rendering into "a,b" is still cut into
// a|b, while an item rendering empty is skipped
func renderList(values settings.StringList, field string, vars *template.Vars, mode template.Mode) (string, []apperr.Problem, error) {
	var warnings []apperr.Problem
	parts := make([]string, 0, len(values))
	for i, configured := range values {
		rendered, warns, err := renderField(configured, field+"."+strconv.Itoa(i), vars, mode)
		if err != nil {
			return "", warnings, err
		}
		warnings = append(warnings, warns...)
		parts = append(parts, splitPipeSeparated(rendered)...)
	}
	return strings.Join(parts, "|"), warnings, nil
}

// splitPipeSeparated 按 ASCII 逗号, 全角逗号, 顿号与竖线切分, 去空白与空项
// 该归一逻辑不属于 common 的对外契约, 因此在本包内保留一份局部实现
//
// splitPipeSeparated cuts on the ASCII comma, the full-width comma, the ideographic comma and the
// pipe, dropping whitespace and empty items
// This normalisation is not part of the exported common contract, so a local implementation stays
// in this package
func splitPipeSeparated(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', '，', '、', '|':
			return true
		}
		return false
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// rewritePath 把 err 链上的模板 Problem 的 Path 改写为字段路径
//
// rewritePath rewrites the Path of the template Problem on the err chain into a field path
func rewritePath(err error, field string) {
	var prob *apperr.Problem
	if errors.As(err, &prob) {
		setPath(prob, field)
	}
}

// setPath 覆写 Problem.Data 里的 Path, Data 为 nil 时先建表
//
// setPath overwrites Path inside Problem.Data, creating the map when it is nil
func setPath(prob *apperr.Problem, field string) {
	if prob == nil {
		return
	}
	if prob.Data == nil {
		prob.Data = map[string]any{}
	}
	prob.Data["Path"] = field
}

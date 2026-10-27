package wecom

/*
wecom 渠道的渲染层: text 与 markdown 请求体片段的类型, 正文按消息类型的字节上限截断,
截断警告的构造, 以及模板错误与警告的字段路径改写
渠道实现与出网发送在 channel.go, key 提取与遮蔽在 mask.go, 配置结构在 settings.go

The rendering layer of the wecom channel: the request body fragment types for text and markdown,
cutting a body at the byte cap of its message type, building the truncation warning, and rewriting
the field path of a template error or warning
The channel implementation with sending sits in channel.go, key extraction and masking in mask.go,
and the configuration shapes in settings.go
*/

import (
	"errors"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
	"github.com/MarchSnow-1/TaskPush/internal/template"
)

// contentPayload 是 text/markdown 字段的内容体
//
// contentPayload is the content object of the text and markdown fields
type contentPayload struct {
	Content string `json:"content"`
}

// sendPayload 是发送请求体: msgtype 决定 text 与 markdown 哪个字段出现
//
// sendPayload is the request body: msgtype decides whether text or markdown shows up
type sendPayload struct {
	MsgType  string          `json:"msgtype"`
	Text     *contentPayload `json:"text,omitempty"`
	Markdown *contentPayload `json:"markdown,omitempty"`
}

// buildContent 按企业微信的内容规则拼接标题与正文, 判空并按消息类型的字节上限截断
// 标题与正文已由调度层渲染, 这里只做拼接, 判空与截断
// 规格不 TrimSpace: 只含空白的正文按原样发送
//
// buildContent joins the title and the body under the WeCom content rule, checks emptiness and cuts
// the result at the byte cap of the message type
// The scheduling layer has already rendered the title and the body, so this step only joins, checks
// and cuts them
// The specification does not trim: a whitespace-only body goes out as written
func buildContent(msg channel.Message, msgType string) (string, []apperr.Problem, error) {
	content := common.JoinTitleBody(msg.Title, msg.Body)
	if content == "" {
		return "", nil, problem(keyErrorEmptyContent, nil)
	}

	limit := textLimitBytes
	if msgType == msgTypeMarkdown {
		limit = markdownLimitBytes
	}
	var warnings []apperr.Problem
	if clipped, truncated := common.TruncateBytes(content, limit); truncated {
		content = clipped
		warnings = append(warnings, apperr.Problem{
			ID:   keyWarnTruncated,
			Data: map[string]any{"Limit": limit},
		})
	}
	return content, warnings, nil
}

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

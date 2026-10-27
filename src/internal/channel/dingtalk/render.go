package dingtalk

/*
dingtalk 渠道的渲染层: text/markdown 请求体片段的类型, 内容构造, 截断与内容校验,
markdown 标题兜底, 以及模板错误与警告的字段路径改写
渠道实现与出网发送在 channel.go, 加签与遮蔽在 mask.go, 配置结构在 settings.go

The rendering layer of the dingtalk channel: the request body fragment types for text and markdown,
content construction, truncation and content checks, the markdown title fallback, and the rewriting
of the field path of a template error or warning
The channel implementation with sending sits in channel.go, signing and masking in mask.go, and the
configuration shapes in settings.go
*/

import (
	"errors"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
	"github.com/MarchSnow-1/TaskPush/internal/template"
)

// textPayload 是 text 消息的请求体片段
//
// textPayload is the request body fragment of a text message
type textPayload struct {
	Content string `json:"content"`
}

// markdownPayload 是 markdown 消息的请求体片段
//
// markdownPayload is the request body fragment of a markdown message
type markdownPayload struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

// atPayload 是 @所有人 的请求体片段
//
// atPayload is the request body fragment of the @all flag
type atPayload struct {
	IsAtAll bool `json:"isAtAll"`
}

// buildPayload 按 msgType 构造请求体片段并处理截断与内容校验
// 判空按渲染后的原文: 规格 4.1/4.2 写的是「两者都空」与「body 必填」, 因此只拒字面空串,
// 只含空白的内容按原样发送, 因为改写它属于自动改动用户内容
//
// buildPayload builds the request body fragment by msgType and handles truncation and content
// checks
// Emptiness is judged on the text as rendered: sections 4.1 and 4.2 say "both empty" and "the body
// is required", so only a literally empty value is refused and whitespace-only content goes out as
// written, since rewriting it would be an automatic change of the user's content
func buildPayload(msg channel.Message, msgType string, atAll bool, warnings []apperr.Problem) (*sendPayload, []apperr.Problem, error) {
	payload := &sendPayload{MsgType: msgType}
	switch msgType {
	case msgTypeMarkdown:
		if msg.Body == "" {
			return nil, warnings, problem(keyErrorEmptyMarkdown, nil)
		}
		body, truncated := common.TruncateBytes(msg.Body, contentLimit)
		if truncated {
			warnings = append(warnings, truncationWarning())
		}
		title := msg.Title
		if title == "" {
			title = markdownTitleFallback(msg)
		}
		payload.Markdown = &markdownPayload{Title: title, Text: body}
	case msgTypeText:
		content := common.JoinTitleBody(msg.Title, msg.Body)
		if content == "" {
			return nil, warnings, problem(keyErrorEmptyContent, nil)
		}
		if cut, truncated := common.TruncateBytes(content, contentLimit); truncated {
			content = cut
			warnings = append(warnings, truncationWarning())
		}
		payload.Text = &textPayload{Content: content}
		if atAll {
			payload.At = &atPayload{IsAtAll: true}
		}
	default:
		return nil, warnings, problem(keyErrorInvalidMsgType, map[string]any{"Type": msgType})
	}
	return payload, warnings, nil
}

// defaultMarkdownTitle 是 markdown 标题的兜底文案
// 优先用调度层下发的译文 (channel.dingtalk.default_markdown_title), 只有没接线或译文缺失时
// 才落到这个语言无关常量, 因此常量与 zh-CN 的键值保持一致
//
// defaultMarkdownTitle is the fallback markdown title copy
// The copy from the scheduling layer (channel.dingtalk.default_markdown_title) wins, and this
// language-neutral constant only serves a channel without a wiring or without that copy, so it
// matches the zh-CN key value
const defaultMarkdownTitle = "通知"

// markdownTitleFallback 取 markdown 标题的缺省文案: 先问译文, 拿不到再用常量
// 渠道不导入 i18n, 译文经 Message.Translate 下发
//
// markdownTitleFallback picks the default markdown title copy, asking the lookup first and falling
// back to the constant
// The channel does not import i18n, and the copy arrives through Message.Translate
func markdownTitleFallback(msg channel.Message) string {
	if msg.Translate == nil {
		return defaultMarkdownTitle
	}
	if copyText := strings.TrimSpace(msg.Translate(keyDefaultMarkdownText)); copyText != "" {
		return copyText
	}
	return defaultMarkdownTitle
}

// truncationWarning 构造内容截断警告, Limit 为字节上限
//
// truncationWarning builds the truncation warning with the byte cap in Limit
func truncationWarning() apperr.Problem {
	return apperr.Problem{ID: keyWarnTruncated, Data: map[string]any{"Limit": contentLimit}}
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

// problem 构造只带消息键与可选 Data 的 Problem
//
// problem builds a Problem holding a message key and optional Data
func problem(id string, data map[string]any) *apperr.Problem {
	return &apperr.Problem{ID: id, Data: data}
}

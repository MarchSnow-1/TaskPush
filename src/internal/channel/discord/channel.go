package discord

/*
discord 渠道的渠道实现: Channel 与 Prepared 的类型, Prepare 与 Send, 以及状态码错误的重包
渲染与字段路径改写在 render.go, 打码在 mask.go, 配置结构在 settings.go

The channel implementation of discord: the Channel and Prepared types, Prepare and Send, and the
re-wrapping of a status-code error
Rendering and field path rewriting sit in render.go, masking in mask.go, and the configuration
shapes in settings.go
*/

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
)

// ---------------------------------------------------------------------------
// Discord 渠道: 通过频道 Webhook 的 Execute Webhook 端点推送一条消息
// Prepare 完成渲染, 校验与请求体构造, 不发网络; content 按 UTF-16 code unit 截断到 2000,
// username 与 avatarUrl 只在渲染后非空时进入请求体
// webhook URL 与 token 只用于发送, 错误与预览里都不出现原样地址; Discord 对成功与失败统一返回
// 204 或非 2xx 状态码, 所以 Send 只看状态码, 不解析响应体
//
// The Discord channel: it pushes one message through the Execute Webhook endpoint of a channel
// webhook
// Prepare renders, validates and builds the request body without touching the network, cuts the
// content at 2000 UTF-16 code units, and writes username and avatarUrl only when they render
// non-empty
// The webhook URL and its token are used for sending only, and neither an error nor a preview
// writes the address as it came in; Discord answers with 204 or a non-2xx status for both outcomes,
// so Send reads the status and leaves the response body unparsed
// ---------------------------------------------------------------------------

// discordChannel 是一个已配置的 discord 渠道
//
// discordChannel is one configured discord channel
type discordChannel struct {
	id string
	s  discordSettings
}

// ID 返回渠道实例标识
//
// ID returns the channel instance identifier
func (c *discordChannel) ID() string { return c.id }

// Type 返回渠道类型名
//
// Type returns the channel type name
func (c *discordChannel) Type() string { return "discord" }

// preparedChannel 是一次 Prepare 的产物, 可被调度层重复 Send
//
// preparedChannel is the outcome of one Prepare and can be sent repeatedly by the scheduling layer
type preparedChannel struct {
	endpoint string
	body     []byte
	done     bool
}

// Prepare 渲染 settings, 校验内容与头像地址并构造请求体, 不做网络 I/O
// 返回的 warnings 覆盖模板未解析变量的告警与 content 截断告警
// content 按 UTF-16 code unit 截断到 2000: Discord 按该口径计长, 非 BMP 字符占两个单元
//
// Prepare renders the settings, validates the content and the avatar address, and builds the
// request body without network I/O
// The returned warnings cover an unresolved template variable and a truncated content
// The content is cut at 2000 UTF-16 code units, which is the measure Discord uses and counts a
// non-BMP character as two units
func (c *discordChannel) Prepare(_ context.Context, msg channel.Message) (channel.Prepared, []apperr.Problem, error) {
	mode := common.TemplateMode(msg.Mode)

	rawURL, warns, err := renderField(c.s.URL, settingsPathURL, msg.Vars, mode)
	if err != nil {
		return nil, nil, apperr.Permanent(err)
	}
	warnings := warns
	if strings.TrimSpace(rawURL) == "" {
		return nil, warnings, apperr.Permanent(problem(keyErrorMissingURL, nil))
	}
	endpoint, err := common.NormalizeURL(rawURL)
	if err != nil {
		return nil, warnings, apperr.Permanent(invalidURLProblem(rawURL, err))
	}

	username, warns, err := renderField(c.s.Username, settingsPathUsername, msg.Vars, mode)
	if err != nil {
		return nil, nil, apperr.Permanent(err)
	}
	warnings = append(warnings, warns...)

	avatarURL, warns, err := renderField(c.s.AvatarURL, settingsPathAvatarURL, msg.Vars, mode)
	if err != nil {
		return nil, nil, apperr.Permanent(err)
	}
	warnings = append(warnings, warns...)

	// 标题正文已由调度层渲染完成, 这里只按 Discord 的内容规则拼接与判空
	//
	// The scheduling layer has already rendered the title and the body, so this step only joins
	// and emptiness-checks them under the content rule of Discord
	content := common.JoinTitleBody(msg.Title, msg.Body)
	if content == "" {
		return nil, warnings, apperr.Permanent(problem(keyErrorEmptyContent, nil))
	}
	if clipped, truncated := common.TruncateUTF16(content, contentLimitUTF16); truncated {
		content = clipped
		warnings = append(warnings, apperr.Problem{
			ID:   keyWarnTruncated,
			Data: map[string]any{"Limit": contentLimitUTF16},
		})
	}

	payload := sendPayload{Content: content}
	// username 按 TrimSpace 后非空才发, 空值不进入请求体
	//
	// username goes into the request body only when it is non-empty after TrimSpace, so an empty
	// value sends no field
	if name := strings.TrimSpace(username); name != "" {
		payload.Username = name
	}
	// avatarUrl 非空时先按同一规则规范化, 再写入请求体; 规范化后仍非法则报 invalid_avatar_url
	//
	// A non-empty avatarUrl is normalised by the same rule before it enters the request body, and a
	// value still invalid after that reports invalid_avatar_url
	if strings.TrimSpace(avatarURL) != "" {
		normalizedAvatar, err := common.NormalizeURL(avatarURL)
		if err != nil {
			return nil, warnings, apperr.Permanent(invalidAvatarProblem(avatarURL, err))
		}
		payload.AvatarURL = normalizedAvatar
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, warnings, err
	}
	return &preparedChannel{endpoint: endpoint, body: body}, warnings, nil
}

// sendPayload 是 Execute Webhook 的请求体
// username 与 avatar_url 用 omitempty, 未渲染出值时不出现在报文里
//
// sendPayload is the Execute Webhook request body
// username and avatar_url use omitempty, so a value that did not render stays out of the request
type sendPayload struct {
	Content   string `json:"content"`
	Username  string `json:"username,omitempty"`
	AvatarURL string `json:"avatar_url,omitempty"`
}

// Preview 返回脱敏后的预览, 只带目标数量
// webhook URL, token 与正文都不进入 Data
//
// Preview returns the redacted preview carrying the target count only
// Neither the webhook URL, its token nor the content reaches Data
func (p *preparedChannel) Preview() apperr.Problem {
	return apperr.Problem{
		ID:   keyPreviewMessage,
		Data: map[string]any{"Targets": 1},
	}
}

// Send 向 webhook 端点 POST 一次请求体
// 状态码由 common.Check2xx 判定: 2xx (含 204 与空 body) 成功; 3xx 与 4xx (408/425/429 除外)
// 由它包成 apperr.Permanent, 429 与常规 5xx 保持可重试
// 非 2xx 的摘要取 common.error.http_status 的 Body, 再按 discord 的键重包一次; 这里不 sleep,
// 等待与重试计数由调度层负责
// 已成功的一次发送记进 done, 重复 Send 不再产生请求
//
// Send posts the request body to the webhook endpoint once
// common.Check2xx judges the status: a 2xx including 204 with an empty body is a success, while a
// 3xx or a 4xx other than 408, 425 and 429 is wrapped with apperr.Permanent, and 429 plus an
// ordinary 5xx stay retryable
// For a non-2xx status the summary of common.error.http_status is re-wrapped under the discord key;
// nothing sleeps here, since waiting and retry counting belong to the scheduling layer
// A successful send is recorded in done, so a repeated Send reaches no endpoint
func (p *preparedChannel) Send(ctx context.Context, client *http.Client) error {
	if p.done {
		return nil
	}
	// 请求体在 Prepare 阶段已构造完成, 这里用 DoRequest 直接发送字节, 不再经 DoJSON 二次序列化
	//
	// The request body was built during Prepare, so DoRequest sends those bytes directly instead of
	// serialising them once more through DoJSON
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(p.body))
	if err != nil {
		return common.SanitizeError(err)
	}
	req.Header.Set("Content-Type", contentTypeJSON)

	resp, err := common.DoRequest(ctx, client, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if err := common.Check2xx(resp); err != nil {
		return p.httpStatusError(err)
	}
	p.done = true
	return nil
}

// httpStatusError 把 common 的状态码错误重包成 discord 的键
// Status 与 Body 从原 Problem 的 Data 取出, Body 再过一次脱敏与 token 遮蔽, 因为服务端可能在其中
// 回显 webhook 地址; 原 Problem 的永久性判定保持不动
//
// httpStatusError re-wraps the status-code error of common under the discord key
// Status and Body come from the Data of the original Problem, and Body passes through redaction and
// token masking again because the server may echo the webhook address there; the permanence verdict
// of the original Problem stays as it is
func (p *preparedChannel) httpStatusError(err error) error {
	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		return common.SanitizeError(err)
	}
	body, _ := prob.Data["Body"].(string)
	wrapped := &apperr.Problem{
		ID: keyErrorHTTPStatus,
		Data: map[string]any{
			"Status": prob.Data["Status"],
			"Body":   p.maskServerText(body),
		},
		Cause: common.SanitizeError(err),
	}
	if apperr.IsPermanent(err) {
		return apperr.Permanent(wrapped)
	}
	return wrapped
}

// 接口实现检查: 编译期确认渠道与工厂满足契约
//
// Interface assertions: the compile-time checks confirm the channel and the factory satisfy the
// contract
var (
	_ channel.Channel  = (*discordChannel)(nil)
	_ channel.Prepared = (*preparedChannel)(nil)
	_ channel.Factory  = factory{}
)

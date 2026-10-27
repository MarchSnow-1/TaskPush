package serverchan

/*
serverchan 渠道的渠道实现: Channel 与 Prepared 的类型, Prepare 与 Send, 以及响应校验错误的改写
渲染与列表拼接在 render.go, 打码在 mask.go, 配置结构与路由判定在 settings.go

The channel implementation of serverchan: the Channel and Prepared types, Prepare and Send, and the
rewriting of a response-verification error
Rendering with the list joining sits in render.go, masking in mask.go, and the configuration shapes
with the route decision in settings.go
*/

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
)

// ---------------------------------------------------------------------------
// ServerChan 渠道: SendKey 前缀决定 SC3 还是 Turbo
// SC3 key 形如 sctp<uid>t..., 端点 https://<uid>.push.ft07.com/send/<token>.send
// 其它 key 走 Turbo, 端点 https://sctapi.ftqq.com/<token>.send
// Prepare 完成渲染, 校验与请求体构造, 不做网络 I/O; token 只进入 URL 与请求体,
// 不进入预览, 并在错误数据里只以掩码形式出现
// Turbo 标题按 32 个 rune 截断并发警告, SC3 标题按平台规则不截断
//
// The ServerChan channel: the SendKey prefix selects SC3 or Turbo
// An SC3 key looks like sctp<uid>t..., with the endpoint
// https://<uid>.push.ft07.com/send/<token>.send
// Any other key uses Turbo, with the endpoint https://sctapi.ftqq.com/<token>.send
// Prepare renders, validates and builds the request body without network I/O; the token reaches
// the URL and the request body only, never a preview, and appears in error data as a mask alone
// A Turbo title is cut at 32 runes with a warning, while an SC3 title is not cut under the
// platform rule
// ---------------------------------------------------------------------------

// serverchanChannel 是一个已配置的 serverchan 渠道
//
// serverchanChannel is one configured serverchan channel
type serverchanChannel struct {
	id string
	s  serverchanSettings
}

// ID 返回渠道实例标识
//
// ID returns the channel instance identifier
func (c *serverchanChannel) ID() string { return c.id }

// Type 返回渠道类型名
//
// Type returns the channel type name
func (c *serverchanChannel) Type() string { return "serverchan" }

// sendPayload 是发送请求体
// title 与 desp 始终出现, 因为平台接受空正文但要求标题字段存在
// short 用 omitempty 且两条路由都可发送; tags/channel/openid 只在 SC3 路由填入, 空值不发送
//
// sendPayload is the send request body
// title and desp are always present, since the platform accepts an empty body while the title field
// has to exist
// short uses omitempty and may go out on either route, while tags, channel and openid carry values
// on the SC3 route only and an empty one is not sent
type sendPayload struct {
	Title   string `json:"title"`
	Desp    string `json:"desp"`
	Tags    string `json:"tags,omitempty"`
	Short   string `json:"short,omitempty"`
	Channel string `json:"channel,omitempty"`
	OpenID  string `json:"openid,omitempty"`
}

// preparedChannel 是一次 Prepare 的产物, 可被调度层重复 Send
//
// preparedChannel is the outcome of one Prepare and can be sent repeatedly by the scheduling layer
type preparedChannel struct {
	endpoint string
	mode     string
	payload  sendPayload
	token    string
}

// Prepare 渲染 settings, 校验内容与路由并构造请求体, 不做网络 I/O
// tags/channels/openids 逐项渲染后按逗号类分隔符切成 | 连接的字符串, 且只在 SC3 路由进入请求体
// 返回的 warnings 覆盖模板未解析变量与 Turbo 标题截断
//
// Prepare renders the settings, validates content and routing and builds the request body without
// network I/O
// tags, channels and openids are rendered item by item and cut on the comma-like separators into
// one |-joined string, and they reach the request body on the SC3 route alone
// The returned warnings cover an unresolved template variable and a Turbo title cut
func (c *serverchanChannel) Prepare(_ context.Context, msg channel.Message) (channel.Prepared, []apperr.Problem, error) {
	mode := common.TemplateMode(msg.Mode)
	var warnings []apperr.Problem

	token, warns, err := renderField(c.s.Token, "settings.token", msg.Vars, mode)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}
	warnings = append(warnings, warns...)
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, warnings, apperr.Permanent(problem(keyErrorMissingToken, nil))
	}

	endpoint, route, err := serverchanEndpoint(token)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}

	short, warns, err := renderField(c.s.Short, "settings.short", msg.Vars, mode)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}
	warnings = append(warnings, warns...)

	tags, warns, err := renderList(c.s.Tags, "settings.tags", msg.Vars, mode)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}
	warnings = append(warnings, warns...)

	channels, warns, err := renderList(c.s.Channels, "settings.channels", msg.Vars, mode)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}
	warnings = append(warnings, warns...)

	openIDs, warns, err := renderList(c.s.OpenIDs, "settings.openids", msg.Vars, mode)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}
	warnings = append(warnings, warns...)

	// 标题必填, 正文可空: 规格明确不 TrimSpace 内容, 判空按调度层渲染结果原样判断
	// 只拒字面空串: 只含空白的标题按原样发送, 因为改写它属于"自动改写内容", 产品规格禁止这样做
	//
	// The title is required while the body is optional: the specification keeps content untrimmed,
	// so emptiness is judged on the rendered text as it arrived
	// Only a literally empty title is rejected: a whitespace-only title goes out as written, since
	// rewriting it would be an automatic content change, which the product spec forbids
	if msg.Title == "" {
		return nil, warnings, apperr.Permanent(problem(keyErrorEmptyTitle, nil))
	}
	title := msg.Title
	if route == modeTurbo {
		if clipped, truncated := common.TruncateRunes(title, turboTitleLimit); truncated {
			title = clipped
			warnings = append(warnings, apperr.Problem{
				ID:   keyWarnTitleCut,
				Data: map[string]any{"Limit": turboTitleLimit},
			})
		}
	}

	// tags/channel/openid 是 SC3 扩展: spec 第 2 节把它们标为 SC3/SCT 字段, 第 5 节的 Turbo 示例
	// 只有 title/desp, 因此 Turbo 路由不发送这三项; short 按第 2 节未限定路由, 两条路由都发
	// 空值由 omitempty 处理, 所以只有非空字段才进入请求体
	//
	// tags, channel and openid are SC3 extensions: spec section 2 labels them SC3/SCT fields and the
	// Turbo body of section 5 carries title and desp alone, so a Turbo route sends none of the three
	// short is not tied to a route by section 2 and goes out on both routes
	// Empty values are handled by omitempty, so only a non-empty field reaches the request body
	payload := sendPayload{
		Title: title,
		Desp:  msg.Body,
		Short: strings.TrimSpace(short),
	}
	if route == modeSC3 {
		payload.Tags = tags
		payload.Channel = channels
		payload.OpenID = openIDs
	}

	return &preparedChannel{
		endpoint: endpoint,
		mode:     route,
		payload:  payload,
		token:    token,
	}, warnings, nil
}

// Preview 返回脱敏后的预览, 只带路由模式
// token 与正文都不进入 Data; spec 第 8 节把 TitleBytes 列为可选, 因此这里不发它
//
// Preview returns the redacted preview carrying the route mode only
// Neither the token nor the body reaches Data; spec section 8 lists TitleBytes as optional, so it
// stays out
func (p *preparedChannel) Preview() apperr.Problem {
	return apperr.Problem{
		ID:   keyPreviewMessage,
		Data: map[string]any{"Mode": p.mode},
	}
}

// Send 发送一次请求并校验响应
// 请求错误先经 common.SanitizeError, HTTP 状态由 common.VerifyCode 判定, 业务码必须为 0
// 业务码与协议错误按渠道键改写, 让 CLI 渲染 serverchan 自己的文案; 永久性沿用 common 的判定
// 这里不 sleep 也不做重试计数, 等待与计数由调度层负责
//
// Send performs one request and verifies the response
// A request error passes through common.SanitizeError first, the HTTP status is judged by
// common.VerifyCode and the business code has to be 0
// A business-code or protocol error is rewritten under the channel keys so the CLI renders
// serverchan copy, while permanence keeps the verdict reached by common
// Nothing sleeps or counts retries here, since waiting and counting belong to the scheduling layer
func (p *preparedChannel) Send(ctx context.Context, client *http.Client) error {
	resp, err := common.DoJSON(ctx, client, http.MethodPost, p.endpoint, p.payload, nil)
	if err != nil {
		return common.SanitizeError(err)
	}
	defer resp.Body.Close()

	if err := common.VerifyCode(resp, 0); err != nil {
		return p.wrapVerifyError(err)
	}
	return nil
}

// wrapVerifyError 把 common.VerifyCode 的错误改写为 serverchan 的消息键
// 业务码错误携带 Code 与 Message, 协议错误携带 Detail; 其它错误 (HTTP 状态/响应过大) 保持原键
// 服务端回显的文本先过 SanitizeString 与 token 遮蔽, 避免响应里的凭据进入 Data
//
// wrapVerifyError rewrites the errors of common.VerifyCode under the serverchan keys
// A business-code error carries Code and Message, a protocol error carries Detail, and any other
// error (an HTTP status or an oversized response) keeps its original key
// Server-echoed text passes through SanitizeString and token masking, which keeps a credential in
// the response out of Data
func (p *preparedChannel) wrapVerifyError(err error) error {
	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		return common.SanitizeError(err)
	}

	switch prob.ID {
	case "common.error.business_code":
		code, _ := prob.Data["Code"].(int)
		message, _ := prob.Data["Message"].(string)
		wrapped := &apperr.Problem{
			ID: keyErrorAPICode,
			Data: map[string]any{
				"Code":    code,
				"Message": p.maskServerText(message),
			},
			Cause: err,
		}
		if apperr.IsPermanent(err) {
			return apperr.Permanent(wrapped)
		}
		return wrapped
	case "common.error.bad_json", "common.error.bad_response":
		detail, _ := prob.Data["Detail"].(string)
		if detail == "" {
			detail = "the response body is empty or malformed"
		}
		return &apperr.Problem{
			ID: keyErrorBadResponse,
			Data: map[string]any{
				"Detail": p.maskServerText(detail),
			},
			Cause: err,
		}
	default:
		// 其它键 (HTTP 状态/响应过大) 保持原键, 但 Data 里的字符串先遮蔽服务端可能的 token 回显
		//
		// Any other key (an HTTP status or an oversized response) keeps its ID, while the string
		// values in Data are masked first in case the server echoed the token
		data := make(map[string]any, len(prob.Data))
		for key, value := range prob.Data {
			if text, ok := value.(string); ok {
				data[key] = p.maskServerText(text)
				continue
			}
			data[key] = value
		}
		wrapped := &apperr.Problem{
			ID:    prob.ID,
			Data:  data,
			Cause: common.SanitizeError(err),
		}
		if apperr.IsPermanent(err) {
			return apperr.Permanent(wrapped)
		}
		return wrapped
	}
}

// 接口实现检查: 编译期确认渠道与工厂满足契约
//
// Interface assertions: the compile-time checks confirm the channel and the factory satisfy the
// contract
var (
	_ channel.Channel  = (*serverchanChannel)(nil)
	_ channel.Prepared = (*preparedChannel)(nil)
	_ channel.Factory  = factory{}
)

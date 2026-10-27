package ntfy

/*
ntfy 渠道的渠道实现: Channel 与 Prepared 的类型, Prepare, Send 以及状态码错误的再脱敏
渲染与内容判定在 render.go, 打码与 Problem 构造在 mask.go, 配置结构在 settings.go
标题走 Title header, 正文走请求体, 因此 Prepare 在这里把渲染结果装配成请求

The channel implementation of ntfy: the Channel and Prepared types, Prepare, Send and the second
redaction of a status-code error
Rendering and content decisions sit in render.go, masking and Problem construction in mask.go, and
the configuration shapes in settings.go
The title goes into the Title header and the body into the request body, so Prepare is where the
rendered results become a request
*/

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
)

// ntfyChannel 是一个已配置的 ntfy 渠道
//
// ntfyChannel is one configured ntfy channel
type ntfyChannel struct {
	id string
	s  ntfySettings
}

// ID 返回渠道实例标识
//
// ID returns the channel instance identifier
func (c *ntfyChannel) ID() string { return c.id }

// Type 返回渠道类型名
//
// Type returns the channel type name
func (c *ntfyChannel) Type() string { return typeName }

// preparedChannel 是一次 Prepare 的产物, 可被调度层重复 Send
// req 在 Prepare 构造完成, 每次 Send 从 body 重新装配可读的请求体, 因此重试时正文仍在
// done 记录已成功的一次发送, 重复 Send 不再产生第二个请求
//
// preparedChannel is the outcome of one Prepare and can be sent repeatedly by the scheduling layer
// req is built inside Prepare and every Send refits a readable body from bytes, so a retry still
// has its content
// done records a successful send, so a repeated Send reaches no second request
type preparedChannel struct {
	req   *http.Request
	body  []byte
	topic string
	token string
	done  bool
}

// Prepare 渲染 settings, 校验地址, topic, 内容, priority, tags 与 header 取值, 并构造请求
// 不做网络 I/O; 返回的 warnings 覆盖模板未解析变量与正文截断
// 正文取 body, body 为空时取 title, 两者都空报错, 且判定按渲染结果原样, 不做 TrimSpace
// 正文按 UTF-8 字节截断到 4096, 标题与标签只受模板上限约束, 由平台拒绝超长值
//
// Prepare renders the settings, checks the address, topic, content, priority, tags and header
// values, and builds the request without network I/O
// The returned warnings cover an unresolved template variable and a cut body
// The content is the body, or the title when the body is empty; both empty is an error, and the
// test runs on the rendered text as it arrived, without TrimSpace
// The body is cut at 4096 UTF-8 bytes, while the title and the tags stay bounded by the template
// limit alone, leaving an oversized value to the platform
func (c *ntfyChannel) Prepare(ctx context.Context, msg channel.Message) (channel.Prepared, []apperr.Problem, error) {
	mode := common.TemplateMode(msg.Mode)
	var warnings []apperr.Problem

	rawURL, warns, err := renderField(c.s.URL, settingsPathURL, msg.Vars, mode)
	warnings = append(warnings, warns...)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}
	if strings.TrimSpace(rawURL) == "" {
		return nil, warnings, apperr.Permanent(problem(keyErrorMissingURL, nil))
	}
	endpoint, topic, err := topicEndpoint(rawURL)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}

	token, warns, err := renderField(c.s.Token, settingsPathToken, msg.Vars, mode)
	warnings = append(warnings, warns...)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}

	priority, warns, err := c.renderPriority(msg.Vars, mode)
	warnings = append(warnings, warns...)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}

	tags, warns, err := c.renderTags(msg.Vars, mode)
	warnings = append(warnings, warns...)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}

	// 正文为空时退回标题: ntfy 至少要有一个可读的 body, 否则订阅端只会看到空通知
	//
	// The title stands in when the body is empty: ntfy needs at least some readable body, or the
	// subscriber sees an empty notification
	content := msg.Body
	if content == "" {
		content = msg.Title
	}
	if content == "" {
		return nil, warnings, apperr.Permanent(problem(keyErrorEmptyContent, nil))
	}
	bodyBytes := []byte(content)
	if clipped, truncated := common.TruncateBytes(content, bodyLimitBytes); truncated {
		bodyBytes = []byte(clipped)
		warnings = append(warnings, apperr.Problem{
			ID:   keyWarnTruncated,
			Data: map[string]any{"Limit": bodyLimitBytes},
		})
	}

	headers := map[string]string{}
	if msg.Title != "" {
		headers[headerTitle] = msg.Title
	}
	if priority != "" {
		headers[headerPriority] = priority
	}
	if tags != "" {
		headers[headerTags] = tags
	}
	if strings.TrimSpace(token) != "" {
		headers[headerAuthorization] = bearerPrefix + strings.TrimSpace(token)
	}
	// 控制字符校验前移到 Prepare, 让 dry-run 与真实发送判定一致, 因为 CR/LF 到 client.Do 才被拒
	//
	// The control-character check runs here so a dry-run and a real send judge alike, since CR and
	// LF are refused only at client.Do
	if name, ok := invalidHeaderName(headers); !ok {
		return nil, warnings, apperr.Permanent(problem(keyErrorInvalidHeader, map[string]any{"Name": name}))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		// 构造失败只可能来自地址里的非法字符, 因为 method 与 body 都是本地定值
		// A failure here can only come from an illegal character in the address, since the method
		// and the body are fixed locally
		return nil, warnings, apperr.Permanent(invalidURLProblem(rawURL, common.SanitizeError(err)))
	}
	req.ContentLength = int64(len(bodyBytes))
	req.Header.Set("Content-Type", contentTypeText)
	for _, name := range sortedHeaderNames(headers) {
		req.Header.Set(name, headers[name])
	}

	return &preparedChannel{
		req:   req,
		body:  bodyBytes,
		topic: topic,
		token: strings.TrimSpace(token),
	}, warnings, nil
}

// Preview 返回脱敏后的预览, 只带 topic 与正文字节数
// topic 取 URL path 末段, 完整地址, token 与 header 值都不进入 Data
//
// Preview returns the redacted preview carrying the topic and the body size
// The topic is the last path segment, and neither the full address, the token nor a header value
// reaches Data
func (p *preparedChannel) Preview() apperr.Problem {
	return apperr.Problem{
		ID: keyPreviewMessage,
		Data: map[string]any{
			"Topic": p.topic,
			"Bytes": len(p.body),
		},
	}
}

// Send 用传入的 client POST 一次纯文本并判定状态码
// 每次尝试都从保留的正文重建请求体, 所以调度层复用同一个 Prepared 重试时正文仍可读
// HTTP 状态由 common.Check2xx 判定: 2xx (含空 body) 成功, 非 2xx 的摘要由 common 取出后重包成
// 渠道自己的键; 永久性沿用 common 的区间判定, 因为 429 与常规 5xx 重发可能成功, 而 4xx 重发同样被拒
// 这里不 sleep, 也不做重试计数, 等待与计数属于调度层
//
// Send uses the supplied client to post the plain text once and judges the status code
// Every attempt rebuilds the request body from the retained bytes, so the body stays readable when
// the scheduling layer retries with the same Prepared
// common.Check2xx makes the verdict: a 2xx, empty body included, is a success, and the summary of
// any other status is taken from common and re-wrapped under the channel key
// The permanence decision of common is kept, since 429 and an ordinary 5xx may answer differently
// on a retry while a 4xx rejects the same request again
// Nothing sleeps and no retry is counted here; waiting and counting belong to the scheduling layer
func (p *preparedChannel) Send(ctx context.Context, client *http.Client) error {
	if client == nil {
		return errors.New("ntfy: nil HTTP client")
	}
	if p.done {
		return nil
	}

	req := p.req.Clone(ctx)
	req.Body = io.NopCloser(bytes.NewReader(p.body))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(p.body)), nil
	}
	req.ContentLength = int64(len(p.body))

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

// 接口实现检查: 编译期确认渠道与工厂满足契约
//
// Interface assertions: the compile-time checks confirm the channel and the factory satisfy the
// contract
var (
	_ channel.Channel  = (*ntfyChannel)(nil)
	_ channel.Prepared = (*preparedChannel)(nil)
	_ channel.Factory  = factory{}
)

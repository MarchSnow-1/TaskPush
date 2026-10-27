package webhook

/*
webhook 渠道的渠道实现: Channel 与 Prepared 的类型, Prepare 与 Send 以及状态码错误的再脱敏
渲染与请求构造在 render.go, 打码在 mask.go, 配置结构在 settings.go

The channel implementation of webhook: the Channel and Prepared types, Prepare and Send, and the
second redaction of a status-code error
Rendering and request construction sit in render.go, masking in mask.go, and the configuration
shapes in settings.go
*/

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
)

// webhookChannel 是一个已配置的 webhook 渠道
//
// webhookChannel is one configured webhook channel
type webhookChannel struct {
	id       string
	settings settings
}

// ID 返回渠道实例标识
//
// ID returns the channel instance identifier
func (c *webhookChannel) ID() string { return c.id }

// Type 返回渠道类型名
//
// Type returns the channel type name
func (c *webhookChannel) Type() string { return "webhook" }

// preparedChannel 是一次 Prepare 的产物
// req 是请求模板, body 单独保留一份, 让重复 Send 能为每次尝试重建可读的正文
// secrets 是本渠道已知的凭据取值, 用于遮蔽服务端在错误响应体里的回显
//
// preparedChannel is the outcome of one Prepare
// req is the request template and body is kept separately, which lets a repeated Send rebuild a
// readable body for every attempt
// secrets holds the credential values this channel already knows, used to mask what a server echoes
// back inside an error body
type preparedChannel struct {
	req     *http.Request
	body    []byte
	method  string
	target  string
	format  string
	bytes   int
	secrets []string
	// urlSecrets 是地址 path 里被当作凭据的段, 供 Preview 在 MaskURL 结果上再打码
	// 与 secrets 分开保存, 因为 headers 的值只会出现在服务端回显里, 不参与地址打码
	//
	// urlSecrets holds the path segments treated as credentials, which Preview masks on top of the
	// MaskURL result
	// It stays apart from secrets because a header value only appears in what a server echoes back
	// and takes no part in masking the address
	urlSecrets []string
	// previewURL 是 Preview 要显示的地址
	// 在 Prepare 里一次算好: maskSecrets 的匹配结果与随之产生的告警都要在 Prepare 阶段定下来,
	// Preview 只做读取
	//
	// previewURL is the address Preview shows
	// It is computed once in Prepare, since the maskSecrets match and the warnings it produces have to
	// be settled there, and Preview only reads it
	previewURL string
}

// Prepare 渲染 settings, 校验内容并构造 *http.Request, 不做网络 I/O
// 渲染错误与警告的 Path 会改写为 settings.<field>, 便于用户定位到具体字段
//
// Prepare renders the settings, validates the content and builds the *http.Request without network
// I/O
// The Path inside a render error or warning is rewritten to settings.<field>, which points the
// user at the exact field
func (c *webhookChannel) Prepare(ctx context.Context, msg channel.Message) (channel.Prepared, []apperr.Problem, error) {
	mode := common.TemplateMode(msg.Mode)
	var warnings []apperr.Problem

	rawURL, warns, err := renderField(c.settings.URL, "settings.url", msg.Vars, mode)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}
	warnings = append(warnings, warns...)
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, warnings, apperr.Permanent(problem(keyErrorMissingURL, nil))
	}
	endpoint, err := common.NormalizeURL(rawURL)
	if err != nil {
		return nil, warnings, apperr.Permanent(invalidURLProblem(rawURL, err))
	}

	method, warns, err := renderField(c.settings.Method, "settings.method", msg.Vars, mode)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}
	warnings = append(warnings, warns...)
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		method = defaultMethod
	}
	if !common.ValidHeaderName(method) {
		return nil, warnings, apperr.Permanent(problem(keyErrorInvalidMethod, map[string]any{"Method": method}))
	}

	renderedHeaders, headerWarns, err := renderHeaders(c.settings.Headers, msg.Vars, mode)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}
	warnings = append(warnings, headerWarns...)

	bodyBytes, format, contentType, bodyWarns, err := renderBody(c.settings.Body, msg.Vars, mode)
	if err != nil {
		return nil, warnings, apperr.Permanent(err)
	}
	warnings = append(warnings, bodyWarns...)

	headerCT, hasHeaderCT := headerValue(renderedHeaders, "Content-Type")
	if hasHeaderCT {
		contentType = headerCT
	}
	if isJSONContentType(contentType) && !json.Valid(bodyBytes) {
		return nil, warnings, apperr.Permanent(problem(keyErrorInvalidJSONBody, map[string]any{
			"Detail": "the rendered body is not valid JSON",
		}))
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, bodyReader(bodyBytes))
	if err != nil {
		// 请求构造失败来自 method 或 URL 的非法 token, method 在此处已渲染定稿, 因此报 invalid_method
		// A failure here comes from an illegal method or URL token, and the method is settled by now,
		// so invalid_method is reported
		return nil, warnings, apperr.Permanent(invalidMethodProblem(method, common.SanitizeError(err)))
	}

	// Host 与传输层管理的特殊头单独处理, 其余头通过 Header.Set 写入
	// Host and the transport-managed special headers are handled separately, while the rest are
	// written through Header.Set
	if err := applyHeaders(req, renderedHeaders, len(bodyBytes)); err != nil {
		return nil, warnings, apperr.Permanent(err)
	}
	if contentType != "" && (len(bodyBytes) > 0 || c.settings.Body != nil || hasHeaderCT) {
		req.Header.Set("Content-Type", contentType)
	}

	// 凭据先收集齐再交给 preparedChannel: 服务端在错误响应体里回显的就是这些渲染后的取值
	// 地址一侧覆盖 userinfo, 敏感 query 与 path 末段, 后者与 discord 的 webhook 形态一致
	//
	// The credentials are collected before preparedChannel receives them, since the rendered values
	// are exactly what a server echoes inside an error body
	// On the address side the userinfo, the sensitive query parameters and the trailing path segment
	// are covered, the last of which matches the webhook shape of the discord channel
	secrets := headerSecrets(renderedHeaders)
	secrets = append(secrets, endpointSecrets(endpoint)...)

	// 预览地址在 Prepare 里定稿: maskSecrets 的匹配结果与失配告警都要在这里产出,
	// 让 Preview 保持纯读取, 并让告警随渠道警告一起上送到 CLI
	//
	// The preview address is settled in Prepare, where the maskSecrets match and the mismatch
	// warnings belong, which keeps Preview a pure read and lets the warnings reach the CLI together
	// with the other channel warnings
	previewURL, maskWarnings := maskRenderedURL(endpoint, c.settings.MaskSecrets)
	warnings = append(warnings, maskWarnings...)

	return &preparedChannel{
		req:        req,
		body:       bodyBytes,
		method:     method,
		target:     endpoint,
		format:     format,
		bytes:      len(bodyBytes),
		secrets:    secrets,
		urlSecrets: pathCredentialSegments(endpoint),
		previewURL: previewURL,
	}, warnings, nil
}

// Preview 返回脱敏后的预览, 只带方法, 掩码地址, 格式与字节数
// header 值与正文都不进入 Data
//
// Preview returns the redacted preview carrying the method, masked address, format and byte count
// Neither a header value nor the body reaches Data
func (p *preparedChannel) Preview() apperr.Problem {
	return apperr.Problem{
		ID: keyPreviewHTTP,
		Data: map[string]any{
			"Method":  p.method,
			"URL":     p.previewURL,
			"Format":  p.format,
			"Bytes":   p.bytes,
			"Targets": 1,
		},
	}
}

// Send 使用传入的 client 发送一次请求并判定响应
// 每次尝试都从保留的正文重建请求体, 所以调度层复用同一个 Prepared 重试时正文仍可读
// 这里不重试, 不 sleep, 也不创建全局 client; 网络错误与响应摘要都经 common 层脱敏与分类
// 非 2xx 的摘要再经 maskStatusError 遮蔽服务端回显的凭据, 因为 common.Check2xx 只认 URL 形态
//
// Send uses the supplied client to make one request and judges the response
// Every attempt rebuilds the request body from the retained bytes, so the body stays readable when
// the scheduling layer retries with the same Prepared
// It neither retries, sleeps nor creates a global client, and both the network error and the
// response summary pass through the redaction and classification of the common layer
// The non-2xx summary then passes through maskStatusError, which hides the credentials a server
// echoes back, since common.Check2xx recognises the URL form alone
func (p *preparedChannel) Send(ctx context.Context, client *http.Client) error {
	if client == nil {
		return errors.New("webhook: nil HTTP client")
	}
	req := p.req.Clone(ctx)
	if len(p.body) > 0 {
		req.Body = io.NopCloser(bytes.NewReader(p.body))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(p.body)), nil
		}
		req.ContentLength = int64(len(p.body))
	}

	resp, err := common.DoRequest(ctx, client, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := common.Check2xx(resp); err != nil {
		return p.maskStatusError(err)
	}
	return nil
}

// maskStatusError 把 common.Check2xx 的状态码错误里的响应体摘要再遮蔽一次
// 服务端可能把收到的 header 值或地址里的凭据回显在错误响应体里, 而 common 的摘要只按
// scheme://... 形态打码, 裸 token 会原样留下
// 重包时 ID 沿用 common.error.http_status, Data 保留 Status 与处理后的 Body, Cause 置 nil:
// 内层 Problem 放进 Cause 会让渲染层把 "common.error.http_status" 这个内部键追加到用户文案尾部
// 永久性沿用原判定, 因此 4xx 与 3xx 仍然不可重试
// 不是 Problem 的错误走这个兜底分支, 经 SanitizeError 返回, 保持原有的脱敏
//
// maskStatusError masks the response-body summary inside the status-code error of common.Check2xx a
// second time
// A server may echo the header values it received, or a credential from the address, inside an error
// body, while the common summary masks the scheme://... form alone and leaves a bare token as it is
// The re-wrap keeps the ID common.error.http_status, keeps Status and the processed Body in Data, and
// sets Cause to nil: putting the inner Problem into Cause would make the rendering layer append the
// internal key "common.error.http_status" to the user copy
// Permanence follows the original verdict, so a 4xx and a 3xx stay non-retryable
// An error that is not a Problem takes this fallback branch and returns through SanitizeError, which
// keeps its existing redaction
func (p *preparedChannel) maskStatusError(err error) error {
	var prob *apperr.Problem
	if !errors.As(err, &prob) {
		return common.SanitizeError(err)
	}
	body, _ := prob.Data["Body"].(string)
	wrapped := &apperr.Problem{
		ID: "common.error.http_status",
		Data: map[string]any{
			"Status": prob.Data["Status"],
			"Body":   common.MaskInText(body, p.secrets...),
		},
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
	_ channel.Channel  = (*webhookChannel)(nil)
	_ channel.Prepared = (*preparedChannel)(nil)
	_ channel.Factory  = factory{}
)

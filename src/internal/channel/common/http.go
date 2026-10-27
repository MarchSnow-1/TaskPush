package common

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
)

// ---------------------------------------------------------------------------
// HTTP 公共层: 负责发送 (DoJSON/DoRequest) 与响应判定 (Check2xx/VerifyCode/VerifyErrcode/VerifyOK/ReadBody)
// 这一层不认识任何具体渠道, 只提供所有渠道共用的发送与判定流程
//
// 状态码分类边界 (Check2xx):
//   - 3xx 与 4xx (408/425/429 除外) 走 apperr.Permanent, 因为同一请求重发到同一端点会拿到同一状态码,
//     重试不改变判定; 客户端不跟随重定向, 所以 3xx 是真实终点而非中间态
//   - 408/425/429 与其余 5xx 不包装, 保持可重试, 等的是限流窗口过去或服务恢复
//   - 501/505/511 说明端点不支持该方法/版本/网关行为, 重试不改变结果, 走 apperr.Permanent
//   - 1xx 与 5xx 之外的其他异常区间按可重试处理, 因为这里没有可靠的判定依据
//
// The shared HTTP layer: sending (DoJSON/DoRequest) and response checks
// (Check2xx/VerifyCode/VerifyErrcode/VerifyOK/ReadBody)
// Nothing here knows a concrete channel; it offers only the sending and judging flow every channel
// shares
//
// The status-code boundary in Check2xx:
//   - 3xx and 4xx (except 408/425/429) are wrapped with apperr.Permanent, because resending the
//     same request to the same endpoint returns the same code, so a retry cannot change the
//     verdict; the client does not follow redirects, so a 3xx is a real end state, not a hop
//   - 408/425/429 and the remaining 5xx stay unwrapped and retryable, waiting out a rate-limit
//     window or a service recovery
//   - 501/505/511 say the endpoint does not support the method, version or gateway behaviour, so
//     apperr.Permanent applies for the same reason
//   - 1xx and any other out-of-range code is left retryable, since there is no evidence to judge
//     it either way
// ---------------------------------------------------------------------------

const (
	// maxHTTPBodyBytes 是响应体读取上限 1MB, 超限由 ReadBody 报错而不是静默截断
	// maxHTTPBodyBytes is the 1MB response body read cap; past it ReadBody reports an error
	// instead of truncating in silence
	maxHTTPBodyBytes = 1 << 20

	// bodySummaryLimit 是错误摘要里保留的最多字符数 (按 rune 计), 防止整页 HTML 进日志
	// bodySummaryLimit is the most characters (counted in runes) an error summary keeps, so a
	// whole HTML page does not end up in the log
	bodySummaryLimit = 200

	// defaultJSONContentType 是 DoJSON 在 headers 未指定 Content-Type 时补的默认值
	// defaultJSONContentType is what DoJSON sets when headers carry no Content-Type
	defaultJSONContentType = "application/json; charset=utf-8"
)

// transientBizCodes 收录"HTTP 2xx + 业务码非预期"里属于限流的业务码, 这类失败保留重试窗口
// 已知条目: 企业微信机器人 45009 表示每机器人每分钟调用超限, 钉钉群机器人 410100 表示发送过快
// 其余确定性业务码 (坏 key/token/签名) 走 apperr.Permanent, 重发同样的请求得到同样的拒绝
// 注意本表按数字全局生效, 而各平台错误码空间并不共享, 新增条目前需确认该码在所有使用
// VerifyCode/VerifyErrcode 的渠道里都表示限流, 否则应把瞬态集合改为按渠道传入
//
// transientBizCodes holds the throttling codes that arrive as "HTTP 2xx with an unexpected
// business code", which keep their retry window
// Known entries: WeCom bot 45009 is the per-bot per-minute call cap, DingTalk group bot 410100
// means messages are sent too fast
// Any other business code goes through apperr.Permanent, because a bad key, token or signature
// rejects the same request the same way
// Note that this table keys on the number alone while platforms do not share a code space, so a
// new entry must be checked against every channel using VerifyCode/VerifyErrcode, otherwise the
// transient set should become a per-channel argument
var transientBizCodes = map[int]bool{
	45009:  true, // 企业微信机器人: 接口调用频次超限 / WeCom bot: call frequency exceeded
	410100: true, // 钉钉群机器人: 发送速度太快 / DingTalk group bot: sending too fast
}

// DoJSON 把 payload 序列化为 JSON 并发送, 返回未关闭的响应, 由调用方决定校验与关闭
// 序列化用标准 json.Marshal, HTML 转义保持开启; 需要关闭转义的调用方先用 MarshalNoHTMLEscape
// 得到字节再走 DoRequest, 这样 DoJSON 的入参语义在这里保持单一
// Content-Type 默认 application/json; charset=utf-8, headers 里已有该键 (大小写不敏感) 时不覆盖
// client.Do 的错误经 SanitizeError 处理, 以免带凭据的 URL 进日志; Unwrap 链保留, errors.Is 仍可用
//
// DoJSON serializes payload as JSON, sends it and returns the response unclosed, so the caller
// decides how to check and close it
// Serialization uses the standard json.Marshal, which keeps HTML escaping on; a caller that
// wants escaping off runs MarshalNoHTMLEscape first and goes through DoRequest, so the input
// meaning of DoJSON stays single
// Content-Type defaults to application/json; charset=utf-8 and is left alone when headers already
// carry that key (matched case-insensitively)
// The error from client.Do passes through SanitizeError so a URL holding credentials does not
// reach the log, and the Unwrap chain stays intact for errors.Is
func DoJSON(ctx context.Context, client *http.Client, method, url string, payload any, headers map[string]string) (*http.Response, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		// 序列化错误来自调用方构造的 payload, 文本不含 URL, 原样返回, 不占用未注册的错误键
		//
		// A marshal error comes from the payload the caller built, its text holds no URL, so it
		// is returned as is rather than through a key that is not registered
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(data))
	if err != nil {
		return nil, SanitizeError(err)
	}
	if !hasContentType(headers) {
		req.Header.Set("Content-Type", defaultJSONContentType)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, SanitizeError(err)
	}
	return resp, nil
}

// DoRequest 发送调用方构造好的请求, 只做 client.Do 与错误脱敏
// req 自带 context, 这里的 ctx 参数只为与 DoJSON 保持签名对称, 不会改写 req 的 context
// 响应不自动关闭
//
// DoRequest sends a request the caller built and only wraps client.Do with error redaction
// req carries its own context; the ctx parameter exists to mirror the DoJSON signature and does
// not rewrite the context of req
// The response is not closed automatically
func DoRequest(ctx context.Context, client *http.Client, req *http.Request) (*http.Response, error) {
	_ = ctx
	resp, err := client.Do(req)
	if err != nil {
		return nil, SanitizeError(err)
	}
	return resp, nil
}

// hasContentType 判断 headers 是否已经给出 Content-Type, 键名按大小写不敏感匹配
//
// hasContentType reports whether headers already supplies a Content-Type, matching the key
// case-insensitively
func hasContentType(headers map[string]string) bool {
	for name := range headers {
		if strings.EqualFold(name, "Content-Type") {
			return true
		}
	}
	return false
}

// statusPermanent 报告状态码是否属于"重试不改变判定"的区间, 分类边界见文件头注释
//
// statusPermanent reports whether a status code sits in the "a retry cannot change the verdict"
// range; the boundary is described in the header comment of this file
func statusPermanent(code int) bool {
	switch {
	case code >= 300 && code < 400:
		return true
	case code >= 400 && code < 500:
		switch code {
		case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
			return false
		default:
			return true
		}
	case code >= 500 && code < 600:
		switch code {
		case http.StatusNotImplemented, http.StatusHTTPVersionNotSupported, http.StatusNetworkAuthenticationRequired:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

// Check2xx 判定 HTTP 状态码, 2xx 返回 nil
// 非 2xx 时先读 body (上限 1MB) 并取最多 200 字符摘要放进 common.error.http_status 的 Data.Body,
// 因为服务端常把真实病因写在响应体里, 只报状态码会让排障看不见线索
// body 读取本身失败时不覆盖状态码信息: 摘要留空, 仍然返回 http_status 错误
// 摘要经 SanitizeString 处理, 服务端回显的带凭据 URL 不会进 Data
//
// Check2xx judges the HTTP status code and returns nil for 2xx
// For any other status it reads the body first (capped at 1MB) and puts a summary of at most 200
// characters into Data.Body of common.error.http_status, because servers usually write the real
// cause in the body and a bare status code leaves no clue for debugging
// A body read failure does not override the status information: the summary stays empty and the
// http_status error is still returned
// The summary goes through SanitizeString, so a URL with credentials echoed by the server cannot
// land in Data
func Check2xx(resp *http.Response) error {
	code := resp.StatusCode
	if code >= 200 && code < 300 {
		return nil
	}

	// 读取失败与超大都由这里降级成空摘要, 状态码判定比响应体片段更重要
	//
	// A read failure and an oversized body both degrade to an empty summary here, since the
	// status verdict matters more than a fragment of the body
	body, _ := ReadBody(resp)
	problem := &apperr.Problem{
		ID: "common.error.http_status",
		Data: map[string]any{
			"Status": code,
			"Body":   summarizeBody(body),
		},
	}
	if statusPermanent(code) {
		return apperr.Permanent(problem)
	}
	return problem
}

// summarizeBody 把响应体压成一行摘要: 去首尾空白, 按 rune 截到 200 字符, 再整体脱敏
//
// summarizeBody compresses a response body into a one-line summary: surrounding whitespace is
// trimmed, the text is cut to 200 runes, and the result is redacted as a whole
func summarizeBody(body []byte) string {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return ""
	}
	if r := []rune(s); len(r) > bodySummaryLimit {
		s = string(r[:bodySummaryLimit]) + "…"
	}
	return SanitizeString(s)
}

// ReadBody 读取响应体, 上限 1MB (实际用 LimitReader 读 1MB+1 以判断是否越界)
// 超过上限返回 common.error.body_too_large (Data.Limit), 而不是静默截断, 因为截断后的 JSON
// 无法解析业务码, 调用方拿到的"成功"会是假的
// 读取失败返回 common.error.bad_response, Cause 保留底层错误供 errors.Is 穿透
// 不关闭 body, 关闭由调用方负责
// resp.Body 为 nil 时返回空字节, 手工构造的响应 (无 body) 因此不会 panic
//
// ReadBody reads the response body with a 1MB cap (a LimitReader of 1MB+1 is what detects the
// overshoot)
// Past the cap it returns common.error.body_too_large (Data.Limit) instead of truncating
// silently, because truncated JSON cannot be parsed for a business code and the "success" the
// caller would see would be false
// A read failure returns common.error.bad_response with Cause keeping the underlying error
// reachable to errors.Is
// The body is not closed; closing is the caller's job
// A nil resp.Body yields empty bytes, so a hand-built response without a body does not panic
func ReadBody(resp *http.Response) ([]byte, error) {
	if resp.Body == nil {
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxHTTPBodyBytes+1))
	if err != nil {
		return nil, &apperr.Problem{
			ID: "common.error.bad_response",
			Data: map[string]any{
				"Field":  "body",
				"Detail": sanitizeDetail(err.Error()),
			},
			Cause: err,
		}
	}
	if len(data) > maxHTTPBodyBytes {
		return nil, &apperr.Problem{
			ID:   "common.error.body_too_large",
			Data: map[string]any{"Limit": maxHTTPBodyBytes},
		}
	}
	return data, nil
}

// apiEnvelope 汇总各平台响应体里可能出现的业务字段
// 指针用来区分"字段缺失"与"字段为零值": 期望成功码常是 0, 若按值解析, 缺字段会被读成 0 而误判成功
//
// apiEnvelope collects the business fields platforms may put in a response body
// The pointers tell a missing field apart from a zero value: the expected success code is often
// 0, so by-value decoding would read an absent field as a successful 0
type apiEnvelope struct {
	Code        *int   `json:"code"`
	ErrCode     *int   `json:"errcode"`
	ErrorCode   *int   `json:"error_code"`
	OK          *bool  `json:"ok"`
	Message     string `json:"message"`
	ErrMessage  string `json:"errmsg"`
	Description string `json:"description"`
	ErrText     string `json:"error"`
}

// codeMessage 取业务码旁边的可读文案, 优先级 message > errmsg > description > error
//
// codeMessage picks the readable text next to a business code, in the order message, errmsg,
// description, error
func (e apiEnvelope) codeMessage() string {
	for _, s := range []string{e.Message, e.ErrMessage, e.Description, e.ErrText} {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// okMessage 取 ok=false 旁边的可读文案, Telegram 用 description, 其他网关常用 error
//
// okMessage picks the readable text beside ok=false: Telegram uses description while other
// gateways usually use error
func (e apiEnvelope) okMessage() string {
	for _, s := range []string{e.Description, e.ErrText, e.Message, e.ErrMessage} {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// checkedBody 先做 Check2xx, 通过后读取响应体
// 非 2xx 时直接返回 Check2xx 的错误, body 已由那里读过, 不再二次读取
//
// checkedBody runs Check2xx first and reads the body only when the status passed
// For a non-2xx status the error from Check2xx is returned as is, since that path already read
// the body and a second read would yield nothing
func checkedBody(resp *http.Response) ([]byte, error) {
	if err := Check2xx(resp); err != nil {
		return nil, err
	}
	return ReadBody(resp)
}

// decodeEnvelope 把 2xx 响应体解进 apiEnvelope
// 空 body 返回 common.error.bad_response, 非法 JSON 返回 common.error.bad_json, 两者都是可重试的
// 普通错误: 中间层 (代理/网关) 偶尔会返回一个空壳或错误页, 下一次请求可能拿到真实端点的响应
// Detail 截到 200 字符并脱敏
//
// decodeEnvelope unmarshals a 2xx body into an apiEnvelope
// An empty body returns common.error.bad_response and invalid JSON returns common.error.bad_json,
// both as retryable plain errors: a middle layer (proxy or gateway) sometimes answers with an
// empty shell or an error page, and the next request may reach the real endpoint
// Detail is cut to 200 characters and redacted
func decodeEnvelope(body []byte, field string, env *apiEnvelope) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return &apperr.Problem{
			ID: "common.error.bad_response",
			Data: map[string]any{
				"Field":  field,
				"Detail": "empty response body",
			},
		}
	}
	if err := json.Unmarshal(body, env); err != nil {
		return &apperr.Problem{
			ID: "common.error.bad_json",
			Data: map[string]any{
				"Field":  field,
				"Detail": sanitizeDetail(err.Error()),
			},
			Cause: err,
		}
	}
	return nil
}

// missingFieldError 说明响应体是合法 JSON 却没有业务字段, Data 用 common.error.bad_response
// 返回可重试的普通错误: 字段缺失可能来自中间层返回的合法但无关的 JSON, 渠道若要按确定性失败
// 处理, 可以在自己的 Send 里再包一层 apperr.Permanent
//
// missingFieldError says the body is valid JSON without the expected business field, reported
// through common.error.bad_response
// It stays a retryable plain error: an absent field can come from a middle layer answering valid
// but unrelated JSON, and a channel that wants the deterministic reading can wrap it with
// apperr.Permanent inside its own Send
func missingFieldError(field string) error {
	return &apperr.Problem{
		ID: "common.error.bad_response",
		Data: map[string]any{
			"Field":  field,
			"Detail": "missing " + field + " field in response body",
		},
	}
}

// businessCodeError 构造 common.error.business_code, Data 放 Code 与 Message
// 限流类业务码 (见 transientBizCodes) 保持可重试, 其余走 apperr.Permanent:
// 2xx 加业务码是平台表达"请求被拒"的方式, 同样的请求重发会得到同样的拒绝
//
// businessCodeError builds common.error.business_code with Code and Message in Data
// A throttling code (see transientBizCodes) stays retryable while the rest go through
// apperr.Permanent: a 2xx plus a business code is how a platform says "the request was rejected",
// and resending it earns the same rejection
func businessCodeError(code int, message string) error {
	problem := &apperr.Problem{
		ID: "common.error.business_code",
		Data: map[string]any{
			"Code":    code,
			"Message": sanitizeDetail(message),
		},
	}
	if transientBizCodes[code] {
		return problem
	}
	return apperr.Permanent(problem)
}

// sanitizeDetail 清洗进入 Data 的服务端文本, 带凭据的 URL 一律打码
//
// sanitizeDetail cleans server-supplied text going into Data, so a URL holding credentials is
// masked
func sanitizeDetail(s string) string {
	if s == "" {
		return ""
	}
	if r := []rune(s); len(r) > bodySummaryLimit {
		s = string(r[:bodySummaryLimit]) + "…"
	}
	return SanitizeString(s)
}

// VerifyCode 先 Check2xx, 再解析 {"code":N} 并校验 N == want (ServerChan 一类)
// 业务码不符走 common.error.business_code (Data 含 Code 与 Message)
// 坏 JSON/空 body/缺字段见 decodeEnvelope 与 missingFieldError, 都是可重试的普通错误
// 不关闭 body, 由调用方负责
//
// VerifyCode runs Check2xx first, then parses {"code":N} and checks N against want (the
// ServerChan shape)
// A mismatching code goes through common.error.business_code with Code and Message in Data
// Invalid JSON, an empty body and a missing field are handled by decodeEnvelope and
// missingFieldError, all retryable plain errors
// The body is not closed; the caller owns that
func VerifyCode(resp *http.Response, want int) error {
	body, err := checkedBody(resp)
	if err != nil {
		return err
	}
	var env apiEnvelope
	if err := decodeEnvelope(body, "code", &env); err != nil {
		return err
	}
	if env.Code == nil {
		return missingFieldError("code")
	}
	if *env.Code == want {
		return nil
	}
	return businessCodeError(*env.Code, env.codeMessage())
}

// VerifyErrcode 先 Check2xx, 再解析 {"errcode":N} 并校验 N == want (企微/钉钉一类)
// 语义与 VerifyCode 相同, 只是字段名换成 errcode, 文案优先取 errmsg
//
// VerifyErrcode runs Check2xx first, then parses {"errcode":N} and checks N against want (the
// WeCom and DingTalk shape)
// The semantics match VerifyCode with the field renamed to errcode and errmsg preferred as the
// message
func VerifyErrcode(resp *http.Response, want int) error {
	body, err := checkedBody(resp)
	if err != nil {
		return err
	}
	var env apiEnvelope
	if err := decodeEnvelope(body, "errcode", &env); err != nil {
		return err
	}
	if env.ErrCode == nil {
		return missingFieldError("errcode")
	}
	if *env.ErrCode == want {
		return nil
	}
	return businessCodeError(*env.ErrCode, env.codeMessage())
}

// VerifyOK 先 Check2xx, 再解析 {"ok":true} (Telegram 一类)
// 204 与空 body 返回 nil: 空体是协议允许的无内容成功, 是否信任它由调用方按端点语义决定
// ok=false 走 common.error.business_code, Code 取 error_code (缺省为 0), Message 取 description
// 分类: error_code 为 0 时保持可重试; error_code 落在限流状态区间 (408/425/429 与常规 5xx) 时
// 可重试; 其余 (如 401/400) 走 apperr.Permanent, 因为那是鉴权或参数被确定的拒绝
// 这里复用 statusPermanent 的区间表, 因为 Telegram 用 HTTP 语义的 error_code 表达失败原因
//
// VerifyOK runs Check2xx first, then parses {"ok":true} (the Telegram shape)
// A 204 and an empty body return nil: an empty body is the protocol's allowed no-content success,
// and whether to trust it depends on the endpoint semantics the caller knows
// ok=false goes through common.error.business_code with Code taken from error_code (0 when
// absent) and Message from description
// The classification: error_code 0 stays retryable; a code in the rate-limit region (408/425/429
// or an ordinary 5xx) stays retryable too; any other value (401 or 400, for instance) goes
// through apperr.Permanent, since that is a deterministic rejection by authentication or arguments
// The region table of statusPermanent is reused here because Telegram expresses failure with an
// error_code that follows HTTP semantics
func VerifyOK(resp *http.Response) error {
	if err := Check2xx(resp); err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	body, err := ReadBody(resp)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}

	var env apiEnvelope
	if err := decodeEnvelope(body, "ok", &env); err != nil {
		return err
	}
	if env.OK == nil {
		return missingFieldError("ok")
	}
	if *env.OK {
		return nil
	}

	code := 0
	if env.ErrorCode != nil {
		code = *env.ErrorCode
	}
	message := env.okMessage()
	if message == "" {
		message = "ok=false"
	}
	problem := &apperr.Problem{
		ID: "common.error.business_code",
		Data: map[string]any{
			"Code":    code,
			"Message": sanitizeDetail(message),
		},
	}
	switch {
	case code == 0:
		return problem
	case transientBizCodes[code]:
		return problem
	case statusPermanent(code):
		return apperr.Permanent(problem)
	default:
		return problem
	}
}

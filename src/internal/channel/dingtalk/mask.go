package dingtalk

/*
dingtalk 渠道的遮蔽层: URL 加签 (timestamp 与 sign), access_token 的提取与校验,
服务端回显文本的遮蔽, 以及 URL 错误 Problem 的构造
内容构造在 render.go, 出网发送在 channel.go, 配置结构在 settings.go

The masking layer of the dingtalk channel: URL signing with the timestamp and the sign, the
extraction and the check of the access_token, the masking of server-echoed text, and the construction
of the URL error Problem
Content construction sits in render.go, sending in channel.go, and the configuration shapes in
settings.go
*/

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strconv"
	"time"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
)

// signEndpoint 给端点注入加签参数并返回新地址
// 用 url.Values.Set 替换已有的 timestamp/sign, 避免二次追加; 其它 query 参数
// (含 access_token) 与 fragment 原样保留
//
// signEndpoint injects the signing parameters and returns the new address
// url.Values.Set replaces an existing timestamp/sign rather than appending it a second time, while
// the other query parameters, access_token included, and the fragment stay as they were
func signEndpoint(endpoint, secret string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	timestamp := now.UnixMilli()
	q := u.Query()
	q.Set(signTimestampKey, strconv.FormatInt(timestamp, 10))
	q.Set(signValueKey, signValue(secret, timestamp))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// signValue 计算钉钉加签值: Base64(HMAC-SHA256(secret, timestamp + "\n" + secret))
// 返回未转义的 Base64: signEndpoint 走 url.Values.Encode, 它对每个取值做一次 url.QueryEscape,
// 所以 Base64 里的 + = / 在最终地址中就是转义形态, 这里再转义一次会变成 %252B 那种双重编码
//
// signValue computes the DingTalk signing value: Base64(HMAC-SHA256(secret,
// timestamp + "\n" + secret))
// The Base64 comes back unescaped: signEndpoint writes it through url.Values.Encode, which applies
// url.QueryEscape to every value once, so the + = / inside Base64 are escaped in the final address,
// and escaping here again would produce a double form such as %252B
func signValue(secret string, timestamp int64) string {
	stringToSign := strconv.FormatInt(timestamp, 10) + "\n" + secret
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// accessTokenOf 取出端点 query 里 access_token 的取值, 解析失败或缺失时返回空串
// 这里的地址已由 NormalizeURL 规范化并经 checkAccessToken 校验, 因此成功路径上取值非空
//
// accessTokenOf returns the access_token value from the endpoint query, and an empty string when the
// address fails to parse or carries no such parameter
// The address is normalised by NormalizeURL and checked by checkAccessToken already, so the value is
// non-empty on the success path
func accessTokenOf(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	return u.Query().Get(accessTokenKey)
}

// checkAccessToken 要求端点携带非空 access_token: 平台用它识别机器人, 缺失属于配置错误
//
// checkAccessToken requires a non-empty access_token: the platform identifies the robot through it,
// and an absent one is a configuration error
func checkAccessToken(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return problem(keyErrorInvalidURL, map[string]any{"URL": common.MaskURL(endpoint)})
	}
	if u.Query().Get(accessTokenKey) == "" {
		return problem(keyErrorMissingToken, nil)
	}
	return nil
}

// maskServerText 清洗服务端回显文本: URL 打码后再遮蔽 access_token 与加签 secret
// access_token 在请求 URL 的 query 里, 服务端会把它原样回显进错误响应体, 因此两者都要遮蔽
// 长的一项先替换: secret 与 access_token 互为前缀时, 短的先替换会让长的匹配失败
//
// maskServerText cleans server-echoed text: URLs are masked and then the access_token and the signing
// secret are hidden
// The access_token sits in the query of the request URL and a server echoes it as it arrived inside
// an error body, so both values need masking
// The longer value is replaced first, since a shorter one applied ahead of it would break the longer
// match when one is a prefix of the other
func (p *preparedChannel) maskServerText(text string) string {
	return common.MaskInText(text, p.secret, p.accessToken)
}

// invalidURLProblem 构造 URL 校验错误, Data.URL 先经 MaskURL, Cause 先经 SanitizeError
//
// invalidURLProblem builds the URL validation error, with Data.URL passing through MaskURL and
// Cause through SanitizeError
func invalidURLProblem(raw string, cause error) *apperr.Problem {
	return &apperr.Problem{
		ID:    keyErrorInvalidURL,
		Data:  map[string]any{"URL": common.MaskURL(raw)},
		Cause: common.SanitizeError(cause),
	}
}

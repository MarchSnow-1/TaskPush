package webhook

/*
webhook 渠道的脱敏层: 凭据收集, 预览地址的打码, maskSecrets 的逐段替换,
以及各类 Problem 的构造与错误文本的脱敏
渲染在 render.go, 出网发送在 channel.go, 配置结构在 settings.go

The masking layer of the webhook channel: collecting credentials, masking the preview address,
replacing maskSecrets fragment by fragment, and building the Problem values together with
redacting error text
Rendering sits in render.go, sending in channel.go, and the configuration shapes in settings.go
*/

import (
	"bytes"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
)

// maskValueText 是预览里替换凭据片段的文本, 与 common 的 maskValue 保持同一形态
//
// maskValueText replaces a credential fragment in the preview, matching the maskValue of common
const maskValueText = "***"

// headerSecrets 取出全部渲染后的 header 值, 排序保证结果稳定
// 值为空的不收集, MaskInText 也会跳过空串, 两处判定保持一致
//
// headerSecrets returns every rendered header value in a stable order
// An empty value is not collected, and MaskInText skips an empty secret too, which keeps both
// decisions in step
func headerSecrets(headers map[string]string) []string {
	out := make([]string, 0, len(headers))
	for _, value := range headers {
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// sensitiveQueryNames 是地址里取值需要遮蔽的 query 参数名, 比较时先转小写
// 与 common.MaskURL 打码的五个名字保持一致, common 的集合未导出, 因此这里另列一份,
// 并由 TestPrepareEndpointSecretsCoverSensitiveQuery 逐个确认 MaskURL 确实打码了它们,
// 集合漂移会让该测试失败
//
// sensitiveQueryNames holds the query parameter names whose value needs masking, compared after
// lowercasing
// The five names agree with what common.MaskURL hides; the common set is unexported, so this list is
// written out again and TestPrepareEndpointSecretsCoverSensitiveQuery confirms one by one that
// MaskURL does mask each of them, which makes a drift in either set fail that test
var sensitiveQueryNames = []string{"token", "key", "access_token", "sign", "secret"}

// passwordMaskingHint 描述地址 path 里凭据段的位置判断, 供 endpointSecrets 与 Preview 共用
// 已知的 webhook 形态在末段或末两段放凭据: Slack 是 /services/<a>/<b>/<secret>, Teams 与自建网关
// 常见 /hook/<secret>, 而 /notify 一类无语义的端点不应被过度打码
// 因此只有末段长度达到凭据下限且不是常见无语义词时才当作凭据
//
// passwordMaskingHint describes how the credential segment of an address path is recognised, shared
// by endpointSecrets and Preview
// A known webhook shape carries its credential in the last segment or the last two: Slack uses
// /services/<a>/<b>/<secret>, while Teams and self-hosted gateways often use /hook/<secret>, and a
// path without that meaning such as /notify must not be over-masked
// A trailing segment is therefore a credential only when it is long enough and is not one of the
// common meaningless tails
const (
	// minCredentialSegmentLen 是凭据段的最小长度, 低于它的末段多为动作名 (hook/notify/send)
	//
	// minCredentialSegmentLen is the shortest credential segment, and a shorter tail is usually an
	// action name such as hook, notify or send
	minCredentialSegmentLen = 16
)

// actionTailSegments 是常见的动作名末段, 它们不是凭据
//
// actionTailSegments holds the common action-name tails, which are not credentials
var actionTailSegments = map[string]bool{
	"hook": true, "hooks": true, "notify": true, "send": true, "message": true,
	"messages": true, "push": true, "post": true, "webhook": true, "webhooks": true,
	"event": true, "events": true, "alert": true, "alerts": true, "report": true,
}

// pathCredentialSegments 取地址 path 里应当按凭据处理的段
// 只有末段达到长度下限且不是动作名时才返回它, 否则返回 nil, 让 /notify 一类端点保持可读
//
// pathCredentialSegments returns the path segments that have to be treated as credentials
// The trailing segment comes back only when it reaches the length floor and is not an action name,
// which keeps an endpoint such as /notify readable
func pathCredentialSegments(raw string) []string {
	segments := common.PathSecrets(raw, 1)
	if len(segments) == 0 {
		return nil
	}
	tail := segments[0]
	if len(tail) < minCredentialSegmentLen || actionTailSegments[strings.ToLower(tail)] {
		return nil
	}
	return segments
}

// maskRenderedURL 按 maskSecrets 遮蔽渲染后的完整地址, 返回要放进 Preview 的文本与告警
// maskSecrets 的每一项都必须是 endpoint 的子串, 只有全部匹配时才逐项替换为 ***
// 任意一项匹配不上就把整条地址换成 <redacted-url> 并记告警: 声明了要遮蔽却找不到, 说明用户对
// 地址形态的判断与实际情况不符, 剩下的部分哪一段含凭据无法确定, 因此宁可不显示
// 未匹配的项各自记一条告警, 另加一条说明整条地址被隐藏, 让人知道为什么预览只剩占位符
// maskSecrets 为空时改用路径启发式判断, 因此不写该字段的配置仍能打码 path 里的凭据
//
// maskRenderedURL masks the rendered full address according to maskSecrets, returning the text to
// put in Preview together with the warnings
// Every entry of maskSecrets has to be a substring of endpoint, and only a full match replaces each
// fragment with ***
// A single entry that matches nothing turns the whole address into <redacted-url> with warnings: a
// declared fragment that is absent means the user's reading of the address shape differs from the
// real one, so which remaining part carries the credential is unknown and showing less wins
// Each absent entry earns a warning, plus one explaining that the whole address was hidden, so the
// reader understands why the preview holds a placeholder only
// An empty maskSecrets selects the path heuristic instead, which masks a credential inside the path
// for a configuration that omits the field
func maskRenderedURL(endpoint string, maskSecrets []string) (string, []apperr.Problem) {
	if len(maskSecrets) == 0 {
		return common.MaskURLWithSecrets(endpoint, pathCredentialSegments(endpoint)...), nil
	}

	masked := common.MaskURL(endpoint)
	var warnings []apperr.Problem
	matched := 0
	declared := 0
	for _, fragment := range maskSecrets {
		trimmed := strings.TrimSpace(fragment)
		if trimmed == "" {
			continue
		}
		declared++
		// 只在原始地址里出现过的片段才替换, 否则 MaskURL 之后的文本里可能本来就是 *** 形态
		//
		// Only a fragment present in the original address is replaced, since the text after MaskURL
		// may already carry the *** form
		if !strings.Contains(endpoint, trimmed) {
			warnings = append(warnings, apperr.Problem{
				ID:   keyWarnMaskMiss,
				Data: map[string]any{"Fragment": trimmed, "Path": "settings.maskSecrets"},
			})
			continue
		}
		matched++
		// 这里只做字面替换, 不能走 MaskInText: 它的第一步是 SanitizeString, 而该函数会把整条
		// URL 换成 <redacted-url>, 那样 maskSecrets 的逐段替换就失去意义
		//
		// This step does a literal replacement only and must not go through MaskInText, whose first
		// move is SanitizeString: that replaces a whole URL with <redacted-url>, which would defeat
		// the per-fragment replacement of maskSecrets
		masked = replaceFragment(masked, trimmed)
	}
	// 只有全部声明项都匹配上才显示打了码的地址, 少一项就整条隐藏
	//
	// The masked address is shown only when every declared entry matched, and one absent entry hides
	// the whole address
	if matched != declared {
		// 逐项告警已经记过, 这里只补一条说明为什么整条地址被隐藏, 不再重复每一项
		//
		// The per-entry warnings are already recorded, so this adds the reason the whole address is
		// hidden rather than repeating every entry
		warnings = append(warnings, apperr.Problem{
			ID:   keyWarnMaskAllMiss,
			Data: map[string]any{"Path": "settings.maskSecrets"},
		})
		return common.RedactedURL(), warnings
	}
	return masked, warnings
}

// replaceFragment 把文本里的一段字面内容替换为打码值
// 单独写出来是为了说明它与 MaskInText 的区别: 后者会先跑 SanitizeString 打码整条 URL,
// 而这里处理的是已经打码过的文本, 只需要针对用户声明的片段做字面替换
//
// replaceFragment replaces one literal fragment of the text with the mask value
// It exists to make the difference from MaskInText explicit: that helper runs SanitizeString first,
// which redacts a whole URL, while the text here is already redacted and only needs the declared
// fragment replaced literally
func replaceFragment(text, fragment string) string {
	if fragment == "" {
		return text
	}
	return strings.ReplaceAll(text, fragment, maskValueText)
}

// endpointSecrets 取出地址里的 userinfo, 敏感 query 参数值与 path 凭据段, 用作遮蔽的服务端回显对象
// path 一侧按 pathCredentialSegments 判定, 因此 /services/.../<secret> 型的目标也覆盖到
//
// endpointSecrets returns the userinfo, the sensitive query parameter values and the path credential
// segments of an address as the values to mask when a server echoes them back
// The path side follows pathCredentialSegments, which reaches a /services/.../<secret> target as well
func endpointSecrets(raw string) []string {
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	var out []string
	if u.User != nil {
		if name := u.User.Username(); name != "" {
			out = append(out, name)
		}
		if password, ok := u.User.Password(); ok && password != "" {
			out = append(out, password)
		}
	}
	// 原始地址与转义后的地址都可能出现在服务端回显里, 因此两种写法都收集
	//
	// Both the raw address and the escaped form may appear in what a server echoes back, so both
	// spellings are collected
	query := u.Query()
	for _, name := range sensitiveQueryNames {
		if value := query.Get(name); value != "" {
			out = append(out, value)
		}
	}
	out = append(out, pathCredentialSegments(raw)...)
	sort.Strings(out)
	return out
}

// invalidURLProblem 构造 URL 错误, Data.URL 先经 MaskURL, Cause 先经 SanitizeError
//
// invalidURLProblem builds the URL error, with Data.URL passing through MaskURL and Cause through
// SanitizeError
func invalidURLProblem(raw string, cause error) *apperr.Problem {
	return &apperr.Problem{
		ID:    keyErrorInvalidURL,
		Data:  map[string]any{"URL": common.MaskURL(raw)},
		Cause: common.SanitizeError(cause),
	}
}

// invalidMethodProblem 构造方法错误, Data 的 Method 保留渲染后的取值
//
// invalidMethodProblem builds the method error and keeps the rendered value in Data.Method
func invalidMethodProblem(method string, cause error) *apperr.Problem {
	return &apperr.Problem{
		ID:    keyErrorInvalidMethod,
		Data:  map[string]any{"Method": method},
		Cause: cause,
	}
}

// problem 构造只带消息键与可选 Data 的 Problem
//
// problem builds a Problem holding a message key and optional Data
func problem(id string, data map[string]any) *apperr.Problem {
	return &apperr.Problem{ID: id, Data: data}
}

// errorDetail 提取错误的文本细节并脱敏, 防止地址进入 Data
//
// errorDetail extracts the error text and redacts it, which keeps an address from reaching Data
func errorDetail(err error) string {
	if err == nil {
		return ""
	}
	return common.SanitizeString(err.Error())
}

// bodyReader 在正文非空时返回 bytes.Reader, 空正文用 nil 让 http.NewRequest 构造无正文请求
//
// bodyReader returns a bytes.Reader for a non-empty body and nil for an empty one, which lets
// http.NewRequest build a request without a body
func bodyReader(body []byte) io.Reader {
	if len(body) == 0 {
		return nil
	}
	return bytes.NewReader(body)
}

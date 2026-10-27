package common

import (
	"net/netip"
	"net/url"
	"sort"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
)

// ---------------------------------------------------------------------------
// URL 规范化与脱敏: 渠道设置里的端点先经这里补齐 scheme 并校验, 再进入请求构造
// NormalizeURL 只做字符串层面的工作, 不解析 DNS, 也不建立连接
// 失败一律返回 *apperr.Problem, ID 取 common.error.*, Data 只带字段名, 不带原始地址
//
// URL normalisation and redaction: an endpoint from a channel's settings is completed with a
// scheme and validated here before it reaches request construction
// NormalizeURL works on the string alone, with no DNS lookup and no connection
// A failure always returns an *apperr.Problem whose ID comes from common.error.*, with Data
// carrying the field name only and never the original address
// ---------------------------------------------------------------------------

// 公共层 URL 消息键, 每个都以 common.error. 开头并在 en 与 zh-CN 两份目录里成对存在
//
// The shared URL message keys, each starting with common.error. and existing in both the en and the
// zh-CN catalog
const (
	keyErrorEmptyURL    = "common.error.empty_url"
	keyErrorInvalidURL  = "common.error.invalid_url"
	keyErrorMissingHost = "common.error.missing_host"
)

const (
	// defaultURLField 是 NormalizeURL 的字段名占位: 该签名没有字段参数, Problem.Data 的 Field
	// 先填它, 需要更精确名字的调用方改用 ValidateHTTPURL(raw, field)
	// defaultURLField is the field-name placeholder for NormalizeURL, whose signature takes no
	// field argument, so Data's Field gets this value; a caller wanting a precise name uses
	// ValidateHTTPURL(raw, field) instead
	defaultURLField = "url"

	// redactedURL 是脱敏兜底文本, 出现在 MaskURL 与 SanitizeString 的输出里
	// redactedURL is the fallback redaction text in the output of MaskURL and SanitizeString
	redactedURL = "<redacted-url>"

	// maskValue 替换 userinfo 与敏感 query 参数的值
	// maskValue replaces the value of a userinfo and of a sensitive query parameter
	maskValue = "***"

	// httpScheme 与 httpsScheme 是公共层接受的两种 scheme
	// httpScheme and httpsScheme are the two schemes the shared layer accepts
	httpScheme  = "http"
	httpsScheme = "https"
)

// sensitiveQueryKeys 是 query 里需要打码的参数名, 比较时先转小写
// 这五个名字覆盖各平台把凭据放在 query 里的常见写法
//
// sensitiveQueryKeys holds the query parameter names whose value gets masked, compared after
// lowercasing
// The five names cover the common way a platform keeps a credential inside the query
var sensitiveQueryKeys = map[string]bool{
	"token":        true,
	"key":          true,
	"access_token": true,
	"sign":         true,
	"secret":       true,
}

// NormalizeURL 规范化地址: TrimSpace; 空串报错; 已有 scheme 只允许 http/https 且主机非空;
// 缺 scheme 时给裸 IPv6 补 [], 回环主机 (localhost、127.0.0.0/8、::1) 补 http://, 其余补 https://
// path/query/fragment 原样保留, scheme 之外的文本也不重新编码
//
// NormalizeURL normalises an address: it trims the surrounding space and reports an empty string,
// an existing scheme is limited to http/https with a non-empty host, and a missing scheme gets
// brackets around a bare IPv6 plus http:// for a loopback host (localhost, 127.0.0.0/8, ::1) or
// https:// for anything else
// The path, query and fragment are kept as they were, and no text outside the scheme is re-encoded
func NormalizeURL(raw string) (string, error) {
	return normalizeURL(raw, defaultURLField)
}

// ValidateHTTPURL 与 NormalizeURL 规则相同, 额外把 field 写进错误 Data, 供字段级报错定位
// 只需要「合法 http/https」的字段 (如 avatarUrl) 用这个入口
//
// ValidateHTTPURL follows the rules of NormalizeURL and additionally puts field into the error
// Data so the message names the offending setting
// A field that only needs a valid http/https address, such as avatarUrl, goes through this entry
func ValidateHTTPURL(raw, field string) (string, error) {
	if strings.TrimSpace(field) == "" {
		field = defaultURLField
	}
	return normalizeURL(raw, field)
}

// normalizeURL 是两个入口的共同实现, field 只用于错误定位, 不参与地址判定
//
// normalizeURL is the shared implementation behind both entries, where field only locates the
// error and never takes part in the address decision
func normalizeURL(raw, field string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", urlProblem(keyErrorEmptyURL, field, nil)
	}

	// 裸 IPv6 先行: 它内部的冒号会被 scheme 探测误读, "::1" 与 "fe80::1" 都是这种形态
	// A bare IPv6 goes first, since the colons inside it would be misread by the scheme probe,
	// and "::1" and "fe80::1" are both of that shape
	if host, addr, ok := bareIPv6(s); ok {
		scheme := httpsScheme
		if addr.IsLoopback() {
			scheme = httpScheme
		}
		return scheme + "://" + host, nil
	}

	if scheme, body, ok := splitScheme(s); ok {
		if scheme != httpScheme && scheme != httpsScheme {
			return "", urlProblem(keyErrorInvalidURL, field, nil)
		}
		out := scheme + "://" + body
		u, err := url.Parse(out)
		if err != nil {
			return "", urlProblem(keyErrorInvalidURL, field, err)
		}
		if err := checkAuthority(u, field); err != nil {
			return "", err
		}
		return out, nil
	}

	// 缺 scheme: 先去掉可选的 "//" 前缀并给带 path 的裸 IPv6 补括号, 再用临时 https 解析出主机判回环
	// With a missing scheme: the optional "//" prefix comes off first, a bare IPv6 that carries a
	// path gets brackets, then a temporary https parse reads the host out of it for loopback
	body := strings.TrimPrefix(s, "//")
	body = bracketIPv6Authority(body)
	u, err := url.Parse(httpsScheme + "://" + body)
	if err != nil {
		return "", urlProblem(keyErrorInvalidURL, field, err)
	}
	if err := checkAuthority(u, field); err != nil {
		return "", err
	}
	scheme := httpsScheme
	if isLoopbackHost(u.Hostname()) {
		scheme = httpScheme
	}
	return scheme + "://" + body, nil
}

// bareIPv6 识别整体就是一个 IPv6 地址的输入 (无 path/query/端口), 返回加好括号的主机与地址值
// 已带括号或含 "/" 的输入不在这里处理, 交给缺 scheme 分支
//
// bareIPv6 recognises input that is exactly one IPv6 address (no path, query or port) and returns
// the bracketed host with the parsed address
// Text that already has brackets or holds a "/" is left to the scheme-less branch
func bareIPv6(s string) (host string, addr netip.Addr, ok bool) {
	if !strings.Contains(s, ":") || strings.ContainsAny(s, "/?#") {
		return "", addr, false
	}
	parsed, err := netip.ParseAddr(s)
	if err != nil || !parsed.Is6() {
		return "", addr, false
	}
	return "[" + s + "]", parsed, true
}

// splitScheme 拆出小写 scheme 与其后的 authority/path 部分
// 判定规则: ':' 之前的片段形态合法 (以字母开头, 只含字母数字与 +-.), 且其后的内容以 "/" 开头,
// 或者它是 http/https 本身 (少写斜杠的笔误), 或者它不以数字开头 (opaque 形态, 如 "mailto:x")
// 冒号后紧跟数字时按 host:port 处理, 这是 RFC 3986 对歧义形态的规定, 于是 "localhost:8080"
// 与 "example.com:8080" 走缺 scheme 分支
//
// splitScheme returns the lowercased scheme and the authority/path part that follows it
// A scheme counts as present when the fragment before ':' has the right shape (starting with a
// letter and holding only letters, digits, +, - and .) and what follows starts with "/", or when
// the fragment is http/https itself, which covers the missing-slash typo, or when what follows
// does not start with a digit, which is the opaque form such as "mailto:x"
// A digit right after the colon makes the text a host:port, the RFC 3986 rule for that ambiguity,
// so "localhost:8080" and "example.com:8080" go down the scheme-less path
func splitScheme(s string) (scheme, body string, ok bool) {
	i := strings.IndexByte(s, ':')
	if i <= 0 || !validSchemeName(s[:i]) {
		return "", "", false
	}
	rest := s[i+1:]
	name := strings.ToLower(s[:i])
	if name != httpScheme && name != httpsScheme && !strings.HasPrefix(rest, "/") {
		if rest == "" || !isDigit(rest[0]) {
			return name, "", true
		}
		return "", "", false
	}
	// 最多去掉两个前导斜杠: "https:///x" 的主机仍是空的, 交给 checkAuthority 报缺主机
	// At most two leading slashes come off, so the host of "https:///x" stays empty and
	// checkAuthority reports the missing host
	if strings.HasPrefix(rest, "//") {
		rest = rest[2:]
	} else {
		rest = strings.TrimPrefix(rest, "/")
	}
	return name, rest, true
}

// validSchemeName 按 RFC 3986 判断 scheme: scheme = ALPHA *( ALPHA / DIGIT / "+" / "-" / "." )
//
// validSchemeName checks a scheme against RFC 3986: scheme = ALPHA *( ALPHA / DIGIT / "+" /
// "-" / "." )
func validSchemeName(s string) bool {
	if s == "" || !isAlpha(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if isAlpha(c) || isDigit(c) || c == '+' || c == '-' || c == '.' {
			continue
		}
		return false
	}
	return true
}

func isAlpha(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

// bracketIPv6Authority 给缺 scheme 地址里的裸 IPv6 主机补方括号
// 只在 authority 段动手: 从第一个 /?# 之前的部分判定, 已有 "[" 前缀或冒号不足两个都原样返回
//
// bracketIPv6Authority puts brackets around a bare IPv6 host in a scheme-less address
// Only the authority is touched, the part before the first /, ? or #, and a host that already
// starts with "[" or holds fewer than two colons comes back unchanged
func bracketIPv6Authority(raw string) string {
	host, tail := splitAuthority(raw)
	if strings.HasPrefix(host, "[") || strings.Count(host, ":") < 2 {
		return host + tail
	}
	if _, err := netip.ParseAddr(host); err != nil {
		return host + tail
	}
	return "[" + host + "]" + tail
}

// splitAuthority 在第一个 /?# 处切开地址, tail 含分隔符本身以便原样拼回
//
// splitAuthority cuts an address at the first /, ? or #, keeping the delimiter in tail so the
// pieces join back together as written
func splitAuthority(raw string) (host, tail string) {
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '/', '?', '#':
			return raw[:i], raw[i:]
		}
	}
	return raw, ""
}

// checkAuthority 确认解析结果带主机名, 且端口分隔符之后是数字
// 主机名为空报 missing_host, 端口写法非法报 invalid_url
//
// checkAuthority confirms the parsed address carries a host and that the part after a port
// separator holds digits
// An empty host reports missing_host, and an unparseable port reports invalid_url
func checkAuthority(u *url.URL, field string) error {
	if u.Hostname() == "" {
		return urlProblem(keyErrorMissingHost, field, nil)
	}
	// 方括号内的冒号属于 IPv6 地址, 只有最后一个 "]" 之后的冒号才是端口分隔符
	// A colon inside the brackets belongs to an IPv6 address, so only the last "]" onward holds a
	// port separator
	sep := strings.LastIndexByte(u.Host, ':')
	closing := strings.LastIndexByte(u.Host, ']')
	if sep > closing && u.Port() == "" {
		return urlProblem(keyErrorInvalidURL, field, nil)
	}
	return nil
}

// isLoopbackHost 判定回环主机: localhost 与 netip 认定的回环地址 (127.0.0.0/8、::1)
// 回环才允许 http://, 用于本地调试
//
// isLoopbackHost reports a loopback host: localhost plus the addresses netip classifies as
// loopback (127.0.0.0/8 and ::1)
// Only a loopback host may take http://, which serves local debugging
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return addr.IsLoopback()
}

// urlProblem 构造用户可见的 URL 错误, Data 只放字段名
// cause 是 url.Parse 之类的技术细节, 其文本会原样引用地址, 所以先过 SanitizeError 打码
//
// urlProblem builds the user-visible URL error whose Data holds the field name only
// cause is technical detail from url.Parse, whose text quotes the address back, so it passes
// through SanitizeError for masking first
func urlProblem(id, field string, cause error) *apperr.Problem {
	if cause != nil {
		cause = SanitizeError(cause)
	}
	return &apperr.Problem{
		ID:    id,
		Data:  map[string]any{"Field": field},
		Cause: cause,
	}
}

// MaskURL 输出可安全进日志与 Preview 的地址: 保留 scheme/host/path, userinfo 与敏感 query 参数的
// 值替换为 ***
// 解析失败、不透明地址 (如 mailto:) 与没有主机的地址一律返回 <redacted-url>, 宁多打码不少打码
// 路径与查询的百分号编码保持原样, 不重新编码
//
// MaskURL returns an address that is safe for logs and Preview: the scheme, host and path stay,
// while a userinfo and the value of a sensitive query parameter become ***
// A parse failure, an opaque address such as mailto:, and an address without a host all come back
// as <redacted-url>, since over-masking beats under-masking
// The percent-encoding of the path and query stays as written, with nothing re-encoded
func MaskURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || u.Opaque != "" || u.Host == "" {
		return redactedURL
	}

	var b strings.Builder
	if u.Scheme != "" {
		b.WriteString(u.Scheme)
		b.WriteString("://")
	} else {
		// 协议相对地址 "//host/path": 保留 "//" 前缀, 免得打码后的形态被误读成相对路径
		// A protocol-relative "//host/path" keeps its "//" prefix, which stops the masked form
		// from being misread as a relative path
		b.WriteString("//")
	}
	if u.User != nil {
		b.WriteString(maskValue)
		b.WriteString("@")
	}
	b.WriteString(u.Host)
	b.WriteString(u.EscapedPath())
	if q := maskQuery(u.RawQuery); q != "" {
		b.WriteString("?")
		b.WriteString(q)
	}
	fragment := u.RawFragment
	if fragment == "" {
		fragment = u.Fragment
	}
	if fragment != "" {
		b.WriteString("#")
		b.WriteString(fragment)
	}
	return b.String()
}

// maskQuery 逐个参数打码敏感值, 参数名与其余参数原样保留, 顺序也不变
// 没有 "=" 的裸参数名不含值, 因此不需要打码
//
// maskQuery masks a sensitive value parameter by parameter, keeping the parameter names, every
// other parameter and the original order
// A bare name without "=" carries no value, so it needs no masking
func maskQuery(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	pairs := strings.Split(rawQuery, "&")
	for i, pair := range pairs {
		key, _, hasValue := strings.Cut(pair, "=")
		if !sensitiveQueryKeys[strings.ToLower(key)] {
			continue
		}
		if hasValue {
			pairs[i] = key + "=" + maskValue
			continue
		}
		pairs[i] = maskValue
	}
	return strings.Join(pairs, "&")
}

// RedactedURL 返回脱敏兜底文本, 供调用方在无法判断凭据位置时整条替换地址
//
// RedactedURL returns the fallback redaction text, for a caller replacing a whole address when the
// credential position cannot be determined
func RedactedURL() string { return redactedURL }

// MaskURLWithSecrets 在 MaskURL 的结果上再遮蔽调用方声明的额外凭据
// MaskURL 只处理 userinfo 与五个敏感 query 参数, 而 Slack 的 /services/T…/B…/<secret>、Discord 的
// /api/webhooks/<id>/<token>、Teams 等把凭据放在 path 里, 需要渠道声明哪些段是凭据
// 空串凭据必须跳过, 否则 ReplaceAll 会在每个字符之间插入打码值
// 替换按长度降序进行, 较短的先替换会破坏较长凭据的匹配
// secrets 为空时等价于 MaskURL
//
// MaskURLWithSecrets masks the extra credentials the caller declares on top of the MaskURL result
// MaskURL covers the userinfo and the five sensitive query parameters only, while Slack puts a
// credential in /services/T…/B…/<secret>, Discord in /api/webhooks/<id>/<token> and Teams in a
// similar path segment, which is why a channel declares those segments
// An empty secret must be skipped, otherwise ReplaceAll would insert the mask between every pair of
// characters
// The replacements run in descending order of length, since a shorter secret applied first would
// break the match of a longer one
// With no secrets it behaves like MaskURL
func MaskURLWithSecrets(raw string, secrets ...string) string {
	masked := MaskURL(raw)
	if len(secrets) == 0 {
		return masked
	}

	// 排序在副本上做, 调用方传入的切片顺序与内容都不被改动
	//
	// The sort runs on a copy, so neither the order nor the content of the caller's slice changes
	sorted := make([]string, len(secrets))
	copy(sorted, secrets)
	sort.SliceStable(sorted, func(i, j int) bool {
		return len(sorted[i]) > len(sorted[j])
	})

	for _, secret := range sorted {
		if secret == "" {
			continue
		}
		masked = strings.ReplaceAll(masked, secret, maskValue)
	}
	return masked
}

// PathSecrets 取地址 path 末尾 count 段作为凭据候选, 按「末段在前」的顺序返回, 供渠道声明
// webhook 形态的地址
// 末段在前是因为 webhook 的末段通常是最机密的 token, 调用方按顺序展示时先看到它
// 少于 count 段时返回全部非空段, 没有段时返回 nil; 调用方据此决定是否打码
//
// PathSecrets returns the trailing count segments of an address path as credential candidates,
// last segment first, for a channel declaring a webhook-shaped address
// The last segment comes first because the tail of a webhook address is usually the most sensitive
// token, so a caller showing them in order leads with it
// A path shorter than count yields every non-empty segment, an empty path yields nil, and the caller
// decides from that whether to mask
func PathSecrets(raw string, count int) []string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil
	}
	var segments []string
	for _, segment := range strings.Split(strings.Trim(u.Path, "/"), "/") {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	if len(segments) == 0 {
		return nil
	}
	if count <= 0 || count > len(segments) {
		count = len(segments)
	}
	out := make([]string, 0, count)
	for i := len(segments) - 1; i >= len(segments)-count; i-- {
		out = append(out, segments[i])
	}
	return out
}

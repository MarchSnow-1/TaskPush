package serverchan

/*
serverchan 渠道的配置层: 消息键与协议常量, SendKey 形态判定, settings 结构, 工厂与静态校验
Prepare 与 Send 在 channel.go, 渲染与请求体字段在 render.go, 打码在 mask.go

The configuration layer of the serverchan channel: the message keys and protocol constants, the
SendKey shape decision, the settings struct, the factory and its static validation
Prepare and Send sit in channel.go, rendering with the request-body fields in render.go, and
masking in mask.go
*/

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/settings"
)

// 消息键与协议常量, 每个消息键都以 channel.serverchan. 开头并在 en 与 zh-CN 两份目录里成对存在
//
// The message keys and protocol constants, where every key starts with channel.serverchan. and
// exists in both the en and the zh-CN catalog
const (
	keyDesc              = "channel.serverchan.desc"
	keyFieldToken        = "channel.serverchan.field.token.desc"
	keyFieldTags         = "channel.serverchan.field.tags.desc"
	keyFieldShort        = "channel.serverchan.field.short.desc"
	keyFieldChannels     = "channel.serverchan.field.channels.desc"
	keyFieldOpenIDs      = "channel.serverchan.field.openids.desc"
	keyErrorMissingToken = "channel.serverchan.error.missing_token"
	keyErrorInvalidSC3   = "channel.serverchan.error.invalid_sc3_key"
	keyErrorEmptyTitle   = "channel.serverchan.error.empty_title"
	keyErrorAPICode      = "channel.serverchan.error.api_code"
	keyErrorBadResponse  = "channel.serverchan.error.bad_response"
	keyWarnTitleCut      = "channel.serverchan.warn.title_truncated"
	keyPreviewMessage    = "channel.serverchan.preview.message"
)

const (
	// modeTurbo 是 Turbo 路由的预览模式名
	// modeTurbo is the preview mode name of the Turbo route
	modeTurbo = "Turbo"
	// modeSC3 是 SC3 路由的预览模式名
	// modeSC3 is the preview mode name of the SC3 route
	modeSC3 = "SC3"
	// turboTitleLimit 是 Turbo 标题的 rune 上限, 与平台文档一致
	// turboTitleLimit is the rune cap of a Turbo title, matching the platform documentation
	turboTitleLimit = 32
	// maskValue 替换错误文本里回显的 token
	// maskValue replaces a token echoed inside error text
	maskValue = "***"
	// tokenMaskPrefixRunes 是无效 SC3 key 掩码里保留的前缀长度
	// 前缀只用于让用户定位是哪把 key, 不足以还原完整 SendKey
	//
	// tokenMaskPrefixRunes is how many leading runes the invalid-SC3-key mask keeps
	// The prefix only helps a user find which key is wrong and is not enough to reconstruct the
	// full SendKey
	tokenMaskPrefixRunes = 4
)

// sc3KeyPattern 匹配 SC3 key 的形态: sctp + 数字 uid + t + 剩余内容
// 规格把形态冻结为 ^sctp([0-9]+)t, 因此 sctp 开头但形态不符的 key 会在 Prepare 报错
//
// sc3KeyPattern matches the shape of an SC3 key: sctp, a numeric uid, t and the remaining text
// The specification freezes the shape as ^sctp([0-9]+)t, so a key starting with sctp but shaped
// differently reports an error in Prepare
var sc3KeyPattern = regexp.MustCompile(`^sctp([0-9]+)t`)

// serverchanSettings 是 serverchan 渠道的 settings
//
// serverchanSettings holds the settings of the serverchan channel
type serverchanSettings struct {
	Token    string              `json:"token"`
	Tags     settings.StringList `json:"tags"`
	Short    string              `json:"short"`
	Channels settings.StringList `json:"channels"`
	OpenIDs  settings.StringList `json:"openids"`
}

// factory 创建 serverchan 渠道实例
//
// factory creates serverchan channel instances
type factory struct{}

// init 在包加载时注册工厂, 装配包以空白导入触发
//
// init registers the factory while the package loads, which the assembly package triggers with a
// blank import
func init() { channel.Register(factory{}) }

// Type 返回注册类型名
//
// Type returns the registered type name
func (factory) Type() string { return "serverchan" }

// Describe 返回渠道描述的消息键
//
// Describe returns the message key describing the channel
func (factory) Describe() string { return keyDesc }

// Fields 返回全部 settings 字段的元数据, 顺序稳定
//
// Fields returns the metadata of every settings field in a stable order
func (factory) Fields() []channel.Field {
	return []channel.Field{
		{Name: "token", Type: "string", Required: true, DescriptionKey: keyFieldToken},
		{Name: "tags", Type: "list<string>", Required: false, DescriptionKey: keyFieldTags},
		{Name: "short", Type: "string", Required: false, DescriptionKey: keyFieldShort},
		{Name: "channels", Type: "list<string>", Required: false, DescriptionKey: keyFieldChannels},
		{Name: "openids", Type: "list<string>", Required: false, DescriptionKey: keyFieldOpenIDs},
	}
}

// Create 严格解码 settings 并做静态校验
// token 必填; 列表字段由 settings.StringList 解析数组或逗号串; 静态失败一律包成 Permanent
//
// Create strictly decodes the settings and validates them statically
// token is required, the list fields go through settings.StringList for an array or a comma
// string, and a static failure is wrapped with Permanent
func (factory) Create(id string, raw json.RawMessage) (channel.Channel, error) {
	var s serverchanSettings
	if err := channel.DecodeStrict(raw, &s); err != nil {
		return nil, apperr.Permanent(err)
	}
	s.Token = strings.TrimSpace(s.Token)
	if s.Token == "" {
		return nil, apperr.Permanent(problem(keyErrorMissingToken, nil))
	}
	return &serverchanChannel{id: id, s: s}, nil
}

// serverchanEndpoint 是端点构造的注入点, 测试替换它以指向本地 httptest 服务
//
// serverchanEndpoint is the injection point for endpoint construction, which tests replace with a
// local httptest address
var serverchanEndpoint = buildServerchanEndpoint

// buildServerchanEndpoint 按 SendKey 前缀选择 SC3 或 Turbo
// sctp 开头但不符合 ^sctp([0-9]+)t 的 key 返回 invalid_sc3_key, Data 只放掩码
//
// buildServerchanEndpoint picks SC3 or Turbo from the SendKey prefix
// A key starting with sctp that fails ^sctp([0-9]+)t returns invalid_sc3_key with a mask alone in
// Data
func buildServerchanEndpoint(token string) (string, string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", "", problem(keyErrorMissingToken, nil)
	}
	if strings.HasPrefix(token, "sctp") {
		match := sc3KeyPattern.FindStringSubmatch(token)
		if match == nil {
			return "", "", problem(keyErrorInvalidSC3, map[string]any{
				"TokenMasked": tokenMask(token),
			})
		}
		uid := match[1]
		return "https://" + uid + ".push.ft07.com/send/" + url.PathEscape(token) + ".send", modeSC3, nil
	}
	return "https://sctapi.ftqq.com/" + url.PathEscape(token) + ".send", modeTurbo, nil
}

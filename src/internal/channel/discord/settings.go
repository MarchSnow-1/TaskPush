package discord

/*
discord 渠道的配置层: 消息键与协议常量, settings 结构, 工厂与静态校验
Prepare 与 Send 在 channel.go, 渲染与字段路径改写在 render.go, 打码与 Problem 构造在 mask.go

The configuration layer of the discord channel: the message keys and protocol constants, the
settings struct, the factory and its static validation
Prepare and Send sit in channel.go, rendering and field path rewriting in render.go, and masking
with the Problem builders in mask.go
*/

import (
	"encoding/json"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
)

// 消息键与协议常量, 每个消息键都以 channel.discord. 开头并在 en 与 zh-CN 两份目录里成对存在
//
// The message keys and protocol constants, where every key starts with channel.discord. and exists
// in both the en and the zh-CN catalog
const (
	keyDesc               = "channel.discord.desc"
	keyFieldURL           = "channel.discord.field.url.desc"
	keyFieldUsername      = "channel.discord.field.username.desc"
	keyFieldAvatarURL     = "channel.discord.field.avatarUrl.desc"
	keyErrorMissingURL    = "channel.discord.error.missing_url"
	keyErrorInvalidURL    = "channel.discord.error.invalid_url"
	keyErrorEmptyContent  = "channel.discord.error.empty_content"
	keyErrorInvalidAvatar = "channel.discord.error.invalid_avatar_url"
	keyErrorHTTPStatus    = "channel.discord.error.http_status"
	keyWarnTruncated      = "channel.discord.warn.truncated"
	keyPreviewMessage     = "channel.discord.preview.message"
)

const (
	// contentTypeJSON 是请求体的媒体类型
	// contentTypeJSON is the media type of the request body
	contentTypeJSON = "application/json; charset=utf-8"
	// contentLimitUTF16 是 content 的上限, Discord 按 UTF-16 code unit 计长
	// contentLimitUTF16 is the content cap, which Discord measures in UTF-16 code units
	contentLimitUTF16 = 2000
	// settingsPathURL 与其它 settingsPath* 是模板告警与错误里的字段路径
	// settingsPathURL and the other settingsPath constants are the field paths used by template
	// warnings and errors
	settingsPathURL       = "settings.url"
	settingsPathUsername  = "settings.username"
	settingsPathAvatarURL = "settings.avatarUrl"
	// maskValue 替换服务端回显文本里可能出现的 webhook token
	// maskValue replaces a webhook token that server-supplied text may echo
	maskValue = "***"
)

// discordSettings 是 discord 渠道的 settings, 三个字段都支持模板
//
// discordSettings holds the settings of the discord channel, where all three fields support a template
type discordSettings struct {
	URL       string `json:"url"`
	Username  string `json:"username"`
	AvatarURL string `json:"avatarUrl"`
}

// factory 创建 discord 渠道实例
//
// factory creates discord channel instances
type factory struct{}

// init 在包加载时注册工厂, 装配包以空白导入触发
//
// init registers the factory while the package loads, which the assembly package triggers with a
// blank import
func init() { channel.Register(factory{}) }

// Type 返回注册类型名
//
// Type returns the registered type name
func (factory) Type() string { return "discord" }

// Describe 返回渠道描述的消息键
//
// Describe returns the message key describing the channel
func (factory) Describe() string { return keyDesc }

// Fields 返回全部 settings 字段的元数据, 顺序稳定
//
// Fields returns the metadata of every settings field in a stable order
func (factory) Fields() []channel.Field {
	return []channel.Field{
		{Name: "url", Type: "string", Required: true, DescriptionKey: keyFieldURL},
		{Name: "username", Type: "string", Required: false, DescriptionKey: keyFieldUsername},
		{Name: "avatarUrl", Type: "string", Required: false, DescriptionKey: keyFieldAvatarURL},
	}
}

// Create 严格解码 settings 并做静态校验
// url 必填; username 与 avatarUrl 可以省略, 非空值里的模板留到 Prepare 渲染后再校验
// 静态失败一律包成 Permanent, 因为重试不改变配置判定
//
// Create strictly decodes the settings and validates them statically
// url is required, username and avatarUrl may be omitted, and a non-empty value holding a template
// waits for Prepare to render before validation
// A static failure is wrapped with Permanent, since a retry cannot change a configuration verdict
func (factory) Create(id string, raw json.RawMessage) (channel.Channel, error) {
	var s discordSettings
	if err := channel.DecodeStrict(raw, &s); err != nil {
		return nil, apperr.Permanent(err)
	}
	if strings.TrimSpace(s.URL) == "" {
		return nil, apperr.Permanent(problem(keyErrorMissingURL, nil))
	}
	// 不含模板的 URL 在这里先做静态校验; 含 "{{" 的地址留到 Prepare, 因为那时才有变量值
	//
	// A URL without a template is checked here, while one holding "{{" waits for Prepare, when the
	// variable values exist
	if !strings.Contains(s.URL, "{{") {
		if _, err := common.NormalizeURL(s.URL); err != nil {
			return nil, apperr.Permanent(invalidURLProblem(s.URL, err))
		}
	}
	return &discordChannel{id: id, s: s}, nil
}

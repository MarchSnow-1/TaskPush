package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/template"
)

// ---------------------------------------------------------------------------
// 渠道契约类型: 一个渠道由工厂创建, 经 Prepare 渲染并构造请求, 再由 Prepared.Send 发送
// 渲染模式定义在本包, 避免这里依赖模板包的 Mode, 模板包也因此不认识渠道
//
// The channel contract types: a factory creates a channel, Prepare renders and builds the
// request, and Prepared.Send sends it
// The render mode lives in this package, which keeps it from depending on the template Mode
// and leaves the template package unaware of channels
// ---------------------------------------------------------------------------

// RenderMode 选择模板渲染遇到未解析变量时的处理方式
//
// RenderMode selects how template rendering treats an unresolved variable
type RenderMode int

const (
	// RenderStrict 遇到未解析变量时由渠道返回错误
	// RenderStrict makes a channel return an error on an unresolved variable
	RenderStrict RenderMode = iota
	// RenderCheck 遇到未解析变量时改为追加警告并继续
	// RenderCheck appends a warning and continues instead
	RenderCheck
)

// Message 是 Prepare 的输入: 已渲染的标题正文, 模板变量, 当前时间, 渲染模式与译文查表函数
// 标题正文由调度层渲染完成, 渠道不再把它们当模板处理
// Translate 让渠道按自己的消息键取当前语言文案, 渠道因此不必导入 i18n 也能输出本地化兜底文案
// 为 nil 时渠道必须回退到自己的语言无关常量
//
// Message is the input to Prepare: the rendered title and body, the template variables, the
// current time, the render mode and a translation lookup
// The scheduling layer renders the title and body, so a channel does not treat them as yet more
// templates
// Translate lets a channel resolve its own message key in the active language, so it can emit a
// localised fallback without importing i18n
// A nil lookup means the channel has to fall back to its own language-neutral constant
type Message struct {
	Title     string
	Body      string
	Vars      *template.Vars
	Now       time.Time
	Mode      RenderMode
	Translate func(key string) string
}

// Channel 是一个已配置的渠道实例
// Prepare 完成全部渲染, 校验与请求构造, 不做网络 I/O, 返回 nil error 时 Prepared 必须非 nil
//
// Channel is one configured channel instance
// Prepare performs all rendering, validation and request construction without network I/O,
// and a nil error means Prepared is non-nil
type Channel interface {
	// ID 返回渠道实例标识, 用于日志与 CLI 定位
	// ID returns the channel instance identifier used by logs and CLI targeting
	ID() string
	// Type 返回渠道类型名, 与工厂注册的 type 一致
	// Type returns the channel type name, matching the type the factory registered
	Type() string
	// Prepare 渲染设置字段, 校验内容, 构造 Prepared, 返回警告与错误
	// Prepare renders the settings fields, validates the content and builds a Prepared,
	// returning warnings and an error
	Prepare(ctx context.Context, msg Message) (Prepared, []apperr.Problem, error)
}

// Prepared 是一次可重复发送的准备结果
// 多目标渠道在内部记录已成功目标, 重复 Send 只补发尚未成功的目标
//
// Prepared is the outcome of one preparation that can be sent repeatedly
// A multi-target channel records the targets that already succeeded inside it, so a repeated
// Send reaches only the targets still outstanding
type Prepared interface {
	// Send 执行一次发送, 允许调度层为重试重复调用
	// Send performs one send and may be called again by the scheduling layer for a retry
	Send(ctx context.Context, client *http.Client) error
	// Preview 返回脱敏后的预览描述, 不含 token, header 值与正文全文
	// Preview returns the redacted preview description without a token, a header value or the
	// full body
	Preview() apperr.Problem
}

// Factory 创建一个渠道类型的所有实例
// Create 只做 settings 的严格解码与静态校验, 不渲染模板, 不读环境变量, 不发网络
//
// Factory creates every instance of one channel type
// Create performs strict settings decoding and static validation only, without rendering a
// template, reading an environment variable or touching the network
type Factory interface {
	// Type 返回注册用的渠道类型名
	// Type returns the channel type name used for registration
	Type() string
	// Describe 返回渠道描述的消息键, 如 "channel.webhook.desc"
	// Describe returns the message key describing the channel, such as "channel.webhook.desc"
	Describe() string
	// Fields 返回全部 settings 字段的元数据, 顺序稳定
	// Fields returns the metadata of every settings field in a stable order
	Fields() []Field
	// Create 用 settings 构造渠道实例, 并设置返回实例的 ID 与 Type
	// Create builds a channel from settings and sets the ID and Type of the returned instance
	Create(id string, settings json.RawMessage) (Channel, error)
}

// Field 描述一个 settings 字段, 供 types 命令展示
//
// Field describes one settings field for the types command to display
type Field struct {
	// Name 是点路径形式的字段名, 如 "body.format"
	// Name is the field name in dotted-path form, such as "body.format"
	Name string
	// Type 是字段类型: string, int, bool, list<string>, object 或 duration
	// Type is the field type: string, int, bool, list<string>, object or duration
	Type string
	// Required 表示该字段缺失时 Create 是否报错
	// Required reports whether a missing field makes Create fail
	Required bool
	// Default 是字段缺省值的展示文本, 无缺省时为空
	// Default is the display text of the field default, empty when there is none
	Default string
	// DescriptionKey 是字段描述的消息键, 如 "channel.webhook.field.url.desc"
	// DescriptionKey is the message key of the field description, such as
	// "channel.webhook.field.url.desc"
	DescriptionKey string
}

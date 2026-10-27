package config

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/settings"
)

// ---------------------------------------------------------------------------
// 配置模型类型: 只定义 Root/Channel/Defaults 与来源标签, 不做加载与校验
//
// The configuration model types define Root, Channel, Defaults and the source labels only,
// without loading or validation
// ---------------------------------------------------------------------------

// Root 是 taskpush.json 的顶层模型
// Version/Timeout/Retries/RetryDelay 的缺省值由加载逻辑填充, 本类型只承载字段
//
// Root is the top-level model of taskpush.json
// The loading logic fills the default values of Version, Timeout, Retries and RetryDelay, and
// this type carries the fields only
type Root struct {
	Version    int               `json:"version"`
	Schema     string            `json:"$schema"`
	Timeout    settings.Duration `json:"timeout"`
	Retries    int               `json:"retries"`
	RetryDelay settings.Duration `json:"retryDelay"`
	Proxy      string            `json:"proxy"`
	Channels   []Channel         `json:"channels"`

	// Deferred 保存禁用渠道的环境变量展开错误
	// check --all 需要报告它们, list 与 send 则忽略, json:"-" 让它不参与配置序列化
	//
	// Deferred holds the environment-variable expansion errors of disabled channels
	// check --all needs to report them while list and send ignore them, and json:"-"
	// keeps them out of configuration serialization
	Deferred []apperr.Problem `json:"-"`
}

// Channel 是一个渠道条目
// Settings 原样保存, 由渠道工厂在创建时严格解码; Enabled 用指针区分"未写"与"false"
//
// Channel is one channel entry
// Settings is kept as it was and decoded strictly by the channel factory at creation time,
// while Enabled is a pointer to tell "not written" from "false"
type Channel struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Enabled    *bool              `json:"enabled"`
	Timeout    *settings.Duration `json:"timeout"`
	Retries    *int               `json:"retries"`
	RetryDelay *settings.Duration `json:"retryDelay"`
	Settings   json.RawMessage    `json:"settings"`
}

// Defaults 是调度时生效的全局默认值
// 字段使用标准库类型, 因为它们已经是解析后的值
//
// Defaults holds the global values in force at scheduling time
// The fields use standard-library types because the values are already parsed
type Defaults struct {
	Timeout    time.Duration
	Retries    int
	RetryDelay time.Duration
	Proxy      string
}

// SourceKind 标记配置来源的类别
//
// SourceKind marks the kind of a configuration source
type SourceKind int

const (
	// SourceFile 表示来源是配置文件, Path 为文件路径
	// SourceFile means the source is a configuration file and Path holds its path
	SourceFile SourceKind = iota
	// SourceStdin 表示来源是标准输入
	// SourceStdin means the source is standard input
	SourceStdin
	// SourceEnv 表示来源是环境变量里的内联 JSON
	// SourceEnv means the source is inline JSON from an environment variable
	SourceEnv
)

// Source 描述一份配置从哪里来
// 文件来源的 Path 是路径, env 来源的 Path 是变量名, Data 是原始字节
//
// Source describes where one configuration came from
// A file source keeps the path in Path, an env source keeps the variable name there, and Data
// holds the raw bytes
type Source struct {
	Kind SourceKind
	Path string
	Data []byte
}

// Label 返回来源标签, 供错误与日志定位
// 形式为 "file:/abs/path", "stdin" 或 "env:VAR"
//
// Label returns the source label for errors and log targeting
// The form is "file:/abs/path", "stdin" or "env:VAR"
func (s Source) Label() string {
	switch s.Kind {
	case SourceStdin:
		return "stdin"
	case SourceEnv:
		return "env:" + s.Path
	default:
		return "file:" + s.Path
	}
}

// IsEnabled 返回渠道是否启用
// Enabled 为 nil 视为启用, 只有显式 false 才禁用
//
// IsEnabled reports whether the channel is enabled
// A nil Enabled counts as enabled, and only an explicit false disables the channel
func (c Channel) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// Effective 从全局默认值出发, 用渠道自身的非 nil 字段覆盖, 得到该渠道生效的默认值
// 覆盖后校验: timeout 必须大于 0, retries 与 retryDelay 不能为负
// 返回的 error 由调用方包装成自己的 Problem, 本包不构造 Problem 以避免反向依赖
//
// Effective starts from the global defaults and overrides them with the non-nil fields of the
// channel, giving the defaults in force for that channel
// After the overrides it validates that timeout is above zero and that retries and retryDelay
// are not negative
// The caller wraps the returned error into its own Problem, and this package does not build a
// Problem itself to avoid a reverse dependency
func (c Channel) Effective(d Defaults) (Defaults, error) {
	out := d
	if c.Timeout != nil {
		out.Timeout = c.Timeout.Duration()
	}
	if c.Retries != nil {
		out.Retries = *c.Retries
	}
	if c.RetryDelay != nil {
		out.RetryDelay = c.RetryDelay.Duration()
	}
	if out.Timeout <= 0 {
		return Defaults{}, fmt.Errorf("timeout must be above zero, got %s", out.Timeout)
	}
	if out.Retries < 0 {
		return Defaults{}, fmt.Errorf("retries must not be negative, got %d", out.Retries)
	}
	if out.RetryDelay < 0 {
		return Defaults{}, fmt.Errorf("retryDelay must not be negative, got %s", out.RetryDelay)
	}
	return out, nil
}

// Defaults 返回全局默认值
// 未加载时 Duration 的零值表现为 0, 由调用方 (加载逻辑) 先填充缺省值
//
// Defaults returns the global defaults
// The zero value of a Duration reads as 0 before loading fills the configured defaults
func (r *Root) Defaults() Defaults {
	return Defaults{
		Timeout:    r.Timeout.Duration(),
		Retries:    r.Retries,
		RetryDelay: r.RetryDelay.Duration(),
		Proxy:      r.Proxy,
	}
}

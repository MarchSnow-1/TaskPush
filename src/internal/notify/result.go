package notify

import (
	"time"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
)

// ---------------------------------------------------------------------------
// 调度结果与入参类型: 冻结契约里的 Options/Result/CheckOptions/CheckResult, 以及本包使用的消息键
// notify 只返回结构化结果与 Problem, 用户文案由 CLI 按消息键渲染
//
// The scheduling argument and result types: Options, Result, CheckOptions and CheckResult from the
// frozen contract, together with the message keys this package uses
// notify returns structured results and Problems only, and the CLI renders the user copy from the
// message keys
// ---------------------------------------------------------------------------

// 调度层消息键, 每个都以 notify.error. 开头并在 en 与 zh-CN 两份目录里成对存在
//
// The scheduling message keys, each starting with notify.error. and existing in both the en and the
// zh-CN catalog
const (
	keyErrorNoChannels = "notify.error.no_channels"
	keyErrorVarFile    = "notify.error.var_file"
	keyErrorVar        = "notify.error.var"
	keyErrorReserved   = "notify.error.reserved_var"
	keyErrorTitle      = "notify.error.title_render"
	keyErrorBody       = "notify.error.body_render"
	keyErrorClient     = "notify.error.client"
	keyErrorCanceled   = "notify.error.canceled"
)

// 结果与汇总文案属于 CLI 的渲染职责, 因此 notify 只有错误键
//
// Result and summary copy belongs to the rendering duty of the CLI, so notify carries error keys only

// Options 是一次 send 的调度入参
// StartTime 与 Timed 只在 finish 计时模式下使用, Timed 为真时由 StartTime 生成 elapsed 系列变量
// Version 是写进 User-Agent 的版本号, 空串时由 NewClient 落到 dev
// Translate 是渠道渲染兜底文案的译文查表函数, 为 nil 时渠道回退到自己的语言无关常量
//
// Options holds the scheduling arguments of one send
// StartTime and Timed are used by the finish timing mode only, and when Timed is true the elapsed
// variables are derived from StartTime
// Version is the version written into the User-Agent, and NewClient substitutes dev for an empty
// string
// Translate is the lookup a channel uses to render its fallback copy, and a nil lookup makes the
// channel fall back to its own language-neutral constant
type Options struct {
	Title     string
	Body      string
	VarPairs  []string
	VarFile   string
	DryRun    bool
	Parallel  int
	StartTime time.Time
	Timed     bool
	Version   string
	Translate func(key string) string
}

// Result 是单个渠道的发送结果
// Attempts 是实际尝试次数, DryRun 下为 0; Preview 只在 dry-run 时非 nil; Error 为 nil 表示成功
//
// Result is the outcome of one channel
// Attempts counts the attempts actually made and stays 0 under DryRun, Preview is non-nil in
// dry-run only, and a nil Error means success
type Result struct {
	ID       string
	Type     string
	Attempts int
	Warnings []apperr.Problem
	Preview  *apperr.Problem
	Error    error
}

// CheckOptions 是一次 check 的调度入参
// check 不接收标题与正文: 它用占位内容校验渠道配置
// Translate 的语义与 Options 里的同名字段一致: check 也要能渲染渠道的兜底文案, 但不发送, 因此
// 不需要版本号
//
// CheckOptions holds the scheduling arguments of one check
// check takes no title or body, since it validates channel configuration against placeholder content
// Translate means what the same field on Options means: check also renders the fallback copy of a
// channel, but it sends nothing and therefore needs no version
type CheckOptions struct {
	VarPairs  []string
	VarFile   string
	Translate func(key string) string
}

// CheckResult 是单个渠道的校验结果
// 字段语义与 Result 相同, 只是没有 Attempts: check 不发生任何发送
//
// CheckResult is the validation outcome of one channel
// The fields mean what they mean on Result, without Attempts, since check never sends
type CheckResult struct {
	ID       string
	Type     string
	Warnings []apperr.Problem
	Preview  *apperr.Problem
	Error    error
}

// warningsReporter 是 Prepared 的可选接口, 用于上送 Send 阶段发现的警告
// channel.Prepared 只冻结了 Send 与 Preview, 具体渠道额外的警告经类型断言读取; bark 已实现该方法
//
// warningsReporter is the optional interface a Prepared may implement to report warnings found while
// sending
// channel.Prepared freezes Send and Preview only, so extra channel warnings are read through a type
// assertion; bark already implements this method
type warningsReporter interface {
	Warnings() []apperr.Problem
}

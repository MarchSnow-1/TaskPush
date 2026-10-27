package notify

import (
	"context"
	"time"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/config"
	"github.com/MarchSnow-1/TaskPush/internal/template"
)

// ---------------------------------------------------------------------------
// 配置校验: 与 send 相同的变量合并, 但用占位内容与宽松渲染模式逐渠道 Create + Prepare, 不发送
// 不重试、不并发; 结果按输入顺序返回; 渠道错误只进对应 CheckResult
// 代理与超时的合法性由 config.Load 校验, check 因此不构造 HTTP 客户端
//
// Configuration validation: the same variable merge as send, but every channel runs Create and
// Prepare with placeholder content and the lenient render mode, and nothing is sent
// No retries, no concurrency; results return in the input order, and a channel error lands in its
// own CheckResult
// The proxy and timeout legality is checked by config.Load, so check builds no HTTP client
// ---------------------------------------------------------------------------

// checkContent 是 check 使用的占位标题与正文
//
// checkContent is the placeholder title and body check uses
const checkContent = "TaskPush check"

// Check 逐个渠道做配置与内容校验, 不发送、不重试、不并发
// 未解析变量在 RenderCheck 模式下降级为警告, 语法/字段/URL 错误仍然是错误
// ctx 取消后跳过剩余渠道, 返回已收集结果与 ctx.Err(), 与 Send 的取消语义一致
//
// Check validates configuration and content channel by channel and sends, retries and fans out
// nothing
// An unresolved variable degrades into a warning under RenderCheck, while syntax, field and URL
// errors stay errors
// Once ctx is cancelled the remaining channels are skipped and the collected results return with
// ctx.Err(), matching the cancellation semantics of Send
func Check(ctx context.Context, defaults config.Defaults, channels []config.Channel, opts CheckOptions) ([]CheckResult, error) {
	vars, err := buildVars(opts.VarFile, opts.VarPairs, time.Time{}, false)
	if err != nil {
		return nil, err
	}
	results := make([]CheckResult, len(channels))
	for i, spec := range channels {
		if err := ctx.Err(); err != nil {
			// 未处理的槽位补 ID/Type 与取消错误, 避免零值被 CLI 读成成功
			//
			// The untouched slots get their ID and Type together with the cancellation error, so a
			// zero value cannot read as success in the CLI
			for j := i; j < len(channels); j++ {
				results[j] = CheckResult{ID: channels[j].ID, Type: channels[j].Type, Error: canceledProblem(err)}
			}
			return results, err
		}
		results[i] = checkOnce(ctx, spec, defaults, vars, opts)
	}
	return results, nil
}

// checkOnce 完成一个渠道的校验: 生效默认值 → 创建 → Prepare → 预览
// Effective 失败 (坏 timeout/retries) 是配置错误, 包成 notify.error.client 放进该渠道结果
// 译文查表函数取自 opts.Translate, 因此 check 也能渲染渠道的兜底文案
//
// checkOnce validates one channel: effective defaults, creation, Prepare and preview
// An Effective failure (a bad timeout or retries value) is a configuration error wrapped into
// notify.error.client inside that channel's result
// The translation lookup comes from opts.Translate, which lets check render a channel's fallback copy
func checkOnce(ctx context.Context, spec config.Channel, defaults config.Defaults, vars *template.Vars, opts CheckOptions) CheckResult {
	result := CheckResult{ID: spec.ID, Type: spec.Type}
	if _, err := spec.Effective(defaults); err != nil {
		result.Error = &apperr.Problem{
			ID:    keyErrorClient,
			Data:  map[string]any{"Detail": errorDetail(err)},
			Cause: err,
		}
		return result
	}
	ch, err := channel.Create(spec.ID, spec.Type, spec.Settings)
	if err != nil {
		result.Error = err
		return result
	}
	msg := channel.Message{
		Title:     checkContent,
		Body:      checkContent,
		Vars:      vars,
		Now:       time.Now(),
		Mode:      channel.RenderCheck,
		Translate: opts.Translate,
	}
	prepared, warns, err := ch.Prepare(ctx, msg)
	result.Warnings = append(result.Warnings, warns...)
	if err != nil {
		result.Error = err
		return result
	}
	preview := prepared.Preview()
	result.Preview = &preview
	return result
}

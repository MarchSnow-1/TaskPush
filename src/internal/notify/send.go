package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
	"github.com/MarchSnow-1/TaskPush/internal/config"
	"github.com/MarchSnow-1/TaskPush/internal/template"
)

// ---------------------------------------------------------------------------
// 发送调度: 变量解析 → 严格渲染标题/正文 → 逐渠道创建/Prepare → dry-run 预览或带重试的发送
// 渠道错误只进对应 Result, 不返回全局 error; 变量/内容/无渠道类错误是全局错误
//
// The send scheduler: variable resolution, strict title and body rendering, then per-channel
// creation and Prepare followed by a dry-run preview or a retrying send
// A channel error lands in its own Result and never becomes a global error, while variable, content
// and no-channel failures are global errors
// ---------------------------------------------------------------------------

// maxVarsFileBytes 变量文件/stdin 读取上限, 与 template.LoadVarsFile 的上限保持一致
//
// maxVarsFileBytes caps a vars file or stdin read, matching the cap in template.LoadVarsFile
const maxVarsFileBytes = 1 << 20

// Send 按渠道列表调度一次发送, 返回与输入顺序一致的结果
// 并发上限取 opts.Parallel (小于 1 时按 1), 且不超过渠道数; ctx 取消后停止派发新渠道,
// 已派发的渠道通过 ctx 收到取消信号, 已收集的结果与 ctx.Err() 一起返回
//
// Send schedules one send over the channel list and returns results in the input order
// The concurrency cap comes from opts.Parallel (values below 1 count as 1) and never exceeds the
// channel count; once ctx is cancelled no further channel is dispatched, dispatched channels receive
// the cancellation through ctx, and the collected results return together with ctx.Err()
func Send(ctx context.Context, defaults config.Defaults, channels []config.Channel, opts Options) ([]Result, error) {
	vars, err := buildVars(opts.VarFile, opts.VarPairs, opts.StartTime, opts.Timed)
	if err != nil {
		return nil, err
	}

	// 标题/正文先渲染再写入 title/body 变量: 渲染时不能引用它们自己, 内容只能来自 --title/--body
	//
	// The title and body render before being written into the title and body variables: a render
	// cannot reference itself, and the content arrives only through --title and --body
	title, err := renderContent(opts.Title, vars, keyErrorTitle, "--title")
	if err != nil {
		return nil, err
	}
	body, err := renderContent(opts.Body, vars, keyErrorBody, "--body")
	if err != nil {
		return nil, err
	}
	vars.Set("title", template.Literal(title))
	vars.Set("body", template.Literal(body))

	// 渲染结果写回 opts 副本: sendOnce 只看渲染后的内容, 原始模板到此为止不再参与
	//
	// The rendered content is written back into the opts copy: sendOnce sees the rendered content
	// only, and the raw templates play no part from here on
	opts.Title = title
	opts.Body = body

	if len(channels) == 0 {
		return nil, &apperr.Problem{ID: keyErrorNoChannels}
	}

	results := make([]Result, len(channels))
	if opts.DryRun {
		// dry-run 不发送也不重试: 只创建渠道、Prepare 并取预览, 顺序执行与契约一致
		//
		// A dry-run sends and retries nothing: it only creates the channel, runs Prepare and
		// collects the preview, in sequence as the contract states
		for i, spec := range channels {
			results[i] = sendOnce(ctx, spec, defaults, vars, opts, time.Now(), true)
		}
		return results, nil
	}

	parallel := opts.Parallel
	if parallel <= 0 {
		parallel = 1
	}
	if parallel > len(channels) {
		parallel = len(channels)
	}
	if parallel == 1 {
		for i, spec := range channels {
			if err := ctx.Err(); err != nil {
				fillCanceled(results[i:], channels, err)
				return results, err
			}
			results[i] = sendOnce(ctx, spec, defaults, vars, opts, time.Now(), false)
			// 中止派发的判据只看外层 ctx: 渠道自身的 deadline/timeout 错误链里可能同样带着
			// context.DeadlineExceeded, 但那是该渠道的失败, 不是调度层取消, 后续渠道照常派发
			//
			// Only the outer ctx decides whether dispatch stops: a channel's own deadline or timeout
			// error chain may carry context.DeadlineExceeded too, but that is the channel's failure
			// rather than a scheduling cancellation, and the later channels are dispatched as usual
			if err := ctx.Err(); err != nil {
				fillCanceled(results[i+1:], channels, err)
				return results, err
			}
		}
		return results, nil
	}

	// worker pool: 按索引派发保证结果按输入顺序写回; 一个渠道失败不影响其它渠道
	//
	// The worker pool: dispatching by index writes the results back in the input order, and one
	// channel failing leaves the others untouched
	work := make(chan int)
	dispatched := make([]bool, len(channels))
	var wg sync.WaitGroup
	for w := 0; w < parallel; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				results[i] = sendOnce(ctx, channels[i], defaults, vars, opts, time.Now(), false)
			}
		}()
	}
dispatch:
	for i := range channels {
		select {
		case <-ctx.Done():
			break dispatch
		default:
		}
		select {
		case work <- i:
			dispatched[i] = true
		case <-ctx.Done():
			break dispatch
		}
	}
	close(work)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		// 外层 ctx 仍存活时, 渠道自身的超时错误留在该渠道结果里, 不返回全局错误;
		// ctx 已取消时, 未派发的槽位补上取消错误, 已派发的渠道由 sendWithRetry 通过 ctx 收到
		// 同一次取消
		//
		// While the outer ctx is still alive, a channel's own timeout error stays inside that channel's
		// result and no global error comes back; once ctx is cancelled the undispatched slots carry the
		// cancellation error, and the dispatched channels saw the same cancellation through ctx inside
		// sendWithRetry
		for i := range results {
			if !dispatched[i] {
				results[i] = Result{ID: channels[i].ID, Type: channels[i].Type, Error: canceledProblem(err)}
			}
		}
		return results, err
	}
	return results, nil
}

// fillCanceled 给尚未派发的槽位补上 ID/Type 与取消错误, 避免零值结果在 CLI 里读成成功
//
// fillCanceled fills the undispatched slots with their ID and Type plus the cancellation error, so a
// zero-value result cannot read as success in the CLI
func fillCanceled(slots []Result, channels []config.Channel, err error) {
	for i := range slots {
		index := len(channels) - len(slots) + i
		slots[i] = Result{ID: channels[index].ID, Type: channels[index].Type, Error: canceledProblem(err)}
	}
}

// canceledProblem 构造 notify.error.canceled, Cause 保留原始 context 错误
//
// canceledProblem builds notify.error.canceled with the original context error as Cause
func canceledProblem(err error) *apperr.Problem {
	return &apperr.Problem{ID: keyErrorCanceled, Cause: err}
}

// sendOnce 完成一个渠道的完整生命周期: 创建 → Prepare → 发送或预览, 并组装 Result
// 创建/Prepare/客户端构造失败都进 Result.Error, 不中断其它渠道
// opts 携带渲染完成的标题正文与 Version/Translate 两个字段, 因此只接这一份入参
//
// sendOnce runs the full lifecycle of one channel, creation, Prepare, then the send or preview, and
// assembles the Result
// A creation, Prepare or client failure lands in Result.Error without interrupting the other
// channels
// opts carries the rendered title and body together with the Version and Translate fields, so one
// argument is all sendOnce takes
func sendOnce(ctx context.Context, spec config.Channel, defaults config.Defaults, vars *template.Vars, opts Options, now time.Time, dryRun bool) Result {
	result := Result{ID: spec.ID, Type: spec.Type}
	effective, err := spec.Effective(defaults)
	if err != nil {
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
		Title:     opts.Title,
		Body:      opts.Body,
		Vars:      vars,
		Now:       now,
		Mode:      channel.RenderStrict,
		Translate: opts.Translate,
	}
	prepared, warns, err := ch.Prepare(ctx, msg)
	if err != nil {
		result.Error = err
		return result
	}
	result.Warnings = append(result.Warnings, warns...)

	if dryRun {
		// dry-run 不需要 HTTP 客户端: 它只创建渠道, Prepare 并取预览
		//
		// A dry run needs no HTTP client: it only creates the channel, runs Prepare and takes the
		// preview
		preview := prepared.Preview()
		result.Preview = &preview
		return result
	}
	client, err := NewClient(effective.Proxy, effective.Timeout, opts.Version)
	if err != nil {
		result.Error = err
		return result
	}
	attempts, sendErr := sendWithRetry(ctx, prepared, client, effective.Retries, effective.RetryDelay)
	result.Attempts = attempts
	// Send 阶段警告经可选接口上送 (bark 已实现); Prepared 冻结接口只有 Send/Preview,
	// 因此只能类型断言读取
	// 失败路径同样合并: 警告描述的是本次尝试观察到的问题, 与最终成败无关
	//
	// Send-phase warnings come up through the optional interface (bark implements it), and since the
	// frozen Prepared interface carries Send and Preview only, the read goes through a type
	// assertion
	// The failure path merges them too, since a warning describes what this attempt observed rather
	// than the final verdict
	if reporter, ok := prepared.(warningsReporter); ok {
		result.Warnings = append(result.Warnings, reporter.Warnings()...)
	}
	if sendErr != nil {
		result.Error = sendErr
	}
	return result
}

// renderContent 用严格模式渲染标题或正文, 失败包成 notify.error.title_render/body_render,
// 并把内层模板 Problem 的 Path 覆写为 --title/--body, 让错误指向真正的来源
//
// renderContent renders the title or body in strict mode and wraps a failure into
// notify.error.title_render or body_render, overwriting the Path of the inner template Problem with
// --title or --body, which points the error at its real source
func renderContent(content string, vars *template.Vars, key, path string) (string, error) {
	out, _, err := template.RenderWithMode(content, vars, template.Strict)
	if err == nil {
		return out, nil
	}
	var prob *apperr.Problem
	if errors.As(err, &prob) {
		if prob.Data == nil {
			prob.Data = map[string]any{}
		}
		prob.Data["Path"] = path
	}
	// Detail 带上来源 flag: 外层 notify.error.*_render 文案只有 {{.Detail}} 占位符,
	// 内层模板 Problem 的 Path 不放进来就会丢失, 而验收场景要求错误里能看到 --title/--body
	//
	// Detail carries the source flag: the outer notify.error.*_render copy has only a {{.Detail}}
	// placeholder, so the Path of the inner template Problem would be lost without this prefix,
	// while the acceptance scenario requires --title or --body to appear in the error
	return "", &apperr.Problem{
		ID:    key,
		Data:  map[string]any{"Detail": path + ": " + renderDetail(err)},
		Cause: err,
	}
}

// renderDetail 从渲染错误里提取可读细节: 优先内层模板 Problem 的 Detail 或 Variable,
// 两者都没有时退回错误文本 (模板 Problem 的文本只含消息键, 不含内容)
//
// renderDetail picks a readable detail out of a render error, preferring the Detail or Variable of
// the inner template Problem and falling back to the error text, which holds a message key rather
// than the content
func renderDetail(err error) string {
	var prob *apperr.Problem
	if errors.As(err, &prob) {
		if detail, ok := prob.Data["Detail"].(string); ok && detail != "" {
			return prob.ID + ": " + detail
		}
		if variable, ok := prob.Data["Variable"].(string); ok && variable != "" {
			return prob.ID + ": " + variable
		}
	}
	return errorDetail(err)
}

// buildVars 合并变量来源: 内置时间/计时变量 → 变量文件 → CLI key=value
// CLI 值覆盖文件, 文件覆盖内置
//
// buildVars merges the variable sources: built-in time and elapsed variables, then the vars file,
// then the CLI key=value pairs
// A CLI value outranks the file and the file outranks the built-ins
func buildVars(varFile string, pairs []string, start time.Time, timed bool) (*template.Vars, error) {
	now := time.Now()
	var startMs int64
	if timed {
		if start.IsZero() {
			start = now
		}
		startMs = start.UnixMilli()
	}
	vars := template.BuildVars(now, startMs)

	if varFile != "" {
		if err := loadVarsFile(varFile, vars); err != nil {
			return nil, err
		}
	}
	if err := setCLIVars(pairs, vars); err != nil {
		return nil, err
	}
	return vars, nil
}

// loadVarsFile 读取变量文件 ("-" 为 stdin) 并合并进 vars, 上限 1MB
// 保留名在合并前从原始 JSON 键中检出, 因为 Vars 不提供键遍历, 合并后再检查无法区分
// 用户定义与内置变量; 单次读取也让 stdin 不会被消费两次
//
// loadVarsFile reads a vars file, "-" for stdin, and merges it into vars with a 1MB cap
// Reserved names are detected from the raw JSON keys before the merge, because Vars offers no key
// iteration and a post-merge check could not tell a user definition from a built-in; the single read
// also keeps stdin from being consumed twice
func loadVarsFile(path string, vars *template.Vars) error {
	var r io.Reader
	if path == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return varFileProblem(path, errorDetail(err), err)
		}
		defer f.Close()
		r = f
	}
	data, err := io.ReadAll(io.LimitReader(r, maxVarsFileBytes+1))
	if err != nil {
		return varFileProblem(path, errorDetail(err), err)
	}
	if len(data) > maxVarsFileBytes {
		return varFileProblem(path, "input exceeds the 1MB cap", nil)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err == nil {
		for key := range probe {
			if template.IsReservedVar(key) {
				return reservedVarProblem(key, "--var-file")
			}
		}
	}
	if err := template.MergeVarsJSON(data, vars); err != nil {
		return varFileProblem(path, errorDetail(err), err)
	}
	return nil
}

// setCLIVars 解析 CLI key=value 对并写入 vars, 保留名在写入前拒绝
// 预检查覆盖 SetCLIVars 的两种失败 (缺 "=" 与空键), 因此正常路径下 SetCLIVars 不会报错
//
// setCLIVars parses the CLI key=value pairs into vars and rejects a reserved name before writing
// The pre-check covers both SetCLIVars failures, a missing "=" and an empty key, so SetCLIVars does
// not report an error on the normal path
func setCLIVars(pairs []string, vars *template.Vars) error {
	for _, pair := range pairs {
		key, _, ok := strings.Cut(pair, "=")
		trimmed := strings.TrimSpace(key)
		switch {
		case !ok:
			return varProblem(pair, "expect key=value")
		case trimmed == "":
			return varProblem(pair, "empty key")
		case template.IsReservedVar(trimmed):
			return reservedVarProblem(trimmed, "--var")
		}
	}
	if err := template.SetCLIVars(pairs, vars); err != nil {
		// 防御分支: 预检查与 SetCLIVars 的规则出现漂移时仍返回结构化错误
		//
		// Defensive branch: a structured error still comes back if the pre-check and the SetCLIVars
		// rules ever drift apart
		return varProblem("", errorDetail(err))
	}
	return nil
}

// varFileProblem 构造 notify.error.var_file, Path 为调用方传入的文件位置
//
// varFileProblem builds notify.error.var_file with Path holding the location the caller passed
func varFileProblem(path, detail string, cause error) *apperr.Problem {
	return &apperr.Problem{
		ID:    keyErrorVarFile,
		Data:  map[string]any{"Path": path, "Detail": common.SanitizeString(detail)},
		Cause: cause,
	}
}

// varProblem 构造 notify.error.var, Value 为原始 CLI 片段, Reason 为脱敏后的原因
//
// varProblem builds notify.error.var with Value holding the raw CLI pair and Reason the redacted
// cause
func varProblem(value, reason string) *apperr.Problem {
	return &apperr.Problem{
		ID:   keyErrorVar,
		Data: map[string]any{"Value": value, "Reason": common.SanitizeString(reason)},
	}
}

// reservedVarProblem 构造 template.error.reserved_var 并把 Path 覆写为来源位置 (--var/--var-file)
//
// reservedVarProblem builds template.error.reserved_var and overwrites Path with the source
// position, --var or --var-file
func reservedVarProblem(variable, path string) *apperr.Problem {
	prob := template.ReservedVarProblem(variable)
	if prob.Data == nil {
		prob.Data = map[string]any{}
	}
	prob.Data["Path"] = path
	return prob
}

// errorDetail 提取错误文本作为技术细节并脱敏, 防止地址进入 Data
//
// errorDetail extracts an error text as technical detail and redacts it, which keeps an address from
// reaching Data
func errorDetail(err error) string {
	if err == nil {
		return ""
	}
	return common.SanitizeString(err.Error())
}

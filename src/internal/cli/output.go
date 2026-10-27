package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel/common"
	"github.com/MarchSnow-1/TaskPush/internal/i18n"
	"github.com/MarchSnow-1/TaskPush/internal/notify"
)

// ---------------------------------------------------------------------------
// 输出: 结果行与日志行都经 Translator 渲染, 前缀与渠道 ID/类型保持原样不翻译
// 结果行走 stdout, 进度与全局错误行走 stderr; 预览只渲染脱敏后的 Problem
//
// Output: result lines and log lines are rendered through the Translator, while the prefixes and
// the channel ID and type stay untranslated
// Result lines go to stdout and progress plus global errors go to stderr, and a preview renders a
// redacted Problem only
// ---------------------------------------------------------------------------

// prefixes 是固定的结果与日志前缀, 语言切换不影响它们
//
// prefixes holds the fixed result and log prefixes, which a language switch leaves unchanged
const (
	prefixOK    = "[OK]"
	prefixWarn  = "[WARN]"
	prefixError = "[ERROR]"
	prefixInfo  = "[INFO]"
)

// printer 负责一调用内的全部输出
//
// printer performs all output of one invocation
type printer struct {
	translator *i18n.Translator
	stdout     io.Writer
	stderr     io.Writer
}

// newPrinter 构造输出器, 输出流为空时落到 io.Discard 以避免空指针
//
// newPrinter builds the output helper, falling back to io.Discard for a nil stream so a nil writer
// cannot panic
func newPrinter(t *i18n.Translator, stdout, stderr io.Writer) *printer {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	return &printer{translator: t, stdout: stdout, stderr: stderr}
}

// T 渲染一个消息键
//
// T renders one message key
func (p *printer) T(id string, data map[string]any) string {
	return p.translator.T(id, data)
}

// info 在 stderr 输出一条 INFO 行
//
// info writes one INFO line to stderr
func (p *printer) info(id string, data map[string]any) {
	fmt.Fprintf(p.stderr, "%s %s\n", prefixInfo, p.T(id, data))
}

// warn 在 stderr 输出一条 WARN 行
//
// warn writes one WARN line to stderr
func (p *printer) warn(id string, data map[string]any) {
	fmt.Fprintf(p.stderr, "%s %s\n", prefixWarn, p.T(id, data))
}

// globalError 在 stderr 输出一条 ERROR 行
//
// globalError writes one ERROR line to stderr
func (p *printer) globalError(err error) {
	fmt.Fprintf(p.stderr, "%s %s\n", prefixError, p.renderError(err))
}

// renderError 把一个 error 渲染成用户文案
// *apperr.Problem 走消息键, 其它错误包成通用文案并做 URL 脱敏
//
// renderError renders an error as user copy
// An *apperr.Problem goes through its message key, while any other error is a transport-layer
// failure that never reached a response, so it renders through the request-failed copy with URL
// redaction applied
func (p *printer) renderError(err error) string {
	if err == nil {
		return ""
	}
	var problem *apperr.Problem
	if errors.As(err, &problem) {
		return p.renderProblem(problem)
	}
	return p.T("common.error.request_failed", map[string]any{
		"Detail": common.SanitizeString(err.Error()),
	})
}

// renderProblem 用 Problem 的消息键与占位符数据渲染文案
// Cause 只作为技术细节, 渲染前统一脱敏; Cause 是内层 *apperr.Problem 时不追加
// 内层 Problem 的 Error() 是 "ID: cause" 形态, 追加它会把 common.error.* 这类内部消息键
// 带进用户可见文案, 调试信息应留在调试层
//
// renderProblem renders a Problem through its message key and placeholder data
// Cause carries technical detail only and is redacted before rendering, while a Cause that is an
// inner *apperr.Problem is not appended at all
// The Error() of an inner Problem reads as "ID: cause", so appending it would carry internal
// message keys such as common.error.* into user-visible copy, and that detail belongs to the debug
// layer
func (p *printer) renderProblem(problem *apperr.Problem) string {
	if problem == nil {
		return ""
	}
	text := p.T(problem.ID, problem.Data)
	if problem.Cause == nil {
		return text
	}
	var inner *apperr.Problem
	if errors.As(problem.Cause, &inner) {
		return text
	}
	detail := common.SanitizeString(problem.Cause.Error())
	if detail == "" || strings.Contains(text, detail) {
		return text
	}
	return text + ": " + detail
}

// writeResults 输出一组发送结果, 顺序与渠道顺序一致
// 每个渠道先输出错误或成功行, 再输出它的警告; dry-run 只输出预览行
// 返回失败的渠道数量
//
// writeResults writes one group of send results in channel order
// Every channel writes its error or success line first and its warnings second, while a dry run
// writes preview lines only
// It returns the number of failed channels
func (p *printer) writeResults(results []notify.Result) int {
	failed := 0
	for _, result := range results {
		for _, warning := range result.Warnings {
			fmt.Fprintf(p.stdout, "%s %s: %s\n",
				prefixWarn,
				resultLabel(result.ID, result.Type),
				p.renderProblem(&warning),
			)
		}
		switch {
		case result.Error != nil:
			failed++
			fmt.Fprintf(p.stdout, "%s %s: %s\n",
				prefixError,
				resultLabel(result.ID, result.Type),
				p.renderError(result.Error),
			)
		case result.Preview != nil:
			fmt.Fprintf(p.stdout, "%s %s: %s\n",
				prefixOK,
				resultLabel(result.ID, result.Type),
				p.renderProblem(result.Preview),
			)
		default:
			fmt.Fprintf(p.stdout, "%s %s: %s\n",
				prefixOK,
				resultLabel(result.ID, result.Type),
				p.T("cli.result.ok", map[string]any{"Attempts": result.Attempts}),
			)
		}
	}
	return failed
}

// writeCheckRows 输出 check 结果表与汇总, 返回错误渠道数量
// check 的警告计入 WARN 状态, 只有错误才让退出码变成 1
// 带延后错误的渠道不输出自己的 OK 或 WARN 行, 因为延后错误已描述该渠道, 旁边的成功行会读成自相矛盾
// 错误行一律保留: 同一渠道的校验错误与延后错误各自成行, 互不替代
//
// writeCheckRows writes the check table and its summary and returns the number of failed channels
// A warning counts as a WARN status, and only an error turns the exit code into 1
// A channel carrying a deferred error loses its own OK or WARN row, because the deferred error
// already describes that channel and a success line next to it would read as a contradiction
// Every error row stays: the check errors of a channel and its deferred errors are kept side by
// side rather than replacing each other
func (p *printer) writeCheckRows(results []notify.CheckResult, deferred []apperr.Problem) int {
	tb := newTable(
		p.T("cli.table.id", nil),
		p.T("cli.table.type", nil),
		p.T("cli.table.status", nil),
		p.T("cli.table.detail", nil),
	)
	// 先记下哪些渠道有延后错误: 只有 Deferred 带 ChannelID 时才能对应上,
	// 回退到配置路径的行没有 ID, 因此不抑制任何行
	//
	// The channels owning a deferred error are collected first: only a Deferred carrying ChannelID
	// can be matched, and a row that fell back to the configuration path has no ID, so it suppresses
	// nothing
	suppressed := map[string]bool{}
	for i := range deferred {
		if id := deferredLabel(&deferred[i]); id != "" {
			suppressed[id] = true
		}
	}
	failed := 0
	for _, result := range results {
		status, detail := p.checkVerdict(result)
		if result.Error != nil {
			// 错误行一律保留, 与 Deferred 各自成行
			//
			// An error row always stays and keeps its own line next to the deferred ones
			failed++
		} else if suppressed[result.ID] {
			continue
		}
		tb.addRow(result.ID, result.Type, status, detail)
	}
	for i := range deferred {
		problem := deferred[i]
		// 延后错误带渠道归属: ID 列显示渠道名, TYPE 列显示渠道类型
		// 读不到 id 时 deferredLabel 回退到配置路径, 路径的下标从 1 开始, 因此仍能定位到第几个渠道
		//
		// A deferred error carries its owning channel, so the ID column shows the channel name
		// and the TYPE column shows its type
		// When the ID is unreadable deferredLabel falls back to the configuration path, whose
		// subscript starts at 1, which still points at which channel the problem belongs to
		tb.addRow(deferredLabel(&problem), deferredType(&problem), prefixError, p.renderProblem(&problem))
		failed++
	}
	tb.render(p.stdout)
	return failed
}

// deferredLabel 取延后错误的渠道标识, 依次尝试渠道 ID, 配置路径与来源标签
// 渠道 ID 最贴近用户的认知, 配置路径是没有 ID 时的退路, 至少能指出是第几个渠道条目
//
// deferredLabel picks the channel identifier of a deferred error, trying the channel ID, the
// configuration path and the source label in that order
// The channel ID comes closest to how a user thinks about the configuration, while the path is the
// fallback without an ID and at least points at which channel entry is meant
func deferredLabel(problem *apperr.Problem) string {
	return deferredData(problem, "ChannelID", "Path", "Source")
}

// deferredType 取延后错误的渠道类型, 读不到时留空让 TYPE 列显示空单元格
//
// deferredType picks the channel type of a deferred error, and an unreadable value stays empty so
// the TYPE column renders an empty cell
func deferredType(problem *apperr.Problem) string {
	return deferredData(problem, "ChannelType")
}

// deferredData 按给定顺序取第一个非空的字符串字段, problem 为 nil 时返回空串
//
// deferredData returns the first non-empty string field in the given order, and a nil problem
// yields an empty string
func deferredData(problem *apperr.Problem, keys ...string) string {
	if problem == nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := problem.Data[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

// checkVerdict 把一个 check 结果转成状态与说明
//
// checkVerdict turns one check result into a status and a description
func (p *printer) checkVerdict(result notify.CheckResult) (string, string) {
	switch {
	case result.Error != nil:
		return prefixError, p.renderError(result.Error)
	case len(result.Warnings) > 0:
		return prefixWarn, p.renderProblem(&result.Warnings[0])
	case result.Preview != nil:
		return prefixOK, p.renderProblem(result.Preview)
	default:
		return prefixOK, ""
	}
}

// resultLabel 拼出结果行里的 "id (type)" 片段, 渠道标识不翻译
//
// resultLabel builds the "id (type)" fragment of a result line, with the channel identifier left
// untranslated
func resultLabel(id, typ string) string {
	return fmt.Sprintf("%s (%s)", id, typ)
}

// writeSummary 输出发送汇总: 全部成功, 全部失败或部分失败
//
// writeSummary writes the send summary: all succeeded, all failed or partly failed
func (p *printer) writeSummary(total, failed int) {
	switch {
	case failed == 0:
		fmt.Fprintf(p.stdout, "%s\n", p.T("cli.result.summary_ok", map[string]any{"Count": total}))
	case failed == total:
		fmt.Fprintf(p.stdout, "%s\n", p.T("cli.result.fail_not_sent", nil))
	default:
		fmt.Fprintf(p.stdout, "%s\n", p.T("cli.result.summary_fail", map[string]any{"Count": failed}))
	}
}

package apperr

import "fmt"

// ---------------------------------------------------------------------------
// 结构化错误: 只携带消息键与占位符数据, 不带翻译结果
// 内部包返回 Problem, 由 CLI 按当前语言渲染, 语言因此不在调用链中被固定
//
// Structured errors: they carry a message key and placeholder data, never translated text
// Internal packages return a Problem and the CLI renders it in the active language, which
// keeps the language from being fixed somewhere along the call chain
// ---------------------------------------------------------------------------

// Problem 是用户可见错误的结构化载体
// ID 为 i18n 消息键, Data 的键名与文案里的 {{.Name}} 占位符一致, Cause 只放技术细节
//
// Problem is the structured carrier of a user-visible error
// ID holds the i18n message key, the keys of Data match the {{.Name}} placeholders in the
// message text, and Cause carries technical detail only
type Problem struct {
	ID    string
	Data  map[string]any
	Cause error
}

// Error 返回调试文本: 有 Cause 时输出 "ID: cause", 否则只输出 ID
// 该文本供日志与测试定位用, 不是最终用户文案, 用户文案由 i18n.Translator.T 渲染
//
// Error returns debugging text: "ID: cause" when a Cause is set, and the bare ID otherwise
// The text serves log and test diagnostics rather than being final user copy, which
// i18n.Translator.T renders instead
func (p *Problem) Error() string {
	if p == nil {
		return ""
	}
	if p.Cause != nil {
		return fmt.Sprintf("%s: %s", p.ID, p.Cause.Error())
	}
	return p.ID
}

// Unwrap 返回 Cause, 让 errors.Is / errors.As 能穿透到被包装的底层错误
//
// Unwrap returns Cause, which lets errors.Is and errors.As reach through to the wrapped
// underlying error
func (p *Problem) Unwrap() error {
	if p == nil {
		return nil
	}
	return p.Cause
}

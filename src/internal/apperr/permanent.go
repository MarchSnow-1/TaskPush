package apperr

import "errors"

// ---------------------------------------------------------------------------
// 永久错误标记: 配置/内容/平台确定性失败用它包装, 调度层据此跳过重试
//
// The permanent-error marker: configuration, content and platform failures that are
// deterministic get wrapped with it, and the scheduling layer reads it to skip retries
// ---------------------------------------------------------------------------

// errPermanent 是永久错误的内部哨兵
// 不直接暴露给调用方: 包装类型只在 Is 里比对它, Error 仍然透出原错误文本
//
// errPermanent is the internal sentinel for a permanent error
// It is not exposed to callers directly, since the wrapper compares against it in Is only
// while Error still surfaces the original error text
var errPermanent = errors.New("permanent error")

// permanentError 包装不可重试错误
// Error 转发原错误文本, Unwrap 保留原错误, Is 只在目标为哨兵时返回 true
//
// permanentError wraps a non-retryable error
// Error forwards the original text, Unwrap keeps the original error reachable, and Is
// answers true only when the target is the sentinel
type permanentError struct {
	err error
}

// Error 返回被包装错误的文本
//
// Error returns the text of the wrapped error
func (e *permanentError) Error() string { return e.err.Error() }

// Unwrap 返回被包装的错误, 保持 errors.Is / errors.As 的穿透能力
//
// Unwrap returns the wrapped error and keeps errors.Is and errors.As able to reach through
func (e *permanentError) Unwrap() error { return e.err }

// Is 只在目标为永久错误哨兵时报告匹配
// 这样 errors.Is(err, errPermanent) 不会被底层错误自身的 Is 结果干扰
//
// Is reports a match only when the target is the permanent sentinel, which keeps
// errors.Is(err, errPermanent) from being influenced by the Is result of the underlying error
func (e *permanentError) Is(target error) bool { return target == errPermanent }

// Permanent 把 err 包装为不可重试错误, nil 进 nil 出
// 渠道的配置与内容校验类错误走这里, 重试不会改变这类判定
// 包装只在外层加上可重试性标记, Problem 的 ID 与 Data 不被改写
//
// Permanent wraps err as a non-retryable error and returns nil for a nil input
// Configuration and content validation errors in a channel take this path, since a retry
// does not change that verdict
// The wrapping adds the retryability marker at the outer layer only, leaving the ID and
// Data of a Problem as they were
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

// IsPermanent 判断 err 链上是否出现永久错误标记
// 借助 errors.Is 穿透 Permanent 包装与 fmt.Errorf("...: %w") 之类的再包装
//
// IsPermanent reports whether the permanent marker appears anywhere in the err chain
// errors.Is reaches through both the Permanent wrapper and further wrapping such as
// fmt.Errorf("...: %w")
func IsPermanent(err error) bool {
	return errors.Is(err, errPermanent)
}

package state

// ---------------------------------------------------------------------------
// 计时状态文件: start 写入计时起点, finish 读取后计算耗时
// 磁盘格式为 {"version":1,"startedAt":"RFC3339Nano"}, 未知字段忽略, 读取容忍 UTF-8 BOM
// 文件只存版本号与计时起点, 不含配置内容与密钥
//
// The timing state file: start records the start time and finish reads it to compute elapsed time
// The on-disk format is {"version":1,"startedAt":"RFC3339Nano"}, unknown fields are ignored and a
// UTF-8 BOM is tolerated on read
// The file holds the version and the start time only, with no configuration content and no secrets
// ---------------------------------------------------------------------------

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
)

const (
	// stateVersion 是当前写入的格式版本, 读取时只接受该值
	//
	// stateVersion is the format version written today, and reading accepts that value alone
	stateVersion = 1
	// defaultStateFileName 是未传 --state-file 时使用的状态文件名
	//
	// defaultStateFileName is the state file name used when --state-file is not passed
	defaultStateFileName = ".taskpush-state.json"
	// tempFilePatternSuffix 拼在目标文件名之后, 形成同目录临时文件的随机名模板
	//
	// tempFilePatternSuffix is appended to the target file name to build the random-name pattern
	// of the temp file in the same directory
	tempFilePatternSuffix = ".tmp-*"
	// stateFileMode 是目标文件的权限位, Windows 上权限位语义不同, 可读可写由文件系统决定
	//
	// stateFileMode is the permission bits of the target file, while Windows treats the bits
	// differently and leaves readability and writability to the file system
	stateFileMode = 0o600
	// utf8BOM 是 UTF-8 字节序标记, 读取时容忍它出现在文件开头
	//
	// utf8BOM is the UTF-8 byte order mark, tolerated at the start of the file on read
	utf8BOM = "\xEF\xBB\xBF"
)

const (
	// errKeyParse 是状态文件 JSON 损坏时的消息键
	//
	// errKeyParse is the message key for a state file whose JSON is broken
	errKeyParse = "state.error.parse"
	// errKeyInvalid 是 startedAt 缺失或为零值时的消息键
	//
	// errKeyInvalid is the message key for a missing or zero startedAt
	errKeyInvalid = "state.error.invalid"
	// errKeyVersion 是状态文件版本不受支持时的消息键
	//
	// errKeyVersion is the message key for a state file whose version is not supported
	errKeyVersion = "state.error.version"
	// errKeyWrite 是写入状态文件失败时的消息键, 文案里会提示 --state-file
	//
	// errKeyWrite is the message key for a failed state file write, and its copy points at
	// --state-file
	errKeyWrite = "state.error.write"
	// errKeyRemove 是删除状态文件失败时的消息键
	//
	// errKeyRemove is the message key for a failed state file removal
	errKeyRemove = "state.error.remove"
)

// State 是状态文件的磁盘结构
// StartedAt 按 RFC3339 的纳秒精度编解码, 解码时忽略未知字段
//
// State is the on-disk shape of the state file
// StartedAt is encoded and decoded at RFC3339 nanosecond precision, and unknown fields are
// ignored while decoding
type State struct {
	Version   int       `json:"version"`
	StartedAt time.Time `json:"startedAt"`
}

// DefaultPath 返回 dir 下的默认状态文件路径
// 相对 dir 按调用方的工作目录解析, dir 为空串时落在当前工作目录
//
// DefaultPath returns the default state file path under dir
// A relative dir is resolved against the working directory of the caller, and an empty dir
// lands in the current working directory
func DefaultPath(dir string) string {
	return filepath.Join(dir, defaultStateFileName)
}

// Read 读取状态文件并返回计时起点
// 文件不存在时原样返回包装 os.ErrNotExist 的错误, 由调用方用 errors.Is 判定并渲染 state.error.missing
// JSON 损坏返回 ID 为 state.error.parse 的 *apperr.Problem, 版本不是支持的 1 返回 state.error.version,
// startedAt 缺失或为零值返回 state.error.invalid
// 这三种失败都不写文件, 磁盘上的原内容保留
//
// Read reads the state file and returns its start time
// A missing file comes back as an error wrapping os.ErrNotExist, so the caller can detect it with
// errors.Is and render state.error.missing
// Broken JSON returns an *apperr.Problem whose ID is state.error.parse, a version other than the
// supported one returns state.error.version, and a missing or zero startedAt returns
// state.error.invalid
// Neither failure writes to the file, so the on-disk content stays as it was
func Read(path string) (time.Time, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, err
	}

	var parsed State
	if err := json.Unmarshal(trimBOM(raw), &parsed); err != nil {
		return time.Time{}, &apperr.Problem{
			ID: errKeyParse,
			Data: map[string]any{
				"Path":   path,
				"Detail": err.Error(),
			},
			Cause: err,
		}
	}
	// 版本校验只针对非零值: 旧工具写下的缺 version 文件按 version 1 读取, 不把补齐字段当作版本升级
	//
	// The version check covers a non-zero value only, so a file without the version field written by
	// an older tool still reads as version 1 rather than failing as an upgrade
	if parsed.Version != 0 && parsed.Version != stateVersion {
		return time.Time{}, &apperr.Problem{
			ID:   errKeyVersion,
			Data: map[string]any{"Path": path, "Version": parsed.Version},
		}
	}
	if parsed.StartedAt.IsZero() {
		return time.Time{}, &apperr.Problem{
			ID:   errKeyInvalid,
			Data: map[string]any{"Path": path},
		}
	}
	return parsed.StartedAt, nil
}

// Write 把计时起点写入 path, 版本号取 stateVersion
// 写入走同目录临时文件加 rename 的原子路径, 目标已存在时被整体替换, 读取方不会看到半写内容
// 父目录不存在时直接报错且不自动创建, state.error.write 的文案提示 --state-file
// 写入, 同步, 关闭, 改权限, rename 任一步失败都会关闭并删除临时文件, 已存在的目标文件保持失败前的内容
// startedAt 为零值时按原值写入, 由 Read 侧按 state.error.invalid 拒绝
//
// Write stores the start time at path with the version set to stateVersion
// The write goes through a temp file in the same directory plus a rename, so an existing target is
// replaced as a whole and a reader does not observe half-written content
// A missing parent directory fails without being created, and the copy of state.error.write points
// at --state-file
// A failure in writing, syncing, closing, chmod or the rename closes and removes the temp file,
// while an existing target keeps the content it had before the failure
// A zero startedAt is written as it is and rejected later by Read as state.error.invalid
func Write(path string, startedAt time.Time) error {
	payload, err := json.Marshal(State{Version: stateVersion, StartedAt: startedAt.UTC()})
	if err != nil {
		return writeProblem(path, err)
	}
	payload = append(payload, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+tempFilePatternSuffix)
	if err != nil {
		return writeProblem(path, err)
	}
	tmpName := tmp.Name()

	// fail 关闭临时文件并删除它, 再把底层错误包装成 state.error.write
	// 清理自身的错误不参与返回值, 该分支要报的是最初的失败原因
	//
	// fail closes and removes the temp file and then wraps the underlying error as
	// state.error.write
	// An error from the cleanup itself is not part of the return value, since this branch reports
	// the original failure
	fail := func(cause error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return writeProblem(path, cause)
	}

	if _, err := tmp.Write(payload); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		return fail(err)
	}
	if err := os.Chmod(tmpName, stateFileMode); err != nil {
		return fail(err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fail(err)
	}
	return nil
}

// Remove 删除状态文件, 文件不存在按成功处理
// 该幂等语义让 finish 重跑时不必区分文件是否已被上一次成功发送删掉
// 其余失败返回 ID 为 state.error.remove 的 *apperr.Problem
//
// Remove deletes the state file and treats a missing file as success
// That idempotent rule saves finish from distinguishing a file already removed by an earlier
// successful send
// Any other failure returns an *apperr.Problem whose ID is state.error.remove
func Remove(path string) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return &apperr.Problem{
		ID: errKeyRemove,
		Data: map[string]any{
			"Path":   path,
			"Detail": err.Error(),
		},
		Cause: err,
	}
}

// trimBOM 去掉文件开头的 UTF-8 BOM, 其余字节原样返回
// 只有出现在开头的 BOM 被忽略, 出现在别处的字节交给 JSON 解码器判定
//
// trimBOM drops a UTF-8 BOM at the start of the file and returns the remaining bytes as they are
// Only a BOM in the leading position is ignored, while bytes elsewhere go to the JSON decoder for
// its own verdict
func trimBOM(raw []byte) []byte {
	return bytes.TrimPrefix(raw, []byte(utf8BOM))
}

// writeProblem 把写入失败包装成 ID 为 state.error.write 的 *apperr.Problem
// Data 带 Path 与 Detail, Detail 取底层错误文本, 不含状态文件的正文
//
// writeProblem wraps a write failure into an *apperr.Problem whose ID is state.error.write
// Data carries Path and Detail, where Detail is the underlying error text and holds no state file
// content
func writeProblem(path string, cause error) *apperr.Problem {
	return &apperr.Problem{
		ID: errKeyWrite,
		Data: map[string]any{
			"Path":   path,
			"Detail": cause.Error(),
		},
		Cause: cause,
	}
}

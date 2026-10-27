package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/i18n"
)

// exampleStart, exampleRestart, exampleStale 是测试用的固定时间, 避免依赖真实当前时间
//
// exampleStart, exampleRestart and exampleStale are fixed times used by the tests, which keeps
// them from depending on the real current time
var (
	exampleStart   = time.Date(2026, 10, 18, 12, 0, 0, 123456789, time.UTC)
	exampleRestart = time.Date(2026, 10, 18, 13, 30, 0, 0, time.UTC)
	exampleStale   = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
)

// payloadFor 生成一份合法的状态文件正文, 起点按 RFC3339Nano 写
//
// payloadFor builds one valid state file body with its start time in RFC3339Nano
func payloadFor(startedAt time.Time) string {
	return `{"version":1,"startedAt":"` + startedAt.Format(time.RFC3339Nano) + `"}`
}

// writeRaw 按给定内容写一个测试文件
//
// writeRaw writes a test file with the given content
func writeRaw(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// readRaw 读回测试文件的原文
//
// readRaw reads a test file back as it is
func readRaw(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// listDir 返回目录下的条目名, os.ReadDir 已按名字排序
//
// listDir returns the entry names of a directory, already sorted by os.ReadDir
func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// assertProblem 断言 err 是 ID 为 wantID 的 *apperr.Problem 并返回它
//
// assertProblem asserts that err is an *apperr.Problem with the ID wantID and returns it
func assertProblem(t *testing.T, err error, wantID string) *apperr.Problem {
	t.Helper()
	if err == nil {
		t.Fatalf("error is nil, want a Problem with ID %q", wantID)
	}
	var problem *apperr.Problem
	if !errors.As(err, &problem) {
		t.Fatalf("error = %T (%v), want *apperr.Problem", err, err)
	}
	if problem.ID != wantID {
		t.Fatalf("Problem.ID = %q, want %q", problem.ID, wantID)
	}
	return problem
}

// TestDefaultPath 覆盖默认文件名与不同 dir 输入下的拼接结果
//
// TestDefaultPath covers the default file name and the joined result for several dir inputs
func TestDefaultPath(t *testing.T) {
	cases := []struct {
		name string
		dir  string
		want string
	}{
		{name: "relative dir", dir: "example", want: filepath.Join("example", ".taskpush-state.json")},
		{name: "nested relative dir", dir: filepath.Join("example", "nested"), want: filepath.Join("example", "nested", ".taskpush-state.json")},
		{name: "current dir", dir: ".", want: ".taskpush-state.json"},
		{name: "empty dir", dir: "", want: ".taskpush-state.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DefaultPath(tc.dir)
			if got != tc.want {
				t.Fatalf("DefaultPath(%q) = %q, want %q", tc.dir, got, tc.want)
			}
			if base := filepath.Base(got); base != ".taskpush-state.json" {
				t.Fatalf("base name = %q, want .taskpush-state.json", base)
			}
		})
	}
}

// TestWriteReadRoundTrip 覆盖写入后按原值读回, 以及磁盘格式的版本与时间字段
//
// TestWriteReadRoundTrip covers reading back what was written, plus the version and time fields of
// the on-disk format
func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := DefaultPath(dir)

	if err := Write(path, exampleStart); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !got.Equal(exampleStart) {
		t.Fatalf("Read = %s, want %s", got.Format(time.RFC3339Nano), exampleStart.Format(time.RFC3339Nano))
	}

	wantPrefix := `{"version":1,"startedAt":"`
	raw := readRaw(t, path)
	if !strings.HasPrefix(raw, wantPrefix) {
		t.Fatalf("file starts with %q, want %q", raw, wantPrefix)
	}
	if !strings.Contains(raw, exampleStart.Format(time.RFC3339Nano)) {
		t.Fatalf("file %q does not hold the RFC3339Nano start time", raw)
	}
	if !strings.HasSuffix(raw, "\n") {
		t.Fatalf("file %q does not end with a newline", raw)
	}
	if strings.HasPrefix(raw, utf8BOM) {
		t.Fatal("the written file starts with a BOM")
	}
}

// TestWriteNormalizesToUTC 覆盖非 UTC 起点按同一时刻写入 Z 形式
//
// TestWriteNormalizesToUTC covers a non-UTC start time being written in Z form for the same instant
func TestWriteNormalizesToUTC(t *testing.T) {
	dir := t.TempDir()
	path := DefaultPath(dir)
	zone := time.FixedZone("example", 9*60*60)
	startedAt := time.Date(2026, 10, 18, 21, 0, 0, 0, zone)

	if err := Write(path, startedAt); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !got.Equal(startedAt) {
		t.Fatalf("Read = %s, want the same instant as %s", got.Format(time.RFC3339Nano), startedAt.Format(time.RFC3339Nano))
	}
	if got.Location() != time.UTC {
		t.Fatalf("Read location = %v, want UTC", got.Location())
	}
	if raw := readRaw(t, path); raw != payloadFor(startedAt.UTC())+"\n" {
		t.Fatalf("file = %q, want %q", raw, payloadFor(startedAt.UTC())+"\n")
	}
}

// TestWriteOverwritesExistingTarget 覆盖目标已存在时整体替换, 且不留下临时文件
//
// TestWriteOverwritesExistingTarget covers replacing an existing target as a whole without leaving
// a temp file behind
func TestWriteOverwritesExistingTarget(t *testing.T) {
	dir := t.TempDir()
	path := DefaultPath(dir)

	if err := Write(path, exampleStale); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	if err := Write(path, exampleRestart); err != nil {
		t.Fatalf("second Write: %v", err)
	}

	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !got.Equal(exampleRestart) {
		t.Fatalf("Read = %s, want the second start time %s", got.Format(time.RFC3339Nano), exampleRestart.Format(time.RFC3339Nano))
	}
	if entries := listDir(t, dir); len(entries) != 1 || entries[0] != defaultStateFileName {
		t.Fatalf("directory entries = %v, want only %s", entries, defaultStateFileName)
	}
}

// TestReadMissingFile 覆盖缺失文件返回包装 os.ErrNotExist 的错误, 而不是 Problem
//
// TestReadMissingFile covers a missing file returning an error wrapping os.ErrNotExist rather than
// a Problem
func TestReadMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := DefaultPath(dir)

	got, err := Read(path)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Read error = %v, want it to wrap os.ErrNotExist", err)
	}
	if !got.IsZero() {
		t.Fatalf("Read returned %s for a missing file", got.Format(time.RFC3339Nano))
	}
	var problem *apperr.Problem
	if errors.As(err, &problem) {
		t.Fatalf("Read mapped a missing file to Problem %q; the CLI owns state.error.missing", problem.ID)
	}
}

// TestReadToleratesBOM 覆盖带 UTF-8 BOM 与不带 BOM 的两种合法文件
//
// TestReadToleratesBOM covers a valid file with and without a UTF-8 BOM
func TestReadToleratesBOM(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
	}{
		{name: "no BOM"},
		{name: "UTF-8 BOM", prefix: utf8BOM},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := DefaultPath(dir)
			writeRaw(t, path, tc.prefix+payloadFor(exampleStart))

			got, err := Read(path)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if !got.Equal(exampleStart) {
				t.Fatalf("Read = %s, want %s", got.Format(time.RFC3339Nano), exampleStart.Format(time.RFC3339Nano))
			}
		})
	}
}

// TestReadIgnoresUnknownFields 覆盖额外字段被忽略
//
// TestReadIgnoresUnknownFields covers extra fields being ignored
func TestReadIgnoresUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := DefaultPath(dir)
	writeRaw(t, path, `{"version":1,"startedAt":"`+exampleStart.Format(time.RFC3339Nano)+`","exampleExtra":{"nested":true}}`)

	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !got.Equal(exampleStart) {
		t.Fatalf("Read = %s, want %s", got.Format(time.RFC3339Nano), exampleStart.Format(time.RFC3339Nano))
	}
}

// TestReadRejectsBrokenFiles 覆盖损坏 JSON, 不支持的版本与零值起点分别映射到 parse, version 与
// invalid, 且原文件不被改写
//
// TestReadRejectsBrokenFiles covers broken JSON, an unsupported version and a zero start time mapping
// to parse, version and invalid respectively, with the original file left untouched
func TestReadRejectsBrokenFiles(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		wantID  string
	}{
		{name: "not json", payload: "example, not json", wantID: errKeyParse},
		{name: "truncated object", payload: `{"version":1,`, wantID: errKeyParse},
		{name: "empty file", payload: "", wantID: errKeyParse},
		{name: "array document", payload: `[]`, wantID: errKeyParse},
		{name: "unparsable time", payload: `{"version":1,"startedAt":"not-a-time"}`, wantID: errKeyParse},
		{name: "unsupported version", payload: `{"version":99,"startedAt":"2026-01-02T03:04:05Z"}`, wantID: errKeyVersion},
		{name: "missing startedAt", payload: `{"version":1}`, wantID: errKeyInvalid},
		{name: "zero startedAt", payload: `{"version":1,"startedAt":"0001-01-01T00:00:00Z"}`, wantID: errKeyInvalid},
		{name: "empty object", payload: `{}`, wantID: errKeyInvalid},
		{name: "null document", payload: `null`, wantID: errKeyInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := DefaultPath(dir)
			writeRaw(t, path, tc.payload)

			got, err := Read(path)
			problem := assertProblem(t, err, tc.wantID)
			if !got.IsZero() {
				t.Fatalf("Read returned %s for a broken file", got.Format(time.RFC3339Nano))
			}
			if problem.Data["Path"] != path {
				t.Fatalf("Problem.Data[Path] = %v, want %q", problem.Data["Path"], path)
			}
			if raw := readRaw(t, path); raw != tc.payload {
				t.Fatalf("Read changed the file: %q, want %q", raw, tc.payload)
			}

			detail, hasDetail := problem.Data["Detail"]
			if tc.wantID == errKeyParse {
				text, _ := detail.(string)
				if !hasDetail || strings.TrimSpace(text) == "" {
					t.Fatalf("Problem.Data[Detail] = %v, want the decoder detail", detail)
				}
			}
			if tc.wantID == errKeyInvalid && hasDetail {
				t.Fatalf("state.error.invalid has no Detail placeholder, got %v", problem.Data)
			}
		})
	}
}

// TestWriteMissingParentDirectory 覆盖父目录不存在时报错且不自动创建目录
//
// TestWriteMissingParentDirectory covers a missing parent directory failing without creating it
func TestWriteMissingParentDirectory(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "example-missing")
	path := DefaultPath(parent)

	err := Write(path, exampleStart)
	problem := assertProblem(t, err, errKeyWrite)
	if problem.Data["Path"] != path {
		t.Fatalf("Problem.Data[Path] = %v, want %q", problem.Data["Path"], path)
	}
	if detail, _ := problem.Data["Detail"].(string); strings.TrimSpace(detail) == "" {
		t.Fatalf("Problem.Data[Detail] = %v, want the underlying error text", problem.Data["Detail"])
	}
	// Problem 保留 Cause, 所以调用方仍能用 errors.Is 判定底层原因
	//
	// The Problem keeps its Cause, so a caller can still classify the underlying reason with errors.Is
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Write error = %v, want it to wrap os.ErrNotExist", err)
	}
	if _, statErr := os.Stat(parent); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Write created the parent directory: stat error = %v", statErr)
	}
	if entries := listDir(t, dir); len(entries) != 0 {
		t.Fatalf("directory entries = %v, want an empty directory", entries)
	}
}

// TestWriteCleansTempOnRenameFailure 覆盖 rename 失败时临时文件被清理
// 目标路径放一个目录, rename 在 Unix 与 Windows 上都会失败, 该场景不依赖只读权限
//
// TestWriteCleansTempOnRenameFailure covers the temp file being cleaned up when the rename fails
// A directory at the target path makes the rename fail on both Unix and Windows, so the case does
// not rely on read-only permissions
func TestWriteCleansTempOnRenameFailure(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "example-blocked")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", target, err)
	}

	err := Write(target, exampleStart)
	problem := assertProblem(t, err, errKeyWrite)
	if problem.Data["Path"] != target {
		t.Fatalf("Problem.Data[Path] = %v, want %q", problem.Data["Path"], target)
	}
	if entries := listDir(t, parent); len(entries) != 1 || entries[0] != "example-blocked" {
		t.Fatalf("parent entries = %v, want only the blocking directory and no temp file", entries)
	}
	if entries := listDir(t, target); len(entries) != 0 {
		t.Fatalf("blocking directory holds %v, want nothing", entries)
	}
}

// TestWriteReadOnlyParentUnix 覆盖 Unix 上父目录不可写时的写入失败与目录无残留
// Windows 的目录只读位不阻止创建文件, 该平台跳过; 以 root 或带 CAP_DAC_OVERRIDE 运行时权限位同样不生效, 此时先探测再跳过
//
// TestWriteReadOnlyParentUnix covers a failed write into a non-writable parent directory on Unix
// with nothing left behind
// Windows skips it because the directory read-only bit does not stop file creation there, and a
// process running as root or with CAP_DAC_OVERRIDE is probed first and then skipped, since the
// permission bits do not apply to it either
func TestWriteReadOnlyParentUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows read-only directory bit does not stop file creation")
	}
	dir := t.TempDir()
	readOnly := filepath.Join(dir, "example-readonly")
	if err := os.Mkdir(readOnly, 0o500); err != nil {
		t.Fatalf("mkdir %s: %v", readOnly, err)
	}
	probe, probeErr := os.CreateTemp(readOnly, "example-probe-*")
	if probeErr == nil {
		name := probe.Name()
		_ = probe.Close()
		_ = os.Remove(name)
		t.Skip("the process can create files in a read-only directory, so the permission bits do not apply")
	}

	err := Write(DefaultPath(readOnly), exampleStart)
	assertProblem(t, err, errKeyWrite)
	if entries := listDir(t, readOnly); len(entries) != 0 {
		t.Fatalf("read-only directory entries = %v, want no temp file", entries)
	}
}

// TestWritePermissions 覆盖 Unix 上的 0600, Windows 上只断言文件可打开写入
//
// TestWritePermissions covers 0600 on Unix and asserts only that the file opens for writing on
// Windows
func TestWritePermissions(t *testing.T) {
	dir := t.TempDir()
	path := DefaultPath(dir)
	if err := Write(path, exampleStart); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if runtime.GOOS == "windows" {
		handle, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			t.Fatalf("OpenFile for write: %v", err)
		}
		if err := handle.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		return
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("state file mode = %#o, want %#o", perm, 0o600)
	}
}

// TestRemoveIsIdempotent 覆盖删除成功与文件不存在按成功处理
//
// TestRemoveIsIdempotent covers a successful removal and a missing file counting as success
func TestRemoveIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := DefaultPath(dir)

	if err := Remove(path); err != nil {
		t.Fatalf("Remove of a missing file = %v, want nil", err)
	}
	if err := Write(path, exampleStart); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := Read(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Read after Remove error = %v, want os.ErrNotExist", err)
	}
	if err := Remove(path); err != nil {
		t.Fatalf("second Remove = %v, want nil", err)
	}
	if entries := listDir(t, dir); len(entries) != 0 {
		t.Fatalf("directory entries = %v, want an empty directory", entries)
	}
}

// TestRemoveFailureOnDirectory 覆盖非空目录删除失败返回 remove 错误且目录仍在
//
// TestRemoveFailureOnDirectory covers a failed removal of a non-empty directory returning a remove
// error while the directory stays
func TestRemoveFailureOnDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "example-dir")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", target, err)
	}
	writeRaw(t, filepath.Join(target, "example.txt"), "example")

	err := Remove(target)
	problem := assertProblem(t, err, errKeyRemove)
	if problem.Data["Path"] != target {
		t.Fatalf("Problem.Data[Path] = %v, want %q", problem.Data["Path"], target)
	}
	if detail, _ := problem.Data["Detail"].(string); strings.TrimSpace(detail) == "" {
		t.Fatalf("Problem.Data[Detail] = %v, want the underlying error text", problem.Data["Detail"])
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Fatalf("the target directory disappeared: %v", statErr)
	}
}

// TestConcurrentWritesToDistinctPaths 覆盖同一目录内并发写入各自的临时文件与结果
// 同一个目标的并发 rename 不在契约范围内, 该场景不在此断言
//
// TestConcurrentWritesToDistinctPaths covers concurrent writes of separate temp files and results
// inside one directory
// Concurrent renames of the same target are outside the contract and are not asserted here
func TestConcurrentWritesToDistinctPaths(t *testing.T) {
	dir := t.TempDir()
	const writers = 4

	paths := make([]string, writers)
	starts := make([]time.Time, writers)
	errs := make([]error, writers)
	var wg sync.WaitGroup

	for i := 0; i < writers; i++ {
		paths[i] = filepath.Join(dir, fmt.Sprintf("example-%d.json", i))
		starts[i] = exampleStart.Add(time.Duration(i) * time.Minute)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := Write(paths[i], starts[i]); err != nil {
				errs[i] = err
				return
			}
			got, err := Read(paths[i])
			if err != nil {
				errs[i] = err
				return
			}
			if !got.Equal(starts[i]) {
				errs[i] = fmt.Errorf("Read = %s, want %s", got.Format(time.RFC3339Nano), starts[i].Format(time.RFC3339Nano))
			}
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("writer %d: %v", i, err)
		}
	}
	if entries := listDir(t, dir); len(entries) != writers {
		t.Fatalf("directory entries = %v, want %d files and no temp leftovers", entries, writers)
	}
}

// TestStateMessagesRenderEveryKey 覆盖 en 与 zh-CN 都具备 10 个状态键, 占位符可渲染且无结尾句号
//
// TestStateMessagesRenderEveryKey covers both en and zh-CN holding the ten state keys, with
// renderable placeholders and no trailing full stop
func TestStateMessagesRenderEveryKey(t *testing.T) {
	examplePath := filepath.Join("example", ".taskpush-state.json")
	exampleTime := exampleStart.Format(time.RFC3339Nano)
	keys := map[string]map[string]any{
		"state.error.missing":    {"Path": examplePath},
		"state.error.parse":      {"Path": examplePath, "Detail": "example detail"},
		"state.error.invalid":    {"Path": examplePath},
		"state.error.version":    {"Path": examplePath, "Version": 99},
		"state.error.write":      {"Path": examplePath, "Detail": "example detail"},
		"state.error.remove":     {"Path": examplePath, "Detail": "example detail"},
		"state.warn.future":      {"Time": exampleTime},
		"state.info.overwritten": {"Path": examplePath},
		"state.info.written":     {"Path": examplePath, "Time": exampleTime},
		"state.info.kept":        {"Path": examplePath},
	}
	if len(keys) != 10 {
		t.Fatalf("the table holds %d keys, want the ten state keys", len(keys))
	}

	for _, lang := range []string{"en", "zh-CN"} {
		translator, err := i18n.New(lang)
		if err != nil {
			t.Fatalf("i18n.New(%s): %v", lang, err)
		}
		for key, data := range keys {
			got := translator.T(key, data)
			if got == key {
				t.Errorf("%s: %q is missing from the catalog or its placeholders do not match", lang, key)
				continue
			}
			if strings.TrimSpace(got) == "" {
				t.Errorf("%s: %q rendered an empty message", lang, key)
			}
			if strings.HasSuffix(strings.TrimSpace(got), ".") {
				t.Errorf("%s: %q ends with a full stop: %q", lang, key, got)
			}
		}
	}
}

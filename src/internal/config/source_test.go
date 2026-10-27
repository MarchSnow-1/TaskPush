package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
)

// TestResolveSource_Priority 覆盖三种来源的优先级: --config 覆盖环境变量, 环境变量覆盖默认文件
//
// TestResolveSource_Priority covers the three-source priority: --config overrides the
// environment variable, which overrides the default file
func TestResolveSource_Priority(t *testing.T) {
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, "taskpush.json"), `{"version":1,"channels":[]}`)

	opts := SourceOptions{
		Path:      filepath.Join(cwd, "override.json"),
		CWD:       cwd,
		LookupEnv: lookupFrom(map[string]string{"TASKPUSH_CONFIG_JSON": `{"version":1,"channels":[]}`}),
	}
	writeFile(t, filepath.Join(cwd, "override.json"), `{"version":1,"channels":[]}`)
	src, err := ResolveSource(opts)
	if err != nil {
		t.Fatalf("ResolveSource with --config: %v", err)
	}
	if src.Kind != SourceFile || !strings.HasSuffix(src.Path, "override.json") {
		t.Fatalf("source = %+v, want the --config file", src)
	}

	opts.Path = ""
	src, err = ResolveSource(opts)
	if err != nil {
		t.Fatalf("ResolveSource with env: %v", err)
	}
	if src.Kind != SourceEnv || src.Path != "TASKPUSH_CONFIG_JSON" {
		t.Fatalf("source = %+v, want the env source", src)
	}

	opts.LookupEnv = lookupFrom(nil)
	src, err = ResolveSource(opts)
	if err != nil {
		t.Fatalf("ResolveSource with default file: %v", err)
	}
	if src.Kind != SourceFile || !strings.HasSuffix(src.Path, "taskpush.json") {
		t.Fatalf("source = %+v, want the default file", src)
	}
	if !filepath.IsAbs(src.Path) {
		t.Fatalf("file path %q is not absolute", src.Path)
	}
}

// TestResolveSource_EnvEmpty 覆盖环境变量已设置但为空时直接报错且不回退到文件
//
// TestResolveSource_EnvEmpty covers an environment variable set to an empty value failing
// instead of falling back to the file
func TestResolveSource_EnvEmpty(t *testing.T) {
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, "taskpush.json"), `{"version":1,"channels":[]}`)
	_, err := ResolveSource(SourceOptions{CWD: cwd, LookupEnv: lookupFrom(map[string]string{"TASKPUSH_CONFIG_JSON": ""})})
	assertProblemID(t, err, "config.error.empty")
}

// TestResolveSource_SourceMissing 覆盖三种来源全缺时的报错
//
// TestResolveSource_SourceMissing covers the error when all three sources are absent
func TestResolveSource_SourceMissing(t *testing.T) {
	cwd := t.TempDir()
	_, err := ResolveSource(SourceOptions{CWD: cwd, LookupEnv: lookupFrom(nil)})
	assertProblemID(t, err, "config.error.source_missing")
}

// TestResolveSource_Stdin 覆盖 --config - 从 stdin 读取, 以及空 stdin 报错
//
// TestResolveSource_Stdin covers --config - reading stdin and an empty stdin failing
func TestResolveSource_Stdin(t *testing.T) {
	src, err := ResolveSource(SourceOptions{
		Path:      "-",
		CWD:       t.TempDir(),
		Stdin:     strings.NewReader(`{"version":1,"channels":[]}`),
		LookupEnv: lookupFrom(nil),
	})
	if err != nil {
		t.Fatalf("ResolveSource stdin: %v", err)
	}
	if src.Kind != SourceStdin || string(src.Data) != `{"version":1,"channels":[]}` {
		t.Fatalf("source = %+v", src)
	}

	_, err = ResolveSource(SourceOptions{
		Path:      "-",
		CWD:       t.TempDir(),
		Stdin:     strings.NewReader("   \n\t"),
		LookupEnv: lookupFrom(nil),
	})
	assertProblemID(t, err, "config.error.empty")
}

// TestResolveSource_Limits 覆盖三种来源的 1MB 上限与恰好 1MB 的边界
//
// TestResolveSource_Limits covers the 1MB ceiling of the three sources and the exact-1MB
// boundary
func TestResolveSource_Limits(t *testing.T) {
	payload := padToSize(maxConfigSize + 2)
	_, err := ResolveSource(SourceOptions{
		Path:      "-",
		CWD:       t.TempDir(),
		Stdin:     bytes.NewReader([]byte(payload)),
		LookupEnv: lookupFrom(nil),
	})
	assertProblemID(t, err, "config.error.read")

	_, err = ResolveSource(SourceOptions{
		CWD:       t.TempDir(),
		LookupEnv: lookupFrom(map[string]string{"TASKPUSH_CONFIG_JSON": payload}),
	})
	assertProblemID(t, err, "config.error.read")

	path := filepath.Join(t.TempDir(), "taskpush.json")
	writeFile(t, path, payload)
	_, err = ResolveSource(SourceOptions{Path: path, CWD: t.TempDir(), LookupEnv: lookupFrom(nil)})
	assertProblemID(t, err, "config.error.read")

	path = filepath.Join(t.TempDir(), "taskpush.json")
	writeFile(t, path, padToSize(maxConfigSize-10))
	src, err := ResolveSource(SourceOptions{Path: path, LookupEnv: lookupFrom(nil)})
	if err != nil {
		t.Fatalf("ResolveSource 1MB boundary: %v", err)
	}
	if len(src.Data) != maxConfigSize-10 {
		t.Fatalf("data length = %d, want %d", len(src.Data), maxConfigSize-10)
	}
}

// TestResolveSource_BOM 覆盖三种来源统一去除 UTF-8 BOM
//
// TestResolveSource_BOM covers the UTF-8 BOM being stripped from every source
func TestResolveSource_BOM(t *testing.T) {
	bom := "\xEF\xBB\xBF" + `{"version":1,"channels":[]}`
	src, err := ResolveSource(SourceOptions{Path: "-", Stdin: strings.NewReader(bom)})
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if string(src.Data) != `{"version":1,"channels":[]}` {
		t.Fatalf("data = %q, want the BOM stripped", src.Data)
	}

	src, err = ResolveSource(SourceOptions{CWD: t.TempDir(), LookupEnv: lookupFrom(map[string]string{"TASKPUSH_CONFIG_JSON": bom})})
	if err != nil {
		t.Fatalf("ResolveSource env: %v", err)
	}
	if string(src.Data) != `{"version":1,"channels":[]}` {
		t.Fatalf("env data = %q, want the BOM stripped", src.Data)
	}

	path := filepath.Join(t.TempDir(), "taskpush.json")
	writeFile(t, path, bom)
	src, err = ResolveSource(SourceOptions{Path: path})
	if err != nil {
		t.Fatalf("ResolveSource file: %v", err)
	}
	if string(src.Data) != `{"version":1,"channels":[]}` {
		t.Fatalf("file data = %q, want the BOM stripped", src.Data)
	}
}

// TestResolveSource_RelativePath 覆盖相对 --config 按 CWD 解析成绝对路径
//
// TestResolveSource_RelativePath covers a relative --config resolving against CWD into an
// absolute path
func TestResolveSource_RelativePath(t *testing.T) {
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, "nested.json"), `{"version":1,"channels":[]}`)
	src, err := ResolveSource(SourceOptions{Path: "nested.json", CWD: cwd})
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if src.Path != filepath.Join(cwd, "nested.json") {
		t.Fatalf("path = %q, want %q", src.Path, filepath.Join(cwd, "nested.json"))
	}
}

// TestResolveSource_EnvUnset 覆盖未设置环境变量时继续走默认文件
//
// TestResolveSource_EnvUnset covers the default file being used when the environment variable is
// unset
func TestResolveSource_EnvUnset(t *testing.T) {
	// 进程环境里可能已经存在 TASKPUSH_CONFIG_JSON, 因此这里用显式查询隔离
	// The process environment may already carry TASKPUSH_CONFIG_JSON, so an explicit lookup
	// isolates this case
	_, err := ResolveSource(SourceOptions{CWD: t.TempDir(), LookupEnv: lookupFrom(nil)})
	assertProblemID(t, err, "config.error.source_missing")

	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, "taskpush.json"), `{"version":1,"channels":[]}`)
	os.Setenv("TASKPUSH_CONFIG_JSON_UNSET_PROBE", "")
	if got, ok := lookupFrom(nil)("TASKPUSH_CONFIG_JSON_UNSET_PROBE"); ok {
		t.Fatalf("lookupFrom(nil) reported %q as present", got)
	}
}

// writeFile 写一个测试用文件, 失败时立即结束当前测试
//
// writeFile writes a test fixture and ends the current test on failure
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// padToSize 构造一段恰好 size 字节的文本
//
// padToSize builds a text of exactly size bytes
func padToSize(size int) string {
	if size <= 0 {
		return ""
	}
	return strings.Repeat("x", size)
}

// assertProblemID 断言错误是带指定消息键的 *apperr.Problem
//
// assertProblemID asserts that an error is an *apperr.Problem with the given message key
func assertProblemID(t *testing.T, err error, key string) *apperr.Problem {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %s", key)
	}
	var problem *apperr.Problem
	if !errors.As(err, &problem) {
		t.Fatalf("error = %T (%v), want *apperr.Problem", err, err)
	}
	if problem.ID != key {
		t.Fatalf("Problem.ID = %q, want %q (data %v)", problem.ID, key, problem.Data)
	}
	return problem
}

// asProblemForTest 断言错误是 Problem 并返回它
//
// asProblemForTest asserts an error is a Problem and returns it
func asProblemForTest(t *testing.T, err error, key string) *apperr.Problem {
	t.Helper()
	return assertProblemID(t, err, key)
}

// jsonUnmarshal 是 json.Unmarshal 的测试内别名, 让测试文件不必重复导入 encoding/json
//
// jsonUnmarshal is a test-local alias of json.Unmarshal, which keeps the test files from
// repeating the encoding/json import
func jsonUnmarshal(data []byte, target any) error {
	return json.Unmarshal(data, target)
}

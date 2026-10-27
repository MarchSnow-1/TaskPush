package cli

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// golden 用例: 帮助, 表格与常见错误的输出默认落到 testdata/*.golden
// -update 只在本地更新这些文件, CI 只做比较
//
// Golden cases: the output of help, the tables and the common errors is stored in
// testdata/*.golden
// -update rewrites those files locally only, while CI compares them
// ---------------------------------------------------------------------------

// updateGolden 是本地更新开关, CI 从不传它
//
// updateGolden is the local update switch, which CI never passes
var updateGolden = flag.Bool("update", false, "rewrite the golden files under testdata")

// goldenDir 是一个可选的重定向目录, 设置后 golden 文件从该目录读取与写入, 不改动工作区
//
// goldenDir is an optional redirection directory: when set, golden files are read from and written
// into it, leaving the workspace untouched
var goldenDir = os.Getenv("CLI_GOLDEN_DIR")

// goldenPath 返回一个 golden 文件的完整路径
//
// goldenPath returns the full path of one golden file
func goldenPath(name string) string {
	if goldenDir != "" {
		return filepath.Join(goldenDir, name)
	}
	return filepath.Join("testdata", name)
}

// compareGolden 把实际输出与 golden 文件比较, -update 时改为写入
//
// compareGolden compares the actual output with a golden file and writes it instead under -update
func compareGolden(t *testing.T, name, actual string) {
	t.Helper()
	path := goldenPath(name)
	if *updateGolden {
		if err := os.WriteFile(path, []byte(actual), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run go test ./internal/cli -update to create it)", path, err)
	}
	if actual != string(want) {
		t.Fatalf("%s mismatch\n--- got ---\n%s\n--- want ---\n%s", path, actual, string(want))
	}
}

// TestGolden_GlobalHelp 覆盖全局帮助在两种语言下的输出
//
// TestGolden_GlobalHelp covers the global help in both languages
func TestGolden_GlobalHelp(t *testing.T) {
	cases := []struct {
		file string
		args []string
	}{
		{file: "help_global_en.golden", args: []string{"--help"}},
		{file: "help_global_zh.golden", args: []string{"--lang", "zh-CN", "--help"}},
		{file: "help_bare_en.golden", args: nil},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			result := runCLI(t, tc.args...)
			if result.code != exitOK {
				t.Fatalf("exit code = %d, want 0", result.code)
			}
			compareGolden(t, tc.file, result.stdout)
		})
	}
}

// TestGolden_SendHelp 覆盖 send 命令帮助在两种语言下的输出
//
// TestGolden_SendHelp covers the help of the send command in both languages
func TestGolden_SendHelp(t *testing.T) {
	cases := []struct {
		file string
		args []string
	}{
		{file: "help_send_en.golden", args: []string{"send", "--help"}},
		{file: "help_send_zh.golden", args: []string{"--lang", "zh", "help", "send"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			result := runCLI(t, tc.args...)
			if result.code != exitOK {
				t.Fatalf("exit code = %d, want 0", result.code)
			}
			compareGolden(t, tc.file, result.stdout)
		})
	}
}

// TestGolden_ListAndTypes 覆盖 list 与 types 表格在两种语言下的输出
//
// TestGolden_ListAndTypes covers the list and types tables in both languages
func TestGolden_ListAndTypes(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	path := writeFile(t, dir, "taskpush.json", exampleConfig(
		`{"id":"ops","type":"`+fakeType+`","settings":{}}`,
		`{"id":"ops2","type":"`+fakeHTTPType+`","enabled":false,"settings":{}}`,
	))

	cases := []struct {
		file string
		args []string
	}{
		{file: "list_en.golden", args: []string{"list", "--config", path}},
		{file: "list_zh.golden", args: []string{"--lang", "zh-CN", "list", "--config", path}},
		{file: "types_en.golden", args: []string{"types"}},
		{file: "types_zh.golden", args: []string{"--lang", "zh-CN", "types"}},
		{file: "types_fields_en.golden", args: []string{"types", fakeType}},
		{file: "types_fields_zh.golden", args: []string{"--lang", "zh-CN", "types", fakeType}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			result := runCLI(t, tc.args...)
			if result.code != exitOK {
				t.Fatalf("exit code = %d, want 0 (stderr %q)", result.code, result.stderr)
			}
			compareGolden(t, tc.file, result.stdout)
		})
	}
}

// TestGolden_Check 覆盖 check 表格在两种语言下的输出
//
// TestGolden_Check covers the check table in both languages
func TestGolden_Check(t *testing.T) {
	dir := t.TempDir()
	withWorkdir(t, dir)
	path := writeFile(t, dir, "taskpush.json", exampleConfig(
		`{"id":"ops","type":"`+fakeType+`","settings":{}}`,
		`{"id":"broken","type":"`+fakeType+`","settings":{"fail":true}}`,
	))

	cases := []struct {
		file string
		args []string
	}{
		{file: "check_en.golden", args: []string{"check", "--config", path}},
		{file: "check_zh.golden", args: []string{"--lang", "zh-CN", "check", "--config", path}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			result := runCLI(t, tc.args...)
			compareGolden(t, tc.file, result.stdout)
		})
	}
}

// TestGolden_Errors 覆盖常见错误的 stderr 输出
//
// TestGolden_Errors covers the stderr output of the common errors
func TestGolden_Errors(t *testing.T) {
	cases := []struct {
		file string
		args []string
	}{
		{file: "error_unknown_command.golden", args: []string{"example"}},
		{file: "error_unknown_flag.golden", args: []string{"send", "--bogus"}},
		{file: "error_invalid_lang.golden", args: []string{"--lang", "fr", "send"}},
		{file: "error_flag_not_for_command.golden", args: []string{"list", "--title", "example"}},
		{file: "error_value_dash.golden", args: []string{"send", "--title", "--example"}},
		{file: "error_parallel.golden", args: []string{"send", "--parallel", "9"}},
		{file: "error_unknown_flag_zh.golden", args: []string{"--lang", "zh-CN", "send", "--bogus"}},
		{file: "error_extra_arg_zh.golden", args: []string{"--lang", "zh-CN", "send", "example"}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			result := runCLI(t, tc.args...)
			if result.code != exitUsage {
				t.Fatalf("exit code = %d, want 2", result.code)
			}
			compareGolden(t, tc.file, result.stderr)
		})
	}
}

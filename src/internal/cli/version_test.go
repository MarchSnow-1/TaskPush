package cli

import (
	"strings"
	"testing"
)

// TestVersionFlagOutranksCommand 覆盖 --version 与命令同时出现: 只输出版本, 不执行发送
// 这能防止把误写的 `send --version` 变成一次真实推送
//
// TestVersionFlagOutranksCommand covers --version alongside a command: only the version is printed
// and nothing is sent, which keeps a mistyped `send --version` from becoming a real push
func TestVersionFlagOutranksCommand(t *testing.T) {
	withWorkdir(t, t.TempDir())
	result := runCLI(t, "send", "--version")
	if result.code != exitOK {
		t.Fatalf("send --version exit = %d, want %d (stderr %q)", result.code, exitOK, result.stderr)
	}
	if !strings.Contains(result.stdout, "test-version") {
		t.Fatalf("send --version output misses the version:\n%s", result.stdout)
	}
	if strings.Contains(result.stderr, prefixError) {
		t.Fatalf("send --version reached the send path, stderr:\n%s", result.stderr)
	}
}

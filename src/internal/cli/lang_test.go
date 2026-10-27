package cli

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// 语言用例: --lang en/zh-CN/zh/大小写/非法值, 以及非法语言时错误用英文输出
//
// Language cases: --lang en, zh-CN, zh, mixed case and an invalid value, with the invalid language
// reported in English
// ---------------------------------------------------------------------------

// TestRun_LangEnglishDefault 覆盖默认语言与显式 en 产生同样的输出
//
// TestRun_LangEnglishDefault covers the default language matching an explicit en
func TestRun_LangEnglishDefault(t *testing.T) {
	defaulted := runCLI(t, "version")
	explicit := runCLI(t, "--lang", "en", "version")
	if defaulted.stdout != explicit.stdout {
		t.Fatalf("default = %q, explicit en = %q", defaulted.stdout, explicit.stdout)
	}
	if defaulted.code != exitOK || explicit.code != exitOK {
		t.Fatalf("exit codes = %d and %d, want 0", defaulted.code, explicit.code)
	}
}

// TestRun_LangChinese 覆盖 zh-CN 与 zh 两种写法都切换到中文
//
// TestRun_LangChinese covers both zh-CN and zh switching to Chinese
func TestRun_LangChinese(t *testing.T) {
	for _, args := range [][]string{
		{"--lang", "zh-CN", "version"},
		{"--lang", "zh", "version"},
		{"--lang", "ZH-CN", "version"},
		{"version", "--lang", "zh"},
	} {
		result := runCLI(t, args...)
		if result.code != exitOK {
			t.Fatalf("runCLI(%v) = %d, want 0", args, result.code)
		}
		if !strings.Contains(result.stdout, "taskpush test-version") {
			t.Fatalf("runCLI(%v) stdout = %q, want the version line", args, result.stdout)
		}
	}
	// 用法错误按中文渲染, flag 名保持原样
	//
	// A usage error renders in Chinese while the flag name stays untranslated
	result := runCLI(t, "--lang", "zh-CN", "send", "--bogus")
	if result.code != exitUsage {
		t.Fatalf("exit code = %d, want 2", result.code)
	}
	if !strings.Contains(result.stderr, "未知") || !strings.Contains(result.stderr, "--bogus") {
		t.Fatalf("stderr = %q, want the Chinese unknown-flag copy", result.stderr)
	}
}

// TestRun_LangInvalid 覆盖非法语言用英文报错, 退出 2
//
// TestRun_LangInvalid covers an invalid language reported in English with exit 2
func TestRun_LangInvalid(t *testing.T) {
	result := runCLI(t, "--lang", "fr", "version")
	if result.code != exitUsage {
		t.Fatalf("exit code = %d, want 2", result.code)
	}
	if !strings.Contains(result.stderr, "fr") || !strings.Contains(result.stderr, "en, zh-CN") {
		t.Fatalf("stderr = %q, want the language and the available list", result.stderr)
	}
}

// TestRun_LangErrorBeatsOtherUsageErrors 覆盖语言错误优先于其它用法错误
//
// TestRun_LangErrorBeatsOtherUsageErrors covers the language error outranking another usage error
func TestRun_LangErrorBeatsOtherUsageErrors(t *testing.T) {
	result := runCLI(t, "--lang", "fr", "send", "--bogus")
	if result.code != exitUsage {
		t.Fatalf("exit code = %d, want 2", result.code)
	}
	if !strings.Contains(result.stderr, "unsupported language") {
		t.Fatalf("stderr = %q, want the language error in English", result.stderr)
	}
}

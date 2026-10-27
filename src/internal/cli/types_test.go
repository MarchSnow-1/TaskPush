package cli

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// types 命令的生产路径用例: 不替换任何接线点, 直接经 channel.FactoryByType 读取字段元数据
// 假工厂由 TestMain 通过 channel.Register 注册, 因此这条路径与二进制入口一致
//
// Production-path cases for the types command: no wiring point is replaced and the field metadata
// is read through channel.FactoryByType
// TestMain registers the fake factories through channel.Register, so this path matches the binary
// entry point
// ---------------------------------------------------------------------------

// TestTypesTypeReadsRegistryFactory 覆盖 types TYPE 从注册表取工厂并输出字段表
// 至少要看到字段名与退出码 0, 空表会让字段名断言失败
//
// TestTypesTypeReadsRegistryFactory covers types TYPE reading the factory from the registry and
// printing the field table
// The field names and a 0 exit code are the minimum, and an empty table fails the field assertion
func TestTypesTypeReadsRegistryFactory(t *testing.T) {
	result := runCLI(t, "types", fakeType)
	if result.code != exitOK {
		t.Fatalf("types %s exit = %d, want %d (stderr %q)", fakeType, result.code, exitOK, result.stderr)
	}
	for _, want := range []string{"FIELD", "url", "fail"} {
		if !strings.Contains(result.stdout, want) {
			t.Fatalf("types %s output misses %q:\n%s", fakeType, want, result.stdout)
		}
	}
}

// TestTypesTypeReadsRegistryFactoryInChinese 覆盖中文下同一条生产路径
//
// TestTypesTypeReadsRegistryFactoryInChinese covers the same production path in Chinese
func TestTypesTypeReadsRegistryFactoryInChinese(t *testing.T) {
	result := runCLI(t, "--lang", "zh-CN", "types", fakeHTTPType)
	if result.code != exitOK {
		t.Fatalf("types %s exit = %d, want %d (stderr %q)", fakeHTTPType, result.code, exitOK, result.stderr)
	}
	if !strings.Contains(result.stdout, "url") {
		t.Fatalf("types %s output misses the field name:\n%s", fakeHTTPType, result.stdout)
	}
	if strings.Contains(result.stdout, "channel.webhook") {
		t.Fatalf("types %s output leaked a message key instead of copy:\n%s", fakeHTTPType, result.stdout)
	}
}

// TestTypesAllReadsRegistryDescriptions 覆盖不带参数时类型描述来自注册表的工厂
//
// TestTypesAllReadsRegistryDescriptions covers the type description coming from the registered
// factory when no argument is given
func TestTypesAllReadsRegistryDescriptions(t *testing.T) {
	result := runCLI(t, "types")
	if result.code != exitOK {
		t.Fatalf("types exit = %d, want %d (stderr %q)", result.code, exitOK, result.stderr)
	}
	if !strings.Contains(result.stdout, fakeType) || !strings.Contains(result.stdout, "generic HTTP webhook") {
		t.Fatalf("types output misses the registered type or its description:\n%s", result.stdout)
	}
}

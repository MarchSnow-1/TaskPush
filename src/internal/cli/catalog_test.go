package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// catalog 一致性测试: cli.json 的两种语言键集合一致, 占位符一致, 且生产代码用到的键全部存在
// 这里直接读取两份 JSON 文件, 与 i18n 包的全量测试互补
//
// Catalog consistency tests: the two languages of cli.json hold the same key set and the same
// placeholders, and every key the production code uses is present
// The two JSON files are read directly, which complements the global test in the i18n package
// ---------------------------------------------------------------------------

// cliCatalogFile 是 CLI 的 catalog 文件名
//
// cliCatalogFile is the name of the CLI catalog file
const cliCatalogFile = "cli.json"

// cliCatalogLanguages 是参与对比的语言目录
//
// cliCatalogLanguages lists the language directories compared here
var cliCatalogLanguages = []string{"en", "zh-CN"}

// cliPlaceholderRegexp 匹配文案里的 {{.Name}} 占位符
//
// cliPlaceholderRegexp matches a {{.Name}} placeholder inside message text
var cliPlaceholderRegexp = regexp.MustCompile(`\{\{\s*\.([A-Za-z0-9_]+)\s*\}\}`)

// loadCLICatalog 读取一种语言的 cli.json
//
// loadCLICatalog reads cli.json of one language
func loadCLICatalog(t *testing.T, lang string) map[string]string {
	t.Helper()
	// cli 位于 src/internal/cli, 与 src/internal/i18n 同级, 因此向上只走一层
	//
	// cli lives in src/internal/cli, a sibling of src/internal/i18n, so one level up is enough
	path := filepath.FromSlash(fmt.Sprintf("../i18n/catalog/%s/%s", lang, cliCatalogFile))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var messages map[string]string
	if err := json.Unmarshal(data, &messages); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return messages
}

// cliPlaceholders 提取文案里的占位符名, 排序去重
//
// cliPlaceholders extracts the placeholder names of a message, sorted and deduplicated
func cliPlaceholders(text string) []string {
	matches := cliPlaceholderRegexp.FindAllStringSubmatch(text, -1)
	seen := map[string]bool{}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, m[1])
	}
	sort.Strings(out)
	return out
}

// TestCLICatalog_PlaceholderParity 覆盖两种语言的键集合与占位符集合一致
//
// TestCLICatalog_PlaceholderParity covers the key sets and placeholder sets matching between the
// two languages
func TestCLICatalog_PlaceholderParity(t *testing.T) {
	en := loadCLICatalog(t, "en")
	zh := loadCLICatalog(t, "zh-CN")
	if len(en) != len(zh) {
		t.Fatalf("key count differs: en %d, zh-CN %d", len(en), len(zh))
	}
	for key, enText := range en {
		zhText, ok := zh[key]
		if !ok {
			t.Errorf("zh-CN is missing key %q", key)
			continue
		}
		if got, want := cliPlaceholders(zhText), cliPlaceholders(enText); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("key %q: zh-CN placeholders %v, en placeholders %v", key, got, want)
		}
		if !strings.HasPrefix(key, "cli.") {
			t.Errorf("key %q does not carry the cli prefix", key)
		}
		if strings.Contains(enText, "%s") || strings.Contains(enText, "%d") {
			t.Errorf("key %q: message uses a printf verb: %q", key, enText)
		}
		if strings.HasSuffix(strings.TrimSpace(enText), ".") && !strings.Contains(enText, ". ") {
			t.Errorf("key %q: single-sentence copy ends with a period: %q", key, enText)
		}
	}
}

// TestCLICatalog_RegisteredKeys 覆盖命令表与 flag 表用到的每个键都在两种语言存在
//
// TestCLICatalog_RegisteredKeys covers every key the command and flag tables use being present in
// both languages
func TestCLICatalog_RegisteredKeys(t *testing.T) {
	catalogs := map[string]map[string]string{}
	for _, lang := range cliCatalogLanguages {
		catalogs[lang] = loadCLICatalog(t, lang)
	}

	var keys []string
	keys = append(keys,
		"cli.help.usage", "cli.help.commands", "cli.help.global_flags",
		"cli.help.config_mode", "cli.help.inline_mode", "cli.help.content_flags",
		"cli.help.notes", "cli.help.examples", "cli.help.more",
	)
	for _, spec := range commands {
		keys = append(keys, spec.UsageKey, "cli.desc.command."+spec.Name)
	}
	for _, spec := range flags {
		keys = append(keys, spec.UsageKey, "cli.desc.flag."+spec.Name)
	}
	for _, spec := range displayGroups {
		keys = append(keys, spec.Key)
	}
	keys = append(keys,
		"cli.error.unknown_command", "cli.error.unknown_flag", "cli.error.flag_not_for_command",
		"cli.error.missing_value", "cli.error.duplicate_flag", "cli.error.extra_arg",
		"cli.error.missing_command", "cli.error.inline_config_conflict",
		"cli.error.inline_channel_conflict", "cli.error.inline_all_conflict",
		"cli.error.payload_without_webhook", "cli.error.set_without_type",
		"cli.error.webhook_url_conflict", "cli.error.stdin_conflict", "cli.error.invalid_lang",
		"cli.error.invalid_parallel", "cli.error.invalid_var", "cli.error.invalid_var_reserved",
		"cli.error.invalid_set",
		"cli.error.invalid_payload", "cli.error.invalid_bool", "cli.error.value_dash",
		"cli.error.context_canceled", "cli.error.init_write",
		"cli.result.ok", "cli.result.ok_dry_run", "cli.result.fail", "cli.result.fail_not_sent",
		"cli.result.summary_ok", "cli.result.summary_fail",
		"cli.table.id", "cli.table.type", "cli.table.enabled", "cli.table.status",
		"cli.table.detail", "cli.table.field", "cli.table.required", "cli.table.default",
		"cli.table.description",
		"cli.info.using_config", "cli.info.start_written", "cli.info.start_dry_run",
		"cli.info.finish_deleted", "cli.info.finish_kept", "cli.info.check_no_channels",
		"cli.info.init_created", "cli.info.init_exists", "cli.warn.check_placeholder",
	)
	for _, key := range keys {
		for _, lang := range cliCatalogLanguages {
			if text := catalogs[lang][key]; text == "" {
				t.Errorf("%s is missing or empty in %s", key, lang)
			}
		}
	}
}

// TestCLICatalog_ProblemsRender 覆盖 CLI 构造的错误键在两种语言下都能渲染
//
// TestCLICatalog_ProblemsRender covers the error keys the CLI builds rendering in both languages
func TestCLICatalog_ProblemsRender(t *testing.T) {
	en := loadCLICatalog(t, "en")
	samples := map[string][]string{
		"cli.error.unknown_command":      {"Command", "Available"},
		"cli.error.unknown_flag":         {"Flag", "Command"},
		"cli.error.flag_not_for_command": {"Flag", "Command", "Available"},
		"cli.error.missing_value":        {"Flag"},
		"cli.error.duplicate_flag":       {"Flag"},
		"cli.error.extra_arg":            {"Arg", "Command"},
		"cli.error.stdin_conflict":       {"Flags"},
		"cli.error.invalid_lang":         {"Lang", "Available"},
		"cli.error.invalid_parallel":     {"Value"},
		"cli.error.invalid_var":          {"Value", "Reason"},
		"cli.error.invalid_var_reserved": {"Value", "Variable"},
		"cli.error.invalid_set":          {"Value", "Reason"},
		"cli.error.invalid_payload":      {"Detail"},
		"cli.error.invalid_bool":         {"Flag", "Value"},
		"cli.error.value_dash":           {"Flag", "Value"},
		"cli.error.init_write":           {"Path", "Detail"},
		"cli.result.ok":                  {"Attempts"},
		"cli.result.summary_ok":          {"Count"},
		"cli.result.summary_fail":        {"Count"},
		"cli.info.using_config":          {"Source"},
		"cli.info.start_written":         {"Path", "Time"},
		"cli.info.start_dry_run":         {"Path", "Time"},
		"cli.info.finish_deleted":        {"Path"},
		"cli.info.finish_kept":           {"Path"},
		"cli.info.init_created":          {"Path"},
		"cli.info.init_exists":           {"Path"},
		"cli.help.usage":                 {"Prog"},
	}
	for key, dataKeys := range samples {
		want := cliPlaceholders(en[key])
		sorted := append([]string(nil), dataKeys...)
		sort.Strings(sorted)
		if strings.Join(want, ",") != strings.Join(sorted, ",") {
			t.Errorf("%s: message placeholders %v, code Data keys %v", key, want, dataKeys)
		}
	}
}

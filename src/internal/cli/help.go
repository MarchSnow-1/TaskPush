package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/i18n"
)

// ---------------------------------------------------------------------------
// 帮助生成: 全局帮助与命令帮助都从命令表, flag 表与 i18n 键拼出
// 帮助里的示例只由命令名, flag 名与通用占位值组成, 因此不需要翻译文案
//
// Generating help: both the global help and the command help are assembled from the command table,
// the flag table and i18n keys
// An example in the help consists of command names, flag names and generic placeholder values only,
// which keeps it free of translatable prose
// ---------------------------------------------------------------------------

// progName 是帮助与示例里展示的程序名, 入口不传程序名, 因此这里固定使用二进制名
//
// progName is the program name shown in help and examples, fixed to the binary name since the entry
// point passes no program name
const progName = "taskpush"

// writeGlobalHelp 输出全局帮助: 用法, 命令列表, 全局 flag, 两种模式说明, 三个示例与文档链接
//
// writeGlobalHelp writes the global help: usage, the command list, the global flags, a note on both
// modes, three examples and the documentation links
func writeGlobalHelp(w io.Writer, t *i18n.Translator) {
	fmt.Fprintf(w, "%s\n\n", t.T("cli.help.usage", map[string]any{"Prog": progName}))

	fmt.Fprintf(w, "%s\n", t.T("cli.help.commands", nil))
	for _, spec := range commands {
		line := fmt.Sprintf("  %-9s %s", spec.Name, t.T("cli.desc.command."+spec.Name, nil))
		fmt.Fprintln(w, strings.TrimRight(line, " "))
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "%s\n", t.T("cli.help.global_flags", nil))
	for _, spec := range flags {
		if len(spec.Commands) > 0 {
			continue
		}
		writeFlagLine(w, t, spec)
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "%s\n", t.T("cli.help.config_mode", nil))
	fmt.Fprintf(w, "%s\n", t.T("cli.help.inline_mode", nil))
	fmt.Fprintf(w, "%s\n\n", t.T("cli.help.notes", nil))

	fmt.Fprintf(w, "%s\n", t.T("cli.help.examples", nil))
	for _, line := range globalExamples() {
		fmt.Fprintf(w, "  %s\n", line)
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "%s\n", t.T("cli.help.more", nil))
}

// writeCommandHelp 输出一个命令的帮助: 用法行, 按分组列出的 flag 与文档链接
// 命令的 flag 分组来自展示分组表, 未分组的 flag 追加在最后
//
// writeCommandHelp writes the help of one command: a usage line, the flags grouped as the display
// groups prescribe and the documentation links
// The display group table supplies the grouping, and an ungrouped flag is appended last
func writeCommandHelp(w io.Writer, t *i18n.Translator, spec commandSpec) {
	fmt.Fprintf(w, "%s\n\n", t.T("cli.help.usage", map[string]any{"Prog": progName}))
	fmt.Fprintf(w, "%s\n\n", t.T(spec.UsageKey, nil))

	index := flagIndex()
	printed := map[string]bool{}
	for _, group := range displayGroups {
		var lines []flagSpec
		for _, name := range group.Flags {
			flagSpec, ok := index[name]
			if !ok || !flagAppliesTo(flagSpec, spec.Name) {
				continue
			}
			lines = append(lines, flagSpec)
			printed[name] = true
		}
		if len(lines) == 0 {
			continue
		}
		fmt.Fprintf(w, "%s\n", t.T(group.Key, nil))
		for _, flagSpec := range lines {
			writeFlagLine(w, t, flagSpec)
		}
		fmt.Fprintln(w)
	}

	var rest []flagSpec
	for _, name := range miscFlags {
		flagSpec, ok := index[name]
		if !ok || !flagAppliesTo(flagSpec, spec.Name) {
			continue
		}
		rest = append(rest, flagSpec)
		printed[name] = true
	}
	if len(rest) > 0 {
		for _, flagSpec := range rest {
			writeFlagLine(w, t, flagSpec)
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintf(w, "%s\n", t.T("cli.help.more", nil))
}

// writeFlagLine 输出一行 flag: 名字与取值形状来自 flag 表, 说明来自 desc 键
//
// writeFlagLine writes one flag line: the name and its value shape come from the flag table and the
// description comes from a desc key
func writeFlagLine(w io.Writer, t *i18n.Translator, spec flagSpec) {
	left := t.T(spec.UsageKey, nil)
	right := t.T("cli.desc.flag."+spec.Name, nil)
	if right == "cli.desc.flag."+spec.Name {
		right = ""
	}
	line := fmt.Sprintf("  %-28s %s", left, right)
	fmt.Fprintln(w, strings.TrimRight(line, " "))
}

// globalExamples 返回全局帮助里的三个示例
// 示例由命令名, flag 名与通用占位值拼出, 因此没有需要翻译的叙述文字
//
// globalExamples returns the three examples of the global help
// Each example is assembled from command names, flag names and generic placeholder values, so it
// carries no prose that would need translating
func globalExamples() []string {
	return []string{
		progName + " check --config taskpush.json",
		progName + " send --config taskpush.json --title example",
		progName + " send --webhook https://example.com/hook --payload {}",
	}
}

// writeVersion 输出版本行, 版本为空时显示 dev
//
// writeVersion writes the version line, showing dev when the version is empty
func writeVersion(w io.Writer, t *i18n.Translator, version string) {
	if version == "" {
		version = "dev"
	}
	fmt.Fprintf(w, "%s %s\n", progName, version)
}

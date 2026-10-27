package cli

import (
	"strings"

	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/i18n"
)

// ---------------------------------------------------------------------------
// types 命令: 无参数时列出已注册类型与描述, 带 TYPE 参数时列出该类型的 settings 字段
// 类型名与字段名不翻译, 描述与默认值文案经 Translator 渲染
//
// The types command: without an argument it lists the registered types and their descriptions, and
// with a TYPE argument it lists the settings fields of that type
// Type and field names stay untranslated, while descriptions and defaults are rendered through the
// Translator
// ---------------------------------------------------------------------------

// runTypes 列出渠道类型或某个类型的字段
// 工厂查找经 factoryLookup 接线点, 找不到工厂时退回类型描述键的约定命名
//
// runTypes lists the channel types or the fields of one type
// The factory lookup goes through the factoryLookup wiring point, and a missing factory falls back
// to the conventional description key of a type
func runTypes(parsed *parsedArgs, p *printer) invocation {
	registered := channel.RegisteredTypes()
	if len(parsed.positionals) == 0 {
		tb := newTable(
			p.T("cli.table.type", nil),
			p.T("cli.table.description", nil),
		)
		for _, typ := range registered {
			tb.addRow(typ, typeDescription(p.translator, typ))
		}
		tb.render(p.stdout)
		return invocation{code: exitOK}
	}

	target := parsed.positionals[0]
	if !channel.IsRegistered(target) {
		return invocation{
			code: exitFailure,
			err: &apperr.Problem{
				ID: "channel.error.unknown_type",
				Data: map[string]any{
					"Type":      target,
					"Available": strings.Join(registered, ", "),
				},
			},
		}
	}
	factory, _ := factoryLookup(target)
	fields := []channel.Field(nil)
	if factory != nil {
		fields = factory.Fields()
	}

	tb := newTable(
		p.T("cli.table.field", nil),
		p.T("cli.table.type", nil),
		p.T("cli.table.required", nil),
		p.T("cli.table.default", nil),
		p.T("cli.table.description", nil),
	)
	for _, field := range fields {
		tb.addRow(
			field.Name,
			field.Type,
			requiredText(field.Required),
			field.Default,
			fieldDescription(p.translator, field),
		)
	}
	tb.render(p.stdout)
	return invocation{code: exitOK}
}

// requiredText 把必填标记渲染成 true 或 false, 两个值都不翻译
//
// requiredText renders the required marker as true or false, neither of which is translated
func requiredText(required bool) string {
	if required {
		return "true"
	}
	return "false"
}

// typeDescription 渲染一个渠道类型的描述
// 工厂可用时取 Factory.Describe 给出的键, 否则按 channel.<type>.desc 的命名约定取键
//
// typeDescription renders the description of one channel type
// A usable factory supplies the key through Factory.Describe, and otherwise the key follows the
// channel.<type>.desc naming convention
func typeDescription(t *i18n.Translator, typ string) string {
	key := "channel." + typ + ".desc"
	if factory, ok := factoryLookup(typ); ok && factory != nil && factory.Describe() != "" {
		key = factory.Describe()
	}
	return t.T(key, nil)
}

// fieldDescription 渲染一个 settings 字段的描述, 描述键为空时返回空串
//
// fieldDescription renders the description of one settings field and returns an empty string when
// the description key is empty
func fieldDescription(t *i18n.Translator, field channel.Field) string {
	if field.DescriptionKey == "" {
		return ""
	}
	return t.T(field.DescriptionKey, nil)
}

package cli

import (
	"github.com/MarchSnow-1/TaskPush/internal/apperr"
	"github.com/MarchSnow-1/TaskPush/internal/channel"
	"github.com/MarchSnow-1/TaskPush/internal/config"
)

// ---------------------------------------------------------------------------
// list 命令: 只加载配置并列出渠道, 不创建渠道也不发网络请求
// 表格三列分别是 ID, TYPE 与 ENABLED
//
// The list command: load the configuration and list the channels without creating one or touching
// the network
// The three columns are ID, TYPE and ENABLED
// ---------------------------------------------------------------------------

// runList 列出配置文件里的全部渠道
// 加载之后直接读 Root.Channels, 因此禁用渠道也会出现在表格里
//
// runList lists every channel of the configuration file
// Root.Channels is read straight after loading, so a disabled channel shows up in the table too
func runList(opts options, p *printer) invocation {
	root, err := loadRoot(opts.ConfigPath)
	if err != nil {
		return invocation{code: exitFailure, err: err}
	}
	tb := newTable(
		p.T("cli.table.id", nil),
		p.T("cli.table.type", nil),
		p.T("cli.table.enabled", nil),
	)
	for _, spec := range root.Channels {
		tb.addRow(spec.ID, spec.Type, enabledText(spec.IsEnabled()))
	}
	tb.render(p.stdout)
	return invocation{code: exitOK}
}

// enabledText 把启用状态渲染成 true 或 false, 两个值都不翻译
//
// enabledText renders the enabled state as true or false, neither of which is translated
func enabledText(enabled bool) string {
	if enabled {
		return "true"
	}
	return "false"
}

// loadRoot 解析配置来源并严格加载
// 列表与配置校验只读配置, 因此这里不做渠道选择
//
// loadRoot resolves the configuration source and loads it strictly
// Listing and configuration checking read the configuration only, so no channel is selected here
func loadRoot(configPath string) (*config.Root, error) {
	cwd, err := workingDir()
	if err != nil {
		return nil, &apperr.Problem{
			ID:    "config.error.source_missing",
			Data:  map[string]any{"Hint": sourceHint},
			Cause: err,
		}
	}
	source, err := config.ResolveSource(config.SourceOptions{
		Path:      configPath,
		CWD:       cwd,
		Stdin:     stdinReader,
		LookupEnv: lookupEnv,
	})
	if err != nil {
		return nil, err
	}
	return config.Load(source)
}

// factoryLookup 按 type 取已注册的工厂, 供 types 命令读取 Factory.Fields
// 默认实现直接读 channel 注册表; 测试可以替换它来隔离注册表状态
//
// factoryLookup returns the factory registered for a type, which the types command reads
// Factory.Fields through
// The default reads the channel registry directly, while a test may replace it to isolate the
// registry state
var factoryLookup = channel.FactoryByType

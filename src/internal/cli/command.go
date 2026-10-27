package cli

// ---------------------------------------------------------------------------
// 命令与 flag 表: 解析, 校验与帮助生成共用这一份数据, 避免出现第二份手写清单
// 表里只放命令名, flag 名与 i18n 键, 用户可见文案全部来自 catalog
//
// The command and flag tables: parsing, validation and help generation all read this one source,
// which keeps a second handwritten list from appearing
// The tables carry command names, flag names and i18n keys only, while every user-visible text
// comes from the catalog
// ---------------------------------------------------------------------------

// flagKind 区分取值形式: 单值, 布尔, 路径与可重复值
//
// flagKind tells the value forms apart: single value, boolean, path and repeatable value
type flagKind int

const (
	// flagString 是单值 flag, 重复出现属于用法错误
	//
	// flagString is a single-value flag, and a repeat is a usage error
	flagString flagKind = iota
	// flagBool 只接受 --flag 或 --flag=true/false 两种写法
	//
	// flagBool accepts --flag or --flag=true/false only
	flagBool
	// flagPath 是单值 flag, 显式空值属于用法错误
	//
	// flagPath is a single-value flag whose explicit empty value is a usage error
	flagPath
	// flagRepeatString 是可重复 flag, 每次出现追加一个值
	//
	// flagRepeatString is a repeatable flag, and every occurrence appends one value
	flagRepeatString
	// flagInline 是内联模式 flag, 显式空值属于用法错误
	// 空值的 --webhook= 若当成未给出, 命令会静默改用配置来源里的渠道发送, 属于安静的错误路由
	//
	// flagInline is an inline-mode flag whose explicit empty value is a usage error
	// Treating an empty --webhook= as absent would let the command silently send through the
	// channels of the configuration source, which routes the message to the wrong place
	flagInline
)

// flagSpec 描述一个 flag
// Commands 为空表示全局 flag; UsageKey 指向 cli.help.flag.<name>, 描述键由名字推出
//
// flagSpec describes one flag
// An empty Commands means a global flag, and UsageKey points at cli.help.flag.<name> while the
// description key is derived from the name
type flagSpec struct {
	Name     string
	Kind     flagKind
	Commands []string
	UsageKey string
}

// commandSpec 描述一个命令
// Positionals 是允许的位置参数上限, Flags 是该命令可用的 flag 名
//
// commandSpec describes one command
// Positionals is the upper bound of accepted positional arguments and Flags lists the flag names
// that command accepts
type commandSpec struct {
	Name        string
	UsageKey    string
	Flags       []string
	Positionals int
}

// options 是解析后的命令入参
// help/version/force 三个布尔 flag 不在该结构里, 由 parsedArgs 单独承载
//
// options holds the parsed command arguments
// The help, version and force booleans are absent from this struct and live on parsedArgs
type options struct {
	Command    string
	Lang       string
	ConfigPath string
	Title      string
	TitleFile  string
	Body       string
	BodyFile   string
	Vars       []string
	VarFile    string
	Channels   []string
	DryRun     bool
	Parallel   int
	StateFile  string
	KeepState  bool
	All        bool
	Webhook    string
	Type       string
	Sets       []string
	Payload    string
}

// commandNames 按帮助与错误里展示的顺序保存命令名
//
// commandNames holds the command names in the order help and errors display them
var commandNames = []string{
	"send", "finish", "start", "check", "list", "types", "init", "version", "help",
}

// sendFlagNames 是 send 与 finish 共用的 flag 集合, finish 另外追加计时 flag
//
// sendFlagNames is the flag set shared by send and finish, and finish appends the timing flags
var sendFlagNames = []string{
	"config", "channel", "title", "title-file", "body", "body-file",
	"var", "var-file", "dry-run", "parallel", "webhook", "type", "set", "payload",
}

// checkFlagNames 是 check 的 flag 集合
//
// checkFlagNames is the flag set of check
var checkFlagNames = []string{
	"config", "channel", "all", "var", "var-file", "webhook", "type", "set", "payload",
}

// commands 是命令表, 顺序即帮助里的展示顺序
//
// commands is the command table, and its order is the order help displays
var commands = []commandSpec{
	{Name: "send", UsageKey: "cli.help.command.send", Flags: sendFlagNames, Positionals: 0},
	{Name: "finish", UsageKey: "cli.help.command.finish", Flags: append(append([]string{}, sendFlagNames...), "state-file", "keep-state"), Positionals: 0},
	{Name: "start", UsageKey: "cli.help.command.start", Flags: []string{"config", "state-file", "dry-run"}, Positionals: 0},
	{Name: "check", UsageKey: "cli.help.command.check", Flags: checkFlagNames, Positionals: 0},
	{Name: "list", UsageKey: "cli.help.command.list", Flags: []string{"config"}, Positionals: 0},
	{Name: "types", UsageKey: "cli.help.command.types", Flags: nil, Positionals: 1},
	{Name: "init", UsageKey: "cli.help.command.init", Flags: []string{"force"}, Positionals: 0},
	{Name: "version", UsageKey: "cli.help.command.version", Flags: nil, Positionals: 0},
	{Name: "help", UsageKey: "cli.help.command.help", Flags: nil, Positionals: 1},
}

// flags 是全部 flag 的合并表, help/version/lang 三个全局 flag 排在前面
//
// flags is the merged table of every flag, with the global help, version and lang flags first
var flags = buildFlagTable()

// buildFlagTable 合并全局 flag 与命令 flag, 形成按名字查找的 map 以及展示用的切片
//
// buildFlagTable merges the global and command flags into a lookup map plus a display slice
func buildFlagTable() []flagSpec {
	global := []flagSpec{
		{Name: "help", Kind: flagBool, UsageKey: "cli.help.flag.help"},
		{Name: "version", Kind: flagBool, UsageKey: "cli.help.flag.version"},
		{Name: "lang", Kind: flagString, UsageKey: "cli.help.flag.lang"},
	}
	named := []flagSpec{
		{Name: "config", Kind: flagPath, Commands: []string{"send", "finish", "start", "check", "list"}, UsageKey: "cli.help.flag.config"},
		{Name: "channel", Kind: flagRepeatString, Commands: []string{"send", "finish", "check"}, UsageKey: "cli.help.flag.channel"},
		{Name: "title", Kind: flagString, Commands: []string{"send", "finish"}, UsageKey: "cli.help.flag.title"},
		{Name: "title-file", Kind: flagPath, Commands: []string{"send", "finish"}, UsageKey: "cli.help.flag.title-file"},
		{Name: "body", Kind: flagString, Commands: []string{"send", "finish"}, UsageKey: "cli.help.flag.body"},
		{Name: "body-file", Kind: flagPath, Commands: []string{"send", "finish"}, UsageKey: "cli.help.flag.body-file"},
		{Name: "var", Kind: flagRepeatString, Commands: []string{"send", "finish", "check"}, UsageKey: "cli.help.flag.var"},
		{Name: "var-file", Kind: flagPath, Commands: []string{"send", "finish", "check"}, UsageKey: "cli.help.flag.var-file"},
		{Name: "dry-run", Kind: flagBool, Commands: []string{"send", "finish", "start"}, UsageKey: "cli.help.flag.dry-run"},
		{Name: "parallel", Kind: flagString, Commands: []string{"send", "finish"}, UsageKey: "cli.help.flag.parallel"},
		{Name: "state-file", Kind: flagPath, Commands: []string{"start", "finish"}, UsageKey: "cli.help.flag.state-file"},
		{Name: "keep-state", Kind: flagBool, Commands: []string{"finish"}, UsageKey: "cli.help.flag.keep-state"},
		{Name: "all", Kind: flagBool, Commands: []string{"check"}, UsageKey: "cli.help.flag.all"},
		{Name: "webhook", Kind: flagInline, Commands: []string{"send", "finish", "check"}, UsageKey: "cli.help.flag.webhook"},
		{Name: "type", Kind: flagInline, Commands: []string{"send", "finish", "check"}, UsageKey: "cli.help.flag.type"},
		{Name: "set", Kind: flagRepeatString, Commands: []string{"send", "finish", "check"}, UsageKey: "cli.help.flag.set"},
		{Name: "payload", Kind: flagInline, Commands: []string{"send", "finish", "check"}, UsageKey: "cli.help.flag.payload"},
		{Name: "force", Kind: flagBool, Commands: []string{"init"}, UsageKey: "cli.help.flag.force"},
	}
	return append(global, named...)
}

// flagIndex 把 flag 表转成按名字查找的 map
//
// flagIndex turns the flag table into a map keyed by flag name
func flagIndex() map[string]flagSpec {
	index := make(map[string]flagSpec, len(flags))
	for _, spec := range flags {
		index[spec.Name] = spec
	}
	return index
}

// commandIndex 把命令表转成按名字查找的 map
//
// commandIndex turns the command table into a map keyed by command name
func commandIndex() map[string]commandSpec {
	index := make(map[string]commandSpec, len(commands))
	for _, spec := range commands {
		index[spec.Name] = spec
	}
	return index
}

// flagAppliesTo 报告 flag 是否适用于该命令
// 全局 flag 对所有命令可用, 命令 flag 要求命令名出现在 Commands 里
//
// flagAppliesTo reports whether a flag applies to a command
// A global flag works with every command, while a command flag needs the command name in its
// Commands list
func flagAppliesTo(spec flagSpec, command string) bool {
	if len(spec.Commands) == 0 {
		return true
	}
	for _, name := range spec.Commands {
		if name == command {
			return true
		}
	}
	return false
}

// flagNames 返回该命令可用 flag 的名字列表, 供错误里的 Available 占位符使用
//
// flagNames returns the names of the flags a command accepts, for the Available placeholder of an
// error message
func flagNames(command string) []string {
	var names []string
	for _, spec := range flags {
		if flagAppliesTo(spec, command) {
			names = append(names, "--"+spec.Name)
		}
	}
	return names
}

// displayGroups 是命令帮助里的 flag 分组, 只含命令自己的 flag
//
// displayGroups holds the flag groups of command help, covering command flags only
var displayGroups = []struct {
	Key   string
	Flags []string
}{
	{Key: "cli.help.config_mode", Flags: []string{"config", "channel", "all", "dry-run", "parallel", "state-file", "keep-state"}},
	{Key: "cli.help.inline_mode", Flags: []string{"webhook", "type", "set", "payload"}},
	{Key: "cli.help.content_flags", Flags: []string{"title", "title-file", "body", "body-file", "var", "var-file"}},
}

// miscFlags 是未归入三个展示分组的命令 flag
//
// miscFlags holds the command flags that belong to none of the three display groups
var miscFlags = []string{"force"}

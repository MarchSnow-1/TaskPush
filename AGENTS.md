# AGENTS.md

本文件为人类开发者/Coding Agent 所写，用于规范化本项目开发过程中的一些细节

Written for human developers and coding agents, to lay out the details of how this project is developed.

## 0. 先看什么 / Read first

1. 项目内全部 `*.md` 文档 - 项目介绍与完整使用指南
2. 要改哪个模块，先读那个文件开头的注释

本项目每个源文件开头均说明了其编写原因，请遵守第二条：不要凭猜测推断代码意图，也不要进行破坏性修改

1. Read every `*.md` document in the project — the introduction and the full usage guide
2. Before changing a module, read the header comment of that file

Every source file explains at its top why it is written that way.
Follow rule 2 above: do not infer intent by guesswork, and do not make destructive changes.

## 1. 这是什么 / What this is

TaskPush 是一个纯 Go 编写的轻量多渠道消息推送中心, 用于在 CI、备份、脚本任务等场景下把一条通知推到多个渠道

TaskPush is a lightweight multi-channel message push hub written in pure Go, used to push one notification to several channels from CI, backup jobs, scripted tasks and similar scenarios

## 2. 项目结构 / Project structure

代码全部在 `src/` 下, `src/` 是 Go 模块根 (`go.mod` 在其中), 导入路径不带 `src` 段

```
src/go.mod                  模块定义 (零第三方依赖)
src/cmd/taskpush            入口: 只做 cli.Run 分发与信号处理, 版本经 -ldflags 注入 main.version
src/internal/apperr         结构化错误 Problem 与永久性标记 (叶子包)
src/internal/i18n           消息目录加载与渲染; catalog/{en,zh-CN}/ 按区域分文件, en/zh-CN 键与占位符必须一致
src/internal/settings         配置值类型: Duration, Int, Bool, StringList (叶子包)
src/internal/template         模板引擎: {{var}}, {{if}}, {{range}}, 点路径, 严格与宽松两模式 (只依赖 apperr)
src/internal/channel          渠道契约: Channel, Prepared, Factory, 注册表与严格解码
src/internal/channel/common   渠道共用逻辑: URL 规范化与打码, 状态码判定, 错误脱敏, 截断, header 校验
src/internal/channel/all      装配包: 九个渠道各一行空白导入, 只触发 init() 注册
src/internal/channel/<type>   九个渠道各一包: bark, dingtalk, discord, gotify, ntfy, serverchan, telegram, webhook, wecom
src/internal/config           配置来源解析, ${VAR} 环境变量展开, 严格校验与禁用渠道延迟报告
src/internal/notify           发送调度: 变量合并, 标题正文渲染, 并发与重试, 生效默认值, HTTP client 构造
src/internal/state            start/finish 计时状态文件的读, 写, 删
src/internal/cli              命令与 flag 表, 解析, 分发, i18n 渲染, 表格与退出码
src/internal/e2e              进程内验收用例, 只有测试文件, 经 cli.Run 驱动
```

**跑 `go` 类命令前先切到 `src/`**; `gofmt` 在仓库根执行, 因为 `git ls-files` 给出的是仓库根相对路径

依赖方向单向, 不允许反向引用或环 (下面是内部依赖, 左侧依赖右侧):

```
apperr, i18n, settings     ->  (叶子包, 只依赖标准库)
template                   ->  apperr
channel                    ->  apperr, template
channel/common             ->  apperr, channel, template
channel/<type>             ->  apperr, channel, channel/common, template; 其中 6 个 (bark, dingtalk, gotify, ntfy, serverchan, telegram) 另依赖 settings
channel/all                ->  各 channel/<type> (仅空白导入)
config                     ->  apperr, settings, channel
notify                     ->  apperr, channel, channel/common, config, template
state                      ->  apperr
cli                        ->  apperr, channel, channel/common, config, i18n, notify, state
cmd/taskpush               ->  cli, channel/all
e2e (仅测试)               ->  cli, channel/all
```

反向依赖是被禁止的: `template`, `i18n`, `apperr`, `settings` 不引用任何上层包; `channel` 与 `config` 互不引用, 靠 `settings` 与 `channel` 的契约解耦; `notify` 不导入 `cli`, 渠道包不导入 `config`, `notify`, `cli`, `i18n`, `state`, 也不导入 `net/http` 之外的输出设施

渠道之间零耦合: 新增渠道只需在 `src/internal/channel/` 下新建一个包, 实现 `channel.Channel` 与 `channel.Factory` 并在 `init()` 里注册, 再到 `src/internal/channel/all/all.go` 追加一行空白导入, 最后在 `src/internal/i18n/catalog/{en,zh-CN}/` 补一份 `channel_<type>.json`; 不改动 `notify` 与 `cli`

All code lives under `src/`, which is the Go module root (`go.mod` sits inside it); import paths carry no `src` segment

```
src/go.mod                  module definition (zero third-party dependencies)
src/cmd/taskpush            entry point: only cli.Run dispatch and signal handling; version injected into main.version via -ldflags
src/internal/apperr         structured error Problem and the permanent marker (leaf package)
src/internal/i18n           message catalog loading and rendering; catalog/{en,zh-CN}/ split per area, keys and placeholders must match between en and zh-CN
src/internal/settings       config value types: Duration, Int, Bool, StringList (leaf package)
src/internal/template       template engine: {{var}}, {{if}}, {{range}}, dot paths, strict and lenient modes (depends only on apperr)
src/internal/channel        channel contract: Channel, Prepared, Factory, registry and strict decoding
src/internal/channel/common shared channel logic: URL normalisation and masking, status-code judgement, error redaction, truncation, header validation
src/internal/channel/all    assembly package: one blank import per channel, only triggering init() registration
src/internal/channel/<type> one package per channel: bark, dingtalk, discord, gotify, ntfy, serverchan, telegram, webhook, wecom
src/internal/config         config source resolution, ${VAR} environment expansion, strict validation and deferred reporting of disabled channels
src/internal/notify         send scheduling: variable merging, title/body rendering, concurrency and retries, effective defaults, HTTP client construction
src/internal/state          read, write and delete of the start/finish timing state file
src/internal/cli            command and flag tables, parsing, dispatch, i18n rendering, tables and exit codes
src/internal/e2e            in-process acceptance cases, test files only, driven through cli.Run
```

**Change into `src/` before running any `go` command**; `gofmt` runs at the repository root because `git ls-files` yields repository-root-relative paths

Dependencies flow one way; reverse references and cycles are not allowed (below, the left side depends on the right):

```
apperr, i18n, settings     ->  (leaf packages, standard library only)
template                   ->  apperr
channel                    ->  apperr, template
channel/common             ->  apperr, channel, template
channel/<type>             ->  apperr, channel, channel/common, template; 6 of them (bark, dingtalk, gotify, ntfy, serverchan, telegram) also depend on settings
channel/all                ->  each channel/<type> (blank imports only)
config                     ->  apperr, settings, channel
notify                     ->  apperr, channel, channel/common, config, template
state                      ->  apperr
cli                        ->  apperr, channel, channel/common, config, i18n, notify, state
cmd/taskpush               ->  cli, channel/all
e2e (tests only)           ->  cli, channel/all
```

Reverse dependencies are forbidden: `template`, `i18n`, `apperr` and `settings` reference no upper layer; `channel` and `config` do not reference each other and are decoupled through the `settings` and `channel` contracts; `notify` does not import `cli`, and channel packages do not import `config`, `notify`, `cli`, `i18n` or `state`, nor any output facility beyond `net/http`

Channels are fully decoupled from one another: adding a channel only means creating a package under `src/internal/channel/`, implementing `channel.Channel` and `channel.Factory` and registering in `init()`, appending one blank import to `src/internal/channel/all/all.go`, and adding a `channel_<type>.json` under `src/internal/i18n/catalog/{en,zh-CN}/`; `notify` and `cli` stay untouched

## 3. 安全 / Security

### 3.1 错误文本经过脱敏再输出

错误信息会进 CI 日志, 打印前经脱敏把其中的地址和凭据打码, 日志因此可以公开分享

错误文本经过 `common.SanitizeString` 处理: 其中的 URL 替换为 `<redacted-url>`, 渠道已知的凭据替换为 `***`

`SanitizeError` 只改写 `Error()` 的文本, `Unwrap()` 保留原始错误, 因此 `errors.Is` / `errors.As` 仍可穿透; 打印时用 `.Error()`

Error messages land in CI logs, so they pass through redaction before printing and the log stays safe to share

`common.SanitizeString` rewrites the text: a URL becomes `<redacted-url>` and a credential the channel knows becomes `***`

`SanitizeError` rewrites only `Error()` while `Unwrap()` keeps the original error, so `errors.Is` / `errors.As` still reach through; print with `.Error()`

### 3.2 缺省补全走 https

`common.NormalizeURL` 对缺 scheme 的地址补 `https://`, 回环地址（`localhost` / `127.0.0.0/8` / `::1`）补 `http://` 便于本地调试

`common.ValidateHTTPURL` 与 `NormalizeURL` 规则相同, 额外把字段名写进错误定位; scheme 只接受 http/https 且 host 非空

这条规则作用在缺省补全上: 地址里显式写了 `http://` 的非回环主机按明文处理, 少写双斜杠的 `http:example.com` 同样按明文处理

`common.NormalizeURL` fills in `https://` for an address missing a scheme, and `http://` for a loopback address (`localhost` / `127.0.0.0/8` / `::1`) to ease local debugging

`common.ValidateHTTPURL` follows the same rules as `NormalizeURL` and additionally names the field in the error; the scheme is http or https and the host is non-empty

The rule covers the defaulting path: an explicit `http://` on a non-loopback host goes out as plain text, and `http:example.com` with the double slash missing is treated the same way

### 3.3 依赖策略：优先标准库，不随意引入

本项目引入第三方依赖前须确认：

- 它消除的是**容易出错**的代码，还是仅减少了几行?
- 它会引入多少传递依赖，体积与供应链风险是否可接受?
- 项目是否仍在维护，许可证是否允许当前用法?

自行实现容易在边界条件上出错，版本比较、字符编码、时间处理尤其如此；但引入依赖同样有供应链成本，两者都要论证

Before adding a third-party dependency, confirm:

- Does it eliminate **error-prone** code, or merely save a few lines?
- How many transitive dependencies does it pull in, and are the size and supply-chain risk acceptable?
- Is it still maintained, and does its licence permit the intended use?

Hand-rolled implementations tend to get edge cases wrong — version comparison, character encoding and time handling especially — but dependencies carry supply-chain cost too, so both sides need justification

## 4. 工作方式 / Workflow

- **先说明再修改** —— 阐明目标与影响范围；涉及配置格式、命令行参数、发布产物或重试语义时，先给出方案
- **小步提交** —— 每个批次须保持可编译，一批只做一件事，并明确修改范围，便于后续回溯问题来源
- **未经允许不推送** —— 提交可以；推送分支、打 tag 与发布 Release 均由维护者决定
- 只格式化**本次修改的文件**，不进行全仓库格式化，不改动行尾
- 注释和文案统一使用**中英双语**，完成一个任务后需排查本次变更是否存在遗漏注释的地方，如有遗漏需补全
- 注释须写成**连续的整段中文 + 连续的整段英文**，不得中英逐行交替
- **临时脚本一律放 `.dev/`** —— 此目录供测试使用，已被 .gitignore 忽略，本机使用的测试脚本、临时工具统一放置在此处
- **禁止妄下定论** —— 注释、文案与说明中不得使用「永久」「永远」「绝不」「一定会」这类无限定范围的断言，也不得把当前观察到的情况说成普遍规律。
  改为写清边界：在什么条件下、哪个函数走哪条分支、因此不会出现什么结果

- **Explain before changing** — state the goal and the blast radius; for anything touching config format, CLI arguments, release artefacts or retry semantics, present the plan first
- **Commit in small steps** — every batch must stay compilable, do one thing per batch, and make the scope of the change explicit so problems can be traced back later
- **Do not push without permission** — committing is fine; pushing branches, tagging and publishing releases are the maintainer's call
- Format **only the files changed in this batch**, never the whole repository, and do not touch line endings
- Comments and copy are **bilingual (Chinese and English)**; after finishing a task, check whether this change left any comment gaps and fill them in
- Comments must be written as **a continuous block of Chinese followed by a continuous block of English**, never alternating line by line
- **Temporary scripts always go in `.dev/`** — that directory is for testing and is ignored by .gitignore; test scripts and throwaway tools used on this machine belong there
- **Do not state absolutes** — comments, copy and documentation must not use unbounded assertions such as "permanent", "never", "always" or "guaranteed", nor present a current observation as a general rule.
  State the boundary instead: under which conditions, which function takes which branch, and therefore what cannot happen

## 5. 提交信息 / Commit messages

提交信息**用英文写**，遵循[约定式提交](https://www.conventionalcommits.org/)：
`<type>(<scope>): <description>`，**只写一行**，简洁有力地说明这次改了什么

英语是通用语言，使用英语作为提交信息可以覆盖更多人群，便于后期其他外部开发者做出贡献

不写多行小作文
把写小作文的精力放到**完善注释**上去
提交信息只需要让人知道"这一步做了什么"，而"为什么这么做"应当写在代码注释里，那里才是下一个读代码的人会看的地方

常用 type：`feat` `fix` `refactor` `perf` `docs` `test` `build` `ci` `chore` `style`

```
feat(update): use the Semver library for version comparison
docs: document the .dev/ convention in AGENTS.md
```

Commit messages are **written in English** and follow
[Conventional Commits](https://www.conventionalcommits.org/):
`<type>(<scope>): <description>`, **on a single line**, stating concisely what changed.

English is the lingua franca.
So writing commit messages in English reaches a wider audience and makes it easier for other external developers to contribute later.

No multi-paragraph essays.
Spend that effort on **improving the comments** instead.
The commit message only needs to say what this step did, while the reasoning belongs in a code comment, which is where the
next person to read the code will actually look.

Common types: `feat`, `fix`, `refactor`, `perf`, `docs`, `test`, `build`, `ci`, `chore`, `style`

## 6. 测试 / Testing

测试全部是标准库 `testing` 的单元测试，与被测代码同包同目录（`*_test.go`）

网络相关测试一律起本地 `httptest` 服务器，不连公网
`example.com` 与 `proxy.example` 这类地址只用于 URL 解析与配置断言；`127.0.0.1:1` 是故意指向关闭端口的负向用例

每批改动至少执行：

```bash
gofmt -l $(git ls-files '*.go')   # 在仓库根执行；只检查，输出非空即失败，不跑 gofmt -w
cd src
go vet ./...
go test -count=1 ./...
```

`.github/workflows/dev.yml` 与 `release.yml` 在构建前跑格式检查、`go vet`、`go test` 与 `go test -race`，全部通过才构建产物

修复缺陷须附带回归测试，安全与重试相关的问题尤其如此，错误分类、脱敏、URL 校验都属于容易静默失效的部分

测试用例的输入与断言应覆盖边界条件：空值、超长内容（截断上限）、未解析变量、非法状态码、并发与超时

All tests use the standard library `testing` package and live in the same package and directory as the code under test (`*_test.go`)

Network-related tests always spin up a local `httptest` server and never reach the public internet
Addresses such as `example.com` and `proxy.example` are only used for URL parsing and config assertions; `127.0.0.1:1` is a deliberate negative case pointing at a closed port

Every batch runs at least:

```bash
gofmt -l $(git ls-files '*.go')   # run at the repository root; check only, non-empty output is a failure, do not run gofmt -w
cd src
go vet ./...
go test -count=1 ./...
```

`.github/workflows/dev.yml` and `release.yml` run the format check, `go vet`, `go test` and `go test -race` before building, and build artifacts only after all of them pass

Fixes come with a regression test, especially for security and retry-related issues: error classification, redaction and URL validation are the parts that fail silently, so a test is what keeps them working

Test inputs and assertions should cover edge cases: empty values, oversized content (truncation limits), unresolved variables, invalid status codes, concurrency and timeouts

## 7. 测试数据与示例值 / Test data and fixtures

**代码与测试数据应该使用规范、清晰易懂的通用名称进行命名**

- 示例值一律用 `example` / `Example` / `EXAMPLE` / `examples` 这类**通用占位**，
  例如用户名用 `example`，域名用 `example.com`，仓库用 `username/repository`
- **不得**使用真实域名、仓库名、用户名、设备序列号或使用开发机器上的绝对路径来编写代码/测试用例/注释，也不得使用**由它们截取或变体而来的名字**

**Code and test data must be named with standard, clear, generic terms**

- Fixtures use generic placeholders such as `example` / `Example` / `EXAMPLE` / `examples` —
  for instance `example` for a user name, `example.com` for a domain and `username/repository`
  for a repository
- Do **not** write a real domain, repository name, user name or device serial into code, test cases or
  comments, and do not use an absolute path from a development machine — nor a name **derived or abbreviated** from any of them

## 8. 针对Agent的协作方式建议

- 分批修改 - 对用户下定的任务进行拆分，分多次进行小幅度的修改，适当多用 commit，避免一次性提交大量代码
- 可维护性 - 保证每次 commit 的代码完全可构建并具有清晰易懂的 commit 消息，便于后期 review 与分析问题
- 保持透明 — 每批工作完成后报告：已完成事项、验证结果、遗留风险
- 存在不确定时先行确认，禁止猜测
- 全部代码注释与文档需保持中英双语，若变更代码内容需同步更新对应注释
- 尽量使用简洁易懂的语言来编写注释，降低维护成本
- 尽量使用逗号作为分割，不要在注释行尾或句中添加中文句号，除非一行有两个句子需要区分
- 如需多行注释，保证一句话在一行注释内写完，严禁一句话分多行来写
- 修改面向用户的报错文案、命令行帮助或文档时 **必须** 同步更新 `README.md` 与 `docs/usage.md` 中的对应描述，并实机验证输出
- 严禁修改 AGENTS.md 文件

- Batch your changes — split the user's task into several small edits, commit often, and avoid landing a large amount of code in one go
- Maintainability — keep every commit fully buildable with a clear commit message, so later review and debugging are easy
- Stay transparent — after each batch, report what was done, what was verified and what risks remain
- When something is uncertain, confirm first; never guess
- All code comments and documentation stay bilingual; when code changes, update the matching comments
- Write comments in plain, easy-to-read language to keep maintenance cheap
- For multi-line comments, keep each sentence on one line; never split a sentence across lines
- When changing user-facing error copy, CLI help or documentation, you **must** update the matching text in `README.md` and `docs/usage.md`, and verify the output on a real run
- Never modify the AGENTS.md file
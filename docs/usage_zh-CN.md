# TaskPush 使用指南

[**English**](usage.md) | [**简体中文**](usage_zh-CN.md)

由浅入深地理解 TaskPush 的工作逻辑与扩展用法

## 1. 起步

### 1.1 发送第一条通知

不写任何文件, 直接把地址给 `--webhook`:

```bash
taskpush send --webhook https://example.com/hook --title "构建完成" --body "commit abc123"
```

成功时打印一行 `[OK]`, 退出码 0:

```text
[OK] inline (webhook): sent (attempts: 1)
```

直接使用 `--webhook` 参数会构造一个运行时渠道, 对于且仅对于使用此参数且未指定 `body` 内容时, 会默认使用 `POST` 来发送一段纯文本, 内容为 `标题 + 空行 + 正文`

若使用 `--type` 的通用方法指定渠道并发送, 则和在配置文件中创建渠道的行为一致 (若未指定内容则 `body` 默认为空, 不会像 `--webhook` 快捷入口一样补一份 `标题 + 空行 + 正文` 的默认格式)

### 1.2 模拟发送，测试配置

加 `--dry-run` 参数运行, 即可模拟一次发送流程, 但只渲染和校验你的配置, 不会发出真实请求:

```bash
taskpush send --webhook https://example.com/hook --title "构建完成" --body "commit abc123" --dry-run
```

```text
[OK] inline (webhook): POST https://example.com/hook (text, 14 bytes)
[INFO] dry run, nothing was sent
```

预览里的地址和凭据会被打码, 字节数反映实际正文长度

## 2. 配置文件

不写配置也可以发送单条通知, 但要多渠道、公共参数、渠道私有字段时就需要编写配置文件了

使用 `init` 命令会在当前目录生成一份最小配置:

```bash
taskpush init
```

```json
{
  "version": 1,
  "timeout": "15s",
  "retries": 0,
  "retryDelay": "1s",
  "proxy": "",
  "channels": []
}
```

已存在时不会覆盖 (提示 `already exists`), 要覆盖生成, 需要加 `--force` 参数

`channels` 为空时 `send` 命令没有可发送的渠道, 需要在里面填至少一个:

```json
{
  "version": 1,
  "timeout": "15s",
  "retries": 0,
  "retryDelay": "1s",
  "proxy": "",
  "channels": [
    { "id": "ops", "type": "webhook", "settings": { "url": "https://example.com/hook" } }
  ]
}
```

配置文件名固定为 `taskpush.json`, 位于工作目录下的配置文件会被自动读取, 也可以显式指定路径:

```bash
taskpush send --config path/to/taskpush.json --title "构建完成"
```

这里填写的路径同时支持相对路径与绝对路径, 相对路径基于当前工作目录解析

程序默认按以下顺序查找配置, 命中即停止:

1. `--config PATH` 指定的文件, 或使用 `--config -` 从标准输入读
2. 环境变量 `TASKPUSH_CONFIG_JSON` 的内容
3. 当前目录的 `taskpush.json`

`--config` 与 `TASKPUSH_CONFIG_JSON` 同时存在时, `--config` 优先且不报错, 三者内容上限都是 1MB

配置由标准库 JSON 解析, 因此不支持注释与尾逗号

字段写错会有明确报错并指出路径, 写完建议先跑 `taskpush check` 做一次校验

## 3. 配置校验

在编写完成配置文件后, 推荐对配置进行校验

使用 `check` 命令即可让程序尝试解析配置、校验字段和渲染模板:

```bash
taskpush check
```

```text
ID   TYPE     STATUS  DETAIL
ops  webhook  [OK]    POST https://example.com/hook (none, 0 bytes)
phone bark    [OK]    bark: 1 device
```

同样地, 你也可以指定配置文件:

```bash
taskpush check --config path/to/taskpush.json
```

如有任何错误, 会退出 1

只有警告则退出 0

与使用 `send` 命令 + `--dry-run` 参数的区别是: 这里的 `check` 命令只是校验配置的合法性

默认行为下, 在配置中设置为未启用的渠道不会被校验

但若为 `check` 命令携带了 `--all` 参数, 则会校验所有已填写的渠道 (即便配置了未启用字段)

## 4. 查看渠道字段

使用 `types` 命令可列出所有渠道类型

```bash
taskpush types
```

使用 `types <TYPE>` 可查看某类的具体字段:

```bash
taskpush types webhook
```

```text
FIELD             TYPE        REQUIRED  DEFAULT  DESCRIPTION
url               string      true               target URL, supporting templates
method            string      false     POST     HTTP method, uppercase, default POST
...
```

每个渠道各有一个 `settings` 对象, 可以配置此渠道的详细字段:

```json
{ "id": "tg", "type": "telegram", "settings": { "token": "example:token", "chatIds": ["111111"] } }
```

渠道与必填字段参考表:

请查看 [types.md](types_zh-CN.md)

## 5. 变量

### 5.1 模板变量

标题和正文均支持模板变量替换, 可以使用 `{{name}}` 来调用

内置变量有 `{{time}}`、`{{date}}`、`{{datetime}}`， 以及特殊的 `{{title}}` 及 `{{body}}`:

```bash
taskpush send --webhook https://example.com/hook --title "{{date}} 构建完成" --body "完成时间 {{datetime}}"
```

此处, 我们为 title 传入了 `{{date}} 构建完成`, 这个值会被先渲染一遍, 渲染结果大致为: `2026-01-01 构建完成`

随后, 渲染完成的 title 值及 body 值会被设置为一个新的模板变量, 并支持在配置文件中使用 {{title}} {{body}} 进行引用

如: 

```json
{
  "version": 1,
  "channels": [
    {
      "id": "ops",
      "type": "webhook",
      "settings": {
        "url": "https://example.com/hook",
        "method": "POST",
        "headers": {
          "Content-Type": "application/json",
          "X-Title": "{{title}}"
        },
        "body": {
          "format": "json",
          "json": {
            "title": "{{title}}",
            "text": "{{body}}",
            "sent_at": "{{datetime}}"
          }
        }
      }
    }
  ]
}
```

### 5.2 自定义模板变量

自己传变量, 可以用 `--var` 参数, 此参数可重复添加:

```bash
taskpush send --webhook https://example.com/hook \
  --title "{{project}} 构建完成" --body "分支 {{branch}}" \
  --var project=TaskPush --var branch=main
```

变量很多不便加入传入参数时，也可写进 JSON 文件并使用 `--var-file` 参数来调用:

```json
{ "varfile_project": "TaskPush", "varfile_branch": "main", "varfile_commit": "abc123" }
```

```bash
taskpush send --webhook https://example.com/hook --title "{{varfile_project}} 构建完成" --body "{{varfile_commit}}" --var-file vars.json
```

优先级是 `--var` 外部指定 > `--var-file` 文件读取 > 内置变量

你甚至可以实现在推送前修改 json varfile 内 var 的值, 实现动态调用

### 5.3 环境变量

TaskPush 不仅有自定义的模板变量, 也支持使用环境变量

你可以在任意位置使用 `${VAR}` 来引用一个名为 `VAR` 的环境变量的值:

先创建一个 `taskpush.json` 配置文件

```json
{
  "version": 1,
  "channels": [
    { "id": "ops", "type": "webhook", "settings": { "url": "${HOOK_URL}" } }
  ]
}
```

> [!WARNING]
> 注意: 如使用传参形式则需要使用单引号传入, 以避免环境变量被 shell 提前解析

随后调用

```bash
export HOOK_URL=https://example.com/hook
export REPO=TaskPush
taskpush send --title '${REPO} 构建完成'
```

同上, 此处我们为 title 传入了 `${REPO} 构建完成`, 这个值会被先渲染一遍, 渲染结果大致为: `TaskPush 构建完成`

同样地, 渲染完成的 title 值及 body 值也会被设置为一个新的模板变量, 并支持在配置文件中使用 {{title}} {{body}} 进行引用

按上方举例, `TaskPush 构建完成` 就会被发送到环境变量 `HOOK_URL` 中存储的地址

变量未设置且没有默认值时, 加载会直接报错:

```text
[ERROR] environment variable HOOK_URL is not set (source file:..., path channels[1].settings.url)
```

要允许缺省时可以写 `${VAR:-https://example.com/hook}`

另外, 填写反斜杠转义的 `$${` 可以输出字面 `${`

## 6. 一次发送多个渠道

多个渠道可以同时写进配置文件, `send` 命令默认会发给其中所有启用的渠道:

```json
{
  "version": 1,
  "channels": [
    { "id": "ops", "type": "webhook", "settings": { "url": "https://example.com/hook" } },
    { "id": "phone", "type": "bark", "settings": { "token": "exampleToken" } }
  ]
}
```

注: 对于在配置文件里配置的 `webhook` 类型渠道, 正文需在 `settings.body` 中定义, 未定义时正文为空

```bash
taskpush send --title "构建完成" --body "commit abc123"
```

每个渠道一行结果, 默认从上到下按顺序执行, 全成功则退出 0

任一失败就会退出 1, 但其余渠道仍然照常发送

只想给其中某个或某几个渠道发送可用 `--channel` 参数，此参数可重复添加以同时发送多个渠道

```bash
taskpush send --channel ops --channel phone --title "发送给 ops 与 phone 渠道的通知"
```

```bash
taskpush send --channel ops --title "只发送给 ops 渠道的通知"
```

## 7. webhook 请求体

使用 `--webhook` 参数时, webhook 请求体默认会以纯文本形式发送

但在使用 `--type` 手动传参指定配置或在配置文件中编写 webhook 类型的渠道时, webhook 请求体默认为空

若要修改方法、增加请求头、更换正文格式, 可在 `settings` 内调整:

```json
{
  "id": "ops",
  "type": "webhook",
  "settings": {
    "url": "https://example.com/hook",
    "method": "PUT",
    "headers": { "X-Token": "${HOOK_TOKEN}" },
    "body": {
      "format": "json",
      "json": { "title": "{{title}}", "text": "{{body}}", "at": "{{datetime}}" }
    }
  }
}
```

`body.format` 可以选择 `text`、`json`、`form` 其中的一种作为发送格式

其中, `json` 内部的字符串叶子也支持模板变量及环境变量, 数字与布尔叶子会保持 JSON 原类型

```
对以上 "保持 JSON 原类型" 的解释:

假设某个叶子的值恰好为且仅为 {{变量}}，并且这个变量是布尔值, 此时 TaskPush 会保留 JSON 类型输出

值为 bool 的 true 就输出 true, 而不是字符串 "true"
```

当然, 你也可以使用内联 flag 直接编写:

```bash
taskpush send --type webhook \
  --set url=https://example.com/hook \
  --set headers.X-Token=exampleToken \
  --set body.format=json \
  --set body.json.msg='{{body}}' \
  --title "构建完成" --body "abc123"
```

`--set` 会用点路径临时写入 `settings`, 因此 `body.json.msg` 会落到 `body` 对象内 `json` 对象的 `msg` 键

如果你觉得过于麻烦, 也可以使用 `--payload` 并指定 json 内容来自定义发送载荷

例如: `--payload '{"msg":"x"}'` 即为 `--set body.format=json` 加整个 `body.json` 的简写

## 8. 重试与送达

### 失败重试

默认情况下, 程序在失败时不会进行重试

即: 一次推送只发一遍, 失败即报错并退出 1

如果你希望程序启用失败重试, 可以在配置里写 `retries` 来手动启用失败重试

启用后, 程序会在首次发送失败后额外尝试重发最大 `retries`次, 因此最坏情况下程序的总尝试次数为 `retries + 1`

```json
{
  "version": 1,
  "timeout": "15s",
  "retries": 2,
  "retryDelay": "1s",
  "channels": [ ... ]
}
```

`retryDelay` 是两次尝试之间的固定间隔

另外, `timeout`、`retries`、`retryDelay` 都能写在单个渠道里覆盖顶层配置

若渠道内未填写, 程序会向上查找顶层数值, 若顶层仍未填写则采用默认数值

优先级顺序为: 渠道配置>顶层配置>默认值

重试只作用于可能自愈的错误：

| 会重试 | 立即停止 |
| --- | --- |
| 网络错误（连接被拒、超时、DNS 失败） | 3xx（不跟随重定向，按最终响应处理） |
| 408 / 425 / 429 | 其余 4xx（400、401、403、404…） |
| 常规 5xx（500、502、503…） | 501 / 505 / 511 |
| 响应体为空或不是合法 JSON | 平台业务码判定为确定性失败 |

列表中右列情况出现时, 即便配置中的 `retries` 数值大于 1 也只尝试发送一次，不会进行后续重试

### 送达保障

一条通知在实际环境中可能让对方收到重复消息, 前提是启用了自动重试

假设请求其实已经到了, 但回包丢失

由于回包丢失, TaskPush 无法确认请求是否重新到达

此时若配置了自动重试, 那么重试便成为第二次送达

因此, 若下游会触发动作, 则需要自己保证幂等 (也可以直接关闭重试, 但会丢失可靠性)

## 9. 计时

使用 `start` 命令可以记录当前时间戳 (会覆写本地文件), 任务执行完成后使用 `finish` 命令即可计算耗时并发送:

```bash
taskpush start
# ... 执行需要计时的任务 ...
taskpush finish --title "任务完成" --body "耗时 {{elapsed}}"
```

`finish` 额外提供了 `{{elapsed}}`、`{{elapsed_ms}}`、`{{elapsed_s}}` 这些模板变量

计时的原理为向本地写入携带时间戳的状态文件, 在完成后读取并计算耗时

因此, 发送成功 (体现为0值退出, 即无任何渠道发送失败) 时会自动删除临时的状态文件, 在有渠道失败时会保留

携带 `--keep-state` 参数可以在成功发送的情况下也保留状态文件

状态文件默认是运行时所使用的配置文件同目录 (若来源不是文件, 则默认使用工作路径) 下的 `.taskpush-state.json`, 也可以用 `--state-file` 指定

## 10. 退出码与输出

| 退出码 | 含义 |
| --- | --- |
| 0 | 成功, `check` 只有警告也算成功 |
| 1 | 配置、环境变量、渠道、发送或状态错误 |
| 2 | 命令行用法错误 |
| 130 | Ctrl+C(SIGINT)或 SIGTERM 取消 |

## 11. 语言

```bash
taskpush --lang zh-CN list
```

`--lang` 参数目前支持 `en`(默认) 或 `zh-CN`, 写在命令前后均可

语言的设置会影响日志输出内容、帮助等信息, 不会影响通知内容及配置语义

## 12. 安全

- 建议在配置文件中始终使用 `${VAR}` 引用密钥, 此方法让密钥由运行环境注入, 可以避免泄露密钥内容, 变量缺失时会进行报错
- 预览与错误里的 URL / token 会进行打码, 但分享日志前也请检查是否存在不宜公开的内容
- 程序不跟随 HTTP 重定向, 鉴权头不会随着跳转外泄

## 13. CI 内的使用样例

### 13.1 最简写法

最简单的写法, 即为只用环境变量, 不落文件:

```yaml
- run: taskpush send --webhook '${HOOK_URL}' --title "${{ github.ref_name }} 构建完成" --body "$GITHUB_SHA"
  env:
    HOOK_URL: ${{ secrets.HOOK_URL }}
```

此时, Github Actions 读取配置的 secrets.HOOK_URL 作为环境变量 HOOK_URL 的值, 并由 TaskPush 解析并处理

> [!WARNING]
> 注意: 如使用传参形式则需要使用单引号传入, 以避免环境变量被 shell 提前解析

### 13.2 多渠道控制

要发多个渠道, 需要编写配置文件并放到工作目录或指定路径, 密钥仍然可以继续走环境变量:

```yaml
- run: taskpush send --title "构建完成" --body "$GITHUB_SHA"
  env:
    HOOK_URL: ${{ secrets.HOOK_URL }}
    HOOK_TOKEN: ${{ secrets.HOOK_TOKEN }}
```

配置不想进仓库时, 可以将 Json 放进 `TASKPUSH_CONFIG_JSON` 这个环境变量, 程序会自行调用

```yaml
- run: taskpush send --title "构建完成" --body "$GITHUB_SHA"
  env:
    HOOK_URL: ${{ secrets.HOOK_URL }}
    HOOK_TOKEN: ${{ secrets.HOOK_TOKEN }}
    TASKPUSH_CONFIG_JSON: ${{ secrets.TASKPUSH_CONFIG_JSON }}
```

配置文件还可以像以下这种方式, 从 stdin 灌进去:

```bash
echo "$CONFIG_JSON" | taskpush send --config - --title "构建完成"
```

```yaml
- run: echo "$CONFIG_JSON" | taskpush send --config - --title "构建完成" --body "$GITHUB_SHA"
  env:
    HOOK_URL: ${{ secrets.HOOK_URL }}
    HOOK_TOKEN: ${{ secrets.HOOK_TOKEN }}
    CONFIG_JSON: ${{ secrets.TASKPUSH_CONFIG_JSON }}
```

## 14. 常见问题

| 现象 | 原因与做法 |
| --- | --- |
| 提示找不到配置来源 | 没有内联 flag, 也没有 `--config` / `TASKPUSH_CONFIG_JSON` / 当前目录 `taskpush.json` |
| `${VAR}` 报环境变量未设置 | 环境变量不存在此值, 检查变量名, 在允许为空的情况下需改写为 `${VAR:-默认值}` 格式 |
| `check --all` 报禁用渠道里的变量未设置 | `--all` 会连禁用渠道一起校验; 补齐对应变量, 或去掉 `--all` |
| 未知字段报错 | 配置严格解码; 删掉多余键, 或改成正确字段名 |
| `headers` 报类型错误 | 只接受对象映射, 例如 `{"X-Token":"${HOOK_TOKEN}"}` |
| webhook 的字符串 `body` 被当纯文本 | 字符串形态固定是 `text/plain`; 希望使用 JSON 请写 `{"format":"json","json":{...}}` |
| 模板变量没被替换 | `send` 下未解析变量直接报错; 检查 `--var` / `--var-file` 的键名 |
| 模板里想输出字面 `{{` | 用 `\{{`, 例如 `--body '\{{not_a_var}}'` |
| 同一条通知收到两次 | 见[第 8 节](#8-重试与送达) |
| 配置里写了注释就解析失败 | 配置由标准库 JSON 解析, 不支持注释与尾逗号 |
| 连不上本地明文服务 | 缺 scheme 时非回环主机均补 `https://`, 如需 HTTP 请显式写 `http://` |

## 15. 完整命令与 flag

```text
taskpush [global flags] <command> [command flags] [positional args]
```

第一个位置参数是命令; flag 写在命令前后都可以; `--` 之后的 token 全部按位置参数处理

| 命令 | 作用 | 发 HTTP | 读写状态 |
| --- | --- | --- | --- |
| `send` | 发送一条通知 | 是(携带 `--dry-run` 参数则为否) | 否 |
| `start` | 记录计时起点 | 否 | 写 |
| `finish` | 读起点、算耗时并发送 | 是(`--dry-run` 否) | 读; 成功则删除状态文件(`--keep-state` 可强制保留) |
| `check` | 校验配置或内联渠道 | 否 | 否 |
| `list` | 列出配置里的渠道 | 否 | 否 |
| `types` | 列出类型; `types <TYPE>` 看字段 | 否 | 否 |
| `init` | 生成最小 `taskpush.json` | 否 | 否 |
| `version` | 显示版本 | 否 | 否 |
| `help` | 帮助; `help CMD` 看命令 | 否 | 否 |

全局 flag:

| flag | 取值 | 默认 | 说明 |
| --- | --- | --- | --- |
| `--help` / `-h` | 无 | — | 显示帮助; 放在命令后显示该命令帮助 |
| `--version` | 无 | — | 显示版本 |
| `--lang` | `en` / `zh-CN` | `en` | 输出语言 |

命令与 flag:

| 命令 | flag |
| --- | --- |
| `send` | `--config` `--channel` `--title` `--title-file` `--body` `--body-file` `--var` `--var-file` `--dry-run` `--parallel` `--webhook` `--type` `--set` `--payload` |
| `finish` | 在 `send` 的基础上额外加入了 `--state-file` `--keep-state` |
| `start` | `--config` `--state-file` `--dry-run` |
| `check` | `--config` `--channel` `--all` `--var` `--var-file` `--webhook` `--type` `--set` `--payload` |
| `list` | `--config` |
| `types` | 位置参数 `<TYPE>`(可选) |
| `init` | `--force` |
| `version` | 无 |
| `help` | 位置参数 `COMMAND`(可选) |

flag 语义:

| flag | 语义 |
| --- | --- |
| `--config PATH` | 配置路径; 缺省按 `TASKPUSH_CONFIG_JSON` → 当前目录 `taskpush.json` |
| `--channel ID` | 可重复; 只发送或校验指定渠道 |
| `--title TEXT` | 标题模板; 与 `--title-file` 互斥 |
| `--title-file PATH` | 从文件读标题; `-` 读 stdin; 与 `--title` 互斥 |
| `--body TEXT` | 正文模板; 与 `--body-file` 互斥 |
| `--body-file PATH` | 从文件读正文; `-` 读 stdin; 与 `--body` 互斥 |
| `--var KEY=VALUE` | 可重复; 添加一个模板变量; 会强制覆盖 `--var-file` 的同名键; 添加保留名会报错 |
| `--var-file PATH` | JSON 对象文件; `-` 读 stdin |
| `--dry-run` | 完整渲染、校验并输出预览 |
| `--parallel N` | 渠道之间的并发数; 默认 1; 范围 1-8 |
| `--state-file PATH` | 覆盖状态文件路径 |
| `--keep-state` | `finish` 成功后保留状态文件 |
| `--all` | `check` 连禁用渠道一起校验 |
| `--webhook URL` | 构造内联 webhook 渠道 |
| `--type TYPE` | 构造指定类型的内联渠道; 需 `--set` 提供必填字段 |
| `--set KEY=VALUE` | 可重复; 写入内联渠道的 `settings`; 支持嵌套路径 |
| `--payload JSON` | 仅内联 webhook; 设置 `body.format=json` 与 `body.json` |
| `--force` | `init` 覆盖已存在的 `taskpush.json` |

解析规则:

- 单值 flag 重复是错误; `--var`、`--channel`、`--set` 可重复
- 布尔 flag 只支持 `--dry-run` 与 `--dry-run=true/false/1/0`; 不支持 `--dry-run false`
- 以 `--` 开头的值必须写成 `--flag=--value`, 避免被误解析为 flag
- 一次调用最多一个 stdin 消费者: `--config -`、`--title-file -`、`--body-file -`、`--var-file -` 只能出现一个
- `--title` 与 `--title-file` 互斥, `--body` 与 `--body-file` 互斥
- `types` 与 `help` 最多一个位置参数, 其它命令不接受位置参数

## 16. 配置字段表

顶层:

| 字段 | 类型 | 默认 | 约束 |
| --- | --- | --- | --- |
| `version` | int | `1` | 只支持 1 |
| `$schema` | string | 空 | 运行时忽略; 允许出现在顶层 |
| `timeout` | duration | `15s` | 必须大于 0 |
| `retries` | int | `0` | 大于等于 0; 首次之外的重试次数 |
| `retryDelay` | duration | `1s` | 大于等于 0; 固定重试间隔 |
| `proxy` | string | 空 | 空 = 直连; `env` = 环境代理; http/https URL = 显式代理; 不支持 socks5 |
| `channels` | array | 必填 | 每个渠道一个对象 |

单个渠道:

| 字段 | 类型 | 默认 | 约束 |
| --- | --- | --- | --- |
| `id` | string | 必填 | 非空且唯一; 用 `--channel` 指定 |
| `type` | string | 必填 | `types` 列出的类型之一 |
| `enabled` | bool | `true` | 只有显式 `false` 才禁用 |
| `timeout` | duration | 继承顶层 | 大于 0 |
| `retries` | int | 继承顶层 | 大于等于 0 |
| `retryDelay` | duration | 继承顶层 | 大于等于 0 |
| `settings` | object | 启用渠道必填 | 渠道私有字段, 由渠道工厂严格解码 |

配置由标准库 JSON 解析, 不接受注释与尾逗号; 未知字段报错, 报错带 JSON 路径, 例如 `channels[2].settings`

## 17. 模板语法

| 写法 | 说明 |
| --- | --- |
| `{{name}}` | 变量; 支持点路径 `{{a.b.0}}` |
| `{{name\|default:文本}}` | 变量缺失或为假时用它 |
| `{{if x}}A{{else}}B{{end}}` | 条件; `x` 是变量名或点路径 |
| `{{range items}}...{{end}}` | 遍历数组; 体内 `{{.}}` 是当前项, `{{@index}}` 从 0 起 |
| `\{{` | 输出字面 `{{` |

严格模式与宽松模式:

| 模式 | 使用命令 | 未解析变量 |
| --- | --- | --- |
| 严格 | `send` / `finish` / `--dry-run` | 报错并退出 1 |
| 宽松 | `check` | 保留字面片段并告警, 退出 0(其它都有效时) |

`check` 用的是占位内容, 因此宽松模式作用在 `settings` 里的模板字段上; `--title` / `--body` 只在 `send` / `finish` 下渲染, 那里始终是严格模式

内置变量: `{{title}}`、`{{body}}`、`{{time}}`、`{{date}}`、`{{datetime}}`; `finish` 另有 `{{elapsed}}`、`{{elapsed_ms}}`、`{{elapsed_s}}`, 这些是保留名, 不能通过 `--var` 覆盖

渲染后的内容受 64KB 输出上限约束: 严格模式超限报错, 宽松模式截断并告警

## 18. 开发与扩展

代码均在 `src/` 目录下, 导入路径不带 `src` 段

| 目录 | 内容 |
| --- | --- |
| `src/internal/apperr` | 结构化错误与永久性标记 |
| `src/internal/i18n` | 语言目录与 Translator |
| `src/internal/settings` | 配置值类型 |
| `src/internal/template` | 模板引擎与变量模型 |
| `src/internal/channel/` | 渠道契约、注册表、公共助手与各渠道包 |
| `src/internal/state/` | 计时状态文件 |
| `src/internal/config` | 配置来源、`${VAR}` 展开、校验 |
| `src/internal/notify` | 发送调度、并发与重试 |
| `src/internal/cli` | 命令与 flag 表、解析、渲染、退出码 |

依赖方向单向: `channel/<type> → channel/common → channel → template`, `notify → config/channel/template`, `cli → config/notify/state/channel/i18n`

### 18.1 新增一个渠道

1. 新建 `src/internal/channel/<type>/<type>.go`, 实现 `channel.Channel` 与 `channel.Factory`, 在 `init()` 里调用 `channel.Register`
2. 在 `src/internal/channel/all/all.go` 追加一行空白导入
3. 在 `src/internal/i18n/catalog/en/` 与 `catalog/zh-CN/` 各补一份 `channel_<type>.json`, 两边的键与占位符必须一致
4. 不改动 `notify` 与 `cli`

`docs/taskpush.example.json` 是九个渠道各一份的样例, `docs/taskpush.schema.json` 供编辑器补全与校验
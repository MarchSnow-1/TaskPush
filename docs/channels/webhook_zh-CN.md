# webhook

[**English**](webhook.md) | [**简体中文**](webhook_zh-CN.md)

使用可配置的 HTTP 请求承载消息, 不预设平台语义

## 标题与正文

`--title` 与 `--body` 在这条渠道里**不会自动进入请求**, 它们只作为模板变量 `{{title}}` 与 `{{body}}` 供 `settings` 引用

也就是说, 配置文件里不写 `body` 时请求正文为空, 标题与正文都不会出现; 想让它们进正文, 必须自己引用:

```json
{
  "id": "ops",
  "type": "webhook",
  "settings": {
    "url": "https://example.com/hook",
    "body": { "format": "text", "text": "{{title}}\n\n{{body}}" }
  }
}
```

例外是内联的 `--webhook URL`: 该入口不写任何 body 相关参数时, 程序自动补上 `{{title}}\n\n{{body}}` 这份默认正文, 因此标题与正文会自动带上

## 最小配置

```json
{
  "id": "ops",
  "type": "webhook",
  "settings": { "url": "https://example.com/hook" }
}
```

这条配置发出的请求不含正文, 正文需要显式定义, 详见下方「正文形态」

## 字段

| 字段 | 类型 | 必填 | 默认 | 说明 |
| --- | --- | --- | --- | --- |
| `url` | string | 是 | — | 目标地址, 支持模板 |
| `method` | string | 否 | `POST` | HTTP 方法, 大写 |
| `headers` | object | 否 | — | 请求头, 名与值都支持模板 |
| `body` | object 或 string | 否 | — | 请求正文, 两种形态见下 |
| `body.format` | string | 否 | — | `json` / `text` / `form` |
| `body.json` | object | 否 | — | JSON 正文, 字符串叶子支持模板 |
| `body.text` | string | 否 | — | 纯文本正文模板 |
| `body.fields` | object | 否 | — | 表单字段, 名值对 |
| `body.contentType` | string | 否 | — | 显式 Content-Type, 覆盖格式默认值 |
| `maskSecrets` | stringlist | 否 | — | 预览中要隐藏的地址片段, 每项必须是完整 URL 的子串 |

## 正文形态

`body` 有两种写法, 各自规则不同

**字符串形态**固定按 `text/plain` 发送, 内容就是该字符串:

```json
{ "body": "构建完成\n\ncommit abc123" }
```

**对象形态**由 `body.format` 派发, 三选一, `format` 与对应字段必须同时给出, 缺一个就报错, 不会回退到其他形态

```json
{ "body": { "format": "json", "json": { "title": "{{title}}", "text": "{{body}}" } } }
```

| format | 必配字段 |
| --- | --- |
| `text` | `body.text` |
| `json` | `body.json` |
| `form` | `body.fields` |

完全不写 `body` 时正文为空

## JSON 正文的类型保真

`body.json` 的字符串叶子支持模板变量与环境变量, 当某个叶子恰好只写一个 `{{变量}}` 时, 该变量按 JSON 原类型输出:

| 配置 | 变量 | 发出 |
| --- | --- | --- |
| `"n": "值={{n}}"` | `n=5` | `"值=5"` |
| `"n": "{{n}}"` | `n=5` | `5` |
| `"f": "{{flag}}"` | `flag=true` | `true` |
| `"x": "{{nothing}}"` | `null` | `null` |

混入文字后一律按字符串输出

## 调用方法

### 内联写法

```bash
taskpush send --type webhook \
  --set url=https://example.com/hook \
  --set headers.X-Token=exampleToken \
  --set body.format=json \
  --set body.json.msg='{{body}}' \
  --title "构建完成" --body "abc123"
```

```bash
taskpush send --type webhook \
  --set url='${URLInEnv}' \
  --set headers.X-Token='${TokenInEnv}' \
  --set body.format=json \
  --set body.json.msg='{{body}}' \
  --title "构建完成" --body "abc123"
```

`--set` 用点路径写入 `settings`, 因此 `body.json.msg` 落到 `body` 对象的 `json` 对象的 `msg` 键

`--payload '{"msg":"x"}'` 是 `--set body.format=json` 加整个 `body.json` 的简写

### 配置文件写法

在 `taskpush.json` 的 `channels` 里加一条, 字段写进 `settings`:

```json
{
  "version": 1,
  "channels": [
    {
      "id": "ops",
      "type": "webhook",
      "settings": { "url": "${URLInEnv}", "headers": { "X-Token": "${TokenInEnv}" } }
    }
  ]
}
```

```bash
export URLInEnv=https://example.com/hook
export TokenInEnv=exampleToken
taskpush send --title "构建完成" --body "commit abc123"
```

地址与请求头值是两个独立的变量, 各用各的名字

配置里用 `${VAR}` 引用密钥, 由运行环境注入, 文件本身可以安全入库, 变量未设置且没有默认值时加载即报错, 写成 `${URLInEnv:-默认值}` 可允许缺省

注意 `${VAR}` 要生效, 文件里必须写字面量 `${URLInEnv}`, 不能提前被编辑器的变量替换或 CI 的模板插值改掉

## maskSecrets

预览会打码地址里的凭据段, 但自定义地址的凭据位置它不一定认得, `maskSecrets` 让你直接指名要隐藏的片段:

```json
{
  "settings": {
    "url": "https://example.com/hook/abcdef1234567890",
    "maskSecrets": ["abcdef1234567890"]
  }
}
```

每项必须是完整 URL 的子串, 否则告警, 部分失配时, 未匹配到的项对应的内容仍会按凭据规则打码

## 注意

- 这条渠道不定义成功判据之外的业务语义: 2xx 即成功, 其余按状态码分类
- 与 `--webhook` 快捷入口的区别: 用 `--webhook URL` 且不写 body 时, 默认发送 `标题 + 空行 + 正文`; 配置文件里的 webhook 不写 body 时正文为空

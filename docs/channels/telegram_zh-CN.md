# telegram

[**English**](telegram.md) | [**简体中文**](telegram_zh-CN.md)

通过 Telegram Bot API 的 `sendMessage` 推送, 每个 chat id 一次请求

## 最小配置

```json
{
  "id": "tg",
  "type": "telegram",
  "settings": { "token": "example:token", "chatIds": ["111111"] }
}
```

## 字段

| 字段 | 类型 | 必填 | 默认 | 说明 |
| --- | --- | --- | --- | --- |
| `token` | string | 是 | — | Bot token, 只拼进 URL 路径, 不进入请求体 |
| `chatIds` | list\<string\> | 是 | — | 接收消息的 chat id, 一个或多个 |
| `apiBase` | string | 否 | `https://api.telegram.org` | Bot API 基地址, 可指向自建反向代理 |
| `silent` | bool | 否 | `false` | 静音发送 |

## 标题与正文

`--title` 与 `--body` **自动进入消息**, 不需要在 `settings` 里引用任何变量

两者拼成一条消息, 中间用空行分隔, 只有标题或只有正文时直接使用非空的那个

拼好的文本按 **UTF-16 code unit 计长截断到 4096**, 超出时截断并告警

## 多 chat 与重试

`chatIds` 里每个 id 各发一次请求, 已成功的 chat 记进本次发送状态, 重试只补失败的 chat

## 调用方法

### 内联写法

```bash
taskpush send --type telegram \
  --set token=example:token \
  --set chatIds=111111 \
  --title "构建完成" --body "commit abc123"
```

```bash
taskpush send --type telegram \
  --set token='${TokenInEnv}' \
  --set chatIds=111111 \
  --title "构建完成" --body "commit abc123"
```

`chatIds` 是列表字段, 用 `--set` 时给单个值即可; 要传多个, 写成逗号分隔或改用配置文件

### 配置文件写法

在 `taskpush.json` 的 `channels` 里加一条, 字段写进 `settings`:

```json
{
  "version": 1,
  "channels": [
    {
      "id": "tg",
      "type": "telegram",
      "settings": { "token": "${TokenInEnv}", "chatIds": ["111111"] }
    }
  ]
}
```

```bash
export TokenInEnv=example:token
taskpush send --title "构建完成" --body "commit abc123"
```

配置里用 `${VAR}` 引用密钥, 由运行环境注入, 文件本身可以安全入库, 变量未设置且没有默认值时加载即报错, 写成 `${TokenInEnv:-默认值}` 可允许缺省

注意 `${VAR}` 要生效, 文件里必须写字面量 `${TokenInEnv}`, 不能提前被编辑器的变量替换或 CI 的模板插值改掉

## 注意

- `token` 只出现在 URL 路径里, 预览与错误中不会出现 token 与 chat id
- `apiBase` 可指向自建反代, 用于网络受限环境

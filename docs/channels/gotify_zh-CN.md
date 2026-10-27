# gotify

[**English**](gotify.md) | [**简体中文**](gotify_zh-CN.md)

推送到自建 Gotify 服务的应用消息

## 最小配置

```json
{
  "id": "gotify",
  "type": "gotify",
  "settings": {
    "url": "https://gotify.example.com",
    "token": "exampleToken"
  }
}
```

## 字段

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `url` | string | 是 | 服务根地址, 支持模板 |
| `token` | string | 是 | application token, 走 `X-Gotify-Key` 头, 支持模板 |
| `priority` | int | 否 | 消息优先级 0 到 10, 支持模板; 只在配置时发送 |

## 端点拼接

POST 到 `{url}/message`, 拼接只看路径: 去掉尾部斜杠后已以 `/message` 结尾就保持原样, 否则追加, query 与 fragment 原样保留

## 标题与正文

`--title` 与 `--body` **自动进入请求**, 不需要在 `settings` 里引用任何变量

标题与正文**不拼接**:

- 正文进 `message` 字段, `title` 进 `title` 字段
- 正文为空时改用标题当 message, 两者都空报错
- message 按 **rune 截断到 32767** 并告警

## 调用方法

### 内联写法

```bash
taskpush send --type gotify \
  --set url='https://gotify.example.com' \
  --set token=exampleToken \
  --title "构建完成" --body "commit abc123"
```

```bash
taskpush send --type gotify \
  --set url='https://gotify.example.com' \
  --set token='${TokenInEnv}' \
  --title "构建完成" --body "commit abc123"
```

### 配置文件写法

在 `taskpush.json` 的 `channels` 里加一条, 字段写进 `settings`:

```json
{
  "version": 1,
  "channels": [
    {
      "id": "gotify",
      "type": "gotify",
      "settings": { "url": "https://gotify.example.com", "token": "${TokenInEnv}" }
    }
  ]
}
```

```bash
export TokenInEnv=exampleToken
taskpush send --title "构建完成" --body "commit abc123"
```

配置里用 `${VAR}` 引用密钥, 由运行环境注入, 文件本身可以安全入库, 变量未设置且没有默认值时加载即报错, 写成 `${TokenInEnv:-默认值}` 可允许缺省

注意 `${VAR}` 要生效, 文件里必须写字面量 `${TokenInEnv}`, 不能提前被编辑器的变量替换或 CI 的模板插值改掉

## 注意

- token 与完整地址只进入请求; 预览只带 host 与请求体字节数, 错误里不出现它们
- `priority` 是 0 到 10 的整数, 超出范围报错; 不配置则完全不发该字段

# discord

[**English**](discord.md) | [**简体中文**](discord_zh-CN.md)

通过 Discord 频道 Webhook 推送一条消息

## 最小配置

```json
{
  "id": "dc",
  "type": "discord",
  "settings": { "url": "https://discord.com/api/webhooks/123456/abcdef" }
}
```

## 字段

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `url` | string | 是 | 频道 webhook 地址, token 在 URL 里且只用于本次请求 |
| `username` | string | 否 | 覆盖 webhook 显示名; 渲染后为空时不发送该字段 |
| `avatarUrl` | string | 否 | 覆盖 webhook 头像; 非空时按 http/https URL 校验 |

## 标题与正文

`--title` 与 `--body` **自动进入消息**, 不需要在 `settings` 里引用任何变量

两者拼成一条 `content`, 中间空行分隔, 只有一方非空时用非空那个

按 **UTF-16 code unit 计长截断到 2000**, 截断时告警

## 响应判定

Discord 对成功与失败统一返回 204 或非 2xx 状态码, 响应体不带可解析的业务码, 因此这条渠道只按状态码判定, 不解析响应体

## 调用方法

### 内联写法

```bash
taskpush send --type discord \
  --set url='https://discord.com/api/webhooks/123456/abcdef' \
  --set username=TaskPush \
  --title "构建完成" --body "commit abc123"
```

```bash
taskpush send --type discord \
  --set url='${URLInEnv}' \
  --set username=TaskPush \
  --title "构建完成" --body "commit abc123"
```

### 配置文件写法

在 `taskpush.json` 的 `channels` 里加一条, 字段写进 `settings`:

```json
{
  "version": 1,
  "channels": [
    {
      "id": "dc",
      "type": "discord",
      "settings": { "url": "${URLInEnv}" }
    }
  ]
}
```

```bash
export URLInEnv='https://discord.com/api/webhooks/123456/abcdef'
taskpush send --title "构建完成" --body "commit abc123"
```

配置里用 `${VAR}` 引用密钥, 由运行环境注入, 文件本身可以安全入库, 变量未设置且没有默认值时加载即报错, 写成 `${URLInEnv:-默认值}` 可允许缺省

注意 `${VAR}` 要生效, 文件里必须写字面量 `${URLInEnv}`, 不能提前被编辑器的变量替换或 CI 的模板插值改掉

## 注意

- webhook 地址与 token 不出现在错误与预览里
- `username` / `avatarUrl` 只在渲染后非空时进入请求体

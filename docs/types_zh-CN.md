# 渠道类型索引

[**English**](types.md) | [**简体中文**](types_zh-CN.md)

渠道类型的配置指南索引, 每个渠道一份文档

字段表、请求形态与标题正文规则见各文档

执行 `taskpush types` 列出全部类型, 执行 `taskpush types <TYPE>` 可查看某类的字段

## 渠道一览

| 渠道 | `type` | 指南 | 必填字段 |
| --- | --- | --- | --- |
| 通用 Webhook | `webhook` | [webhook_zh-CN.md](channels/webhook_zh-CN.md) | `url` |
| Bark | `bark` | [bark_zh-CN.md](channels/bark_zh-CN.md) | `token` 或 `tokens` 之一 |
| Telegram Bot | `telegram` | [telegram_zh-CN.md](channels/telegram_zh-CN.md) | `token` `chatIds` |
| ServerChan | `serverchan` | [serverchan_zh-CN.md](channels/serverchan_zh-CN.md) | `token` |
| 钉钉群机器人 | `dingtalk` | [dingtalk_zh-CN.md](channels/dingtalk_zh-CN.md) | `url`(须带 `access_token`) |
| 企业微信群机器人 | `wecom` | [wecom_zh-CN.md](channels/wecom_zh-CN.md) | `url`(须带 `key`) |
| Discord 频道 Webhook | `discord` | [discord_zh-CN.md](channels/discord_zh-CN.md) | `url` |
| ntfy | `ntfy` | [ntfy_zh-CN.md](channels/ntfy_zh-CN.md) | `url` |
| Gotify | `gotify` | [gotify_zh-CN.md](channels/gotify_zh-CN.md) | `url` `token` |

## 备注

所有渠道共用同一套配置结构: `id`、`type`、`enabled` 与该渠道自己的 `settings` 对象, 公共字段 (`timeout`、`retries`、`retryDelay`) 与 `${VAR}` 环境变量展开对九个渠道一视同仁, 详见 [usage_zh-CN.md](usage_zh-CN.md)

另外, **各渠道的截断上限不同**, 每个渠道按平台规则各有一套上限, 具体规则请查看对应渠道文档
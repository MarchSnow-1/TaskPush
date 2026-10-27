# Channel type index

[**English**](types.md) | [**简体中文**](types_zh-CN.md)

An index of the configuration guides for the channel types, one document per channel

The field table, the request shape and the title/body rules live in each document

Run `taskpush types` to list every type, and `taskpush types <TYPE>` to see the fields of one type

## Channels at a glance

| Channel | `type` | Guide | Required fields |
| --- | --- | --- | --- |
| Generic webhook | `webhook` | [webhook.md](channels/webhook.md) | `url` |
| Bark | `bark` | [bark.md](channels/bark.md) | one of `token` or `tokens` |
| Telegram Bot | `telegram` | [telegram.md](channels/telegram.md) | `token` `chatIds` |
| ServerChan | `serverchan` | [serverchan.md](channels/serverchan.md) | `token` |
| DingTalk group robot | `dingtalk` | [dingtalk.md](channels/dingtalk.md) | `url` (carrying `access_token`) |
| WeCom group robot | `wecom` | [wecom.md](channels/wecom.md) | `url` (carrying `key`) |
| Discord channel webhook | `discord` | [discord.md](channels/discord.md) | `url` |
| ntfy | `ntfy` | [ntfy.md](channels/ntfy.md) | `url` |
| Gotify | `gotify` | [gotify.md](channels/gotify.md) | `url` `token` |

## Notes

Every channel uses the same configuration shape: `id`, `type`, `enabled` and the channel's own `settings` object, and the shared fields (`timeout`, `retries`, `retryDelay`) plus `${VAR}` environment expansion work the same way across all nine; see [usage.md](usage.md)

Also, **the truncation limits differ between channels**, each channel follows its own platform rules, so check the matching channel document for the specific limit

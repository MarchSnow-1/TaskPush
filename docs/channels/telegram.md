# telegram

[**English**](telegram.md) | [**简体中文**](telegram_zh-CN.md)

Pushes through `sendMessage` of the Telegram Bot API, one request per chat id

## Minimal configuration

```json
{
  "id": "tg",
  "type": "telegram",
  "settings": { "token": "example:token", "chatIds": ["111111"] }
}
```

## Fields

| Field | Type | Required | Default | Meaning |
| --- | --- | --- | --- | --- |
| `token` | string | yes | — | Bot token, spliced into the URL path only and never into the request body |
| `chatIds` | list\<string\> | yes | — | Chat ids receiving the message, one or more |
| `apiBase` | string | no | `https://api.telegram.org` | Bot API base address, usable for a self-hosted reverse proxy |
| `silent` | bool | no | `false` | Send silently |

## Title and body

`--title` and `--body` **enter the message automatically**, with no variable to reference in `settings`

The title and the body are joined into one message with a blank line between, and when only the title or only the body is present the non-empty side is used as is

The joined text is **measured in UTF-16 code units and truncated to 4096**, with a warning when it is cut

## Several chats and retry

Every id in `chatIds` gets one request, the chats that succeeded are recorded, and a retry reaches only the failed chats

## How to call it

### Inline form

```bash
taskpush send --type telegram \
  --set token=example:token \
  --set chatIds=111111 \
  --title "Build done" --body "commit abc123"
```

```bash
taskpush send --type telegram \
  --set token='${TokenInEnv}' \
  --set chatIds=111111 \
  --title "Build done" --body "commit abc123"
```

`chatIds` is a list field, so a single value through `--set` is enough; for several, write them comma-separated or switch to the configuration file

### Configuration file form

Add one entry to `channels` in `taskpush.json`, with the fields inside `settings`:

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
taskpush send --title "Build done" --body "commit abc123"
```

The configuration references secrets with `${VAR}`, injected by the runtime, so the file itself is safe to commit; an unset variable without a default fails during loading, and `${TokenInEnv:-default}` allows a missing value

For `${VAR}` to work the file must carry the literal `${TokenInEnv}`, untouched by editor variable substitution or CI template interpolation

## Notes

- `token` appears in the URL path only, so neither the token nor the chat ids show up in the preview or in errors
- `apiBase` can point at a self-hosted reverse proxy for networks with restricted access

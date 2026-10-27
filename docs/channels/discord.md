# discord

[**English**](discord.md) | [**简体中文**](discord_zh-CN.md)

Pushes one message through a Discord channel webhook

## Minimal configuration

```json
{
  "id": "dc",
  "type": "discord",
  "settings": { "url": "https://discord.com/api/webhooks/123456/abcdef" }
}
```

## Fields

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `url` | string | yes | Channel webhook address, whose token sits in the URL and is used for this request alone |
| `username` | string | no | Display name overriding the webhook name; the field is omitted when it renders empty |
| `avatarUrl` | string | no | Avatar overriding the webhook avatar; checked as an http/https URL when non-empty |

## Title and body

`--title` and `--body` **enter the message automatically**, with no variable to reference in `settings`

The title and the body are joined into one `content` with a blank line between, using whichever side is non-empty when only one is

The content is **measured in UTF-16 code units and truncated to 2000**, with a warning when cut

## Judging the response

Discord answers 204 or a non-2xx status for success and failure alike, and the response body carries no parsable business code, so this channel judges by status code alone and does not parse the body

## How to call it

### Inline form

```bash
taskpush send --type discord \
  --set url='https://discord.com/api/webhooks/123456/abcdef' \
  --set username=TaskPush \
  --title "Build done" --body "commit abc123"
```

```bash
taskpush send --type discord \
  --set url='${URLInEnv}' \
  --set username=TaskPush \
  --title "Build done" --body "commit abc123"
```

### Configuration file form

Add one entry to `channels` in `taskpush.json`, with the fields inside `settings`:

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
taskpush send --title "Build done" --body "commit abc123"
```

The configuration references secrets with `${VAR}`, injected by the runtime, so the file itself is safe to commit; an unset variable without a default fails during loading, and `${URLInEnv:-default}` allows a missing value

For `${VAR}` to work the file must carry the literal `${URLInEnv}`, untouched by editor variable substitution or CI template interpolation

## Notes

- The webhook address and its token show up in neither errors nor the preview
- `username` and `avatarUrl` enter the request body only when they render non-empty

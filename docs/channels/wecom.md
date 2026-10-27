# wecom

[**English**](wecom.md) | [**简体中文**](wecom_zh-CN.md)

Pushes to a WeCom group robot

## Minimal configuration

```json
{
  "id": "wecom",
  "type": "wecom",
  "settings": { "url": "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=exampleToken" }
}
```

## Fields

| Field | Type | Required | Default | Meaning |
| --- | --- | --- | --- | --- |
| `url` | string | yes | — | Full group robot address, which has to carry the `key` query parameter |
| `msgType` | string | no | `text` | `text` or `markdown` |

## Title and body

`--title` and `--body` **enter the message automatically**, with no variable to reference in `settings`

The `text` type joins the title and the body into one `content` with a blank line between, using whichever side is non-empty when only one is

The `markdown` type joins the content the same way and puts it into the `markdown` field

The content is truncated on **UTF-8 bytes**, with a warning when cut:

| msgType | Limit |
| --- | --- |
| `text` | 2048 bytes |
| `markdown` | 4096 bytes |

Both types require the joined content to be non-empty, and both empty reports an error

## How to call it

### Inline form

```bash
taskpush send --type wecom \
  --set url='https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=exampleToken' \
  --title "Build done" --body "commit abc123"
```

```bash
taskpush send --type wecom \
  --set url='https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=${TokenInEnv}' \
  --title "Build done" --body "commit abc123"
```

### Configuration file form

Add one entry to `channels` in `taskpush.json`, with the fields inside `settings`:

```json
{
  "version": 1,
  "channels": [
    {
      "id": "wecom",
      "type": "wecom",
      "settings": { "url": "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=${TokenInEnv}" }
    }
  ]
}
```

```bash
export TokenInEnv=exampleToken
taskpush send --title "Build done" --body "commit abc123"
```

The configuration references secrets with `${VAR}`, injected by the runtime, so the file itself is safe to commit; an unset variable without a default fails during loading, and `${TokenInEnv:-default}` allows a missing value

For `${VAR}` to work the file must carry the literal `${TokenInEnv}`, untouched by editor variable substitution or CI template interpolation

## Notes

- The `key` inside `url` is used for sending only, and neither the preview nor the error data carries it
- Truncation counts UTF-8 bytes, so a Chinese character takes 3 bytes and the usable character count is below the byte limit

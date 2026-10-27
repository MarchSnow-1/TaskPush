# ntfy

[**English**](ntfy.md) | [**简体中文**](ntfy_zh-CN.md)

Posts a message as plain text to the topic address of an ntfy server, self-hosted or official

## Minimal configuration

```json
{
  "id": "ntfy",
  "type": "ntfy",
  "settings": { "url": "https://ntfy.example.com/mytopic" }
}
```

## Fields

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `url` | string | yes | Full topic address, whose last path segment is the topic |
| `token` | string | no | Bearer token sent through the `Authorization` header; omitted when it renders empty |
| `priority` | int or string | no | Message priority, `1` to `5` or `min` / `low` / `default` / `high` / `max` |
| `tags` | list\<string\> | no | Tags sent through the `Tags` header; either an array or a comma-separated string |

## Title and body

`--title` and `--body` **enter the request automatically**, with no variable to reference in `settings`

**The body goes into the request body and the title into the `Title` header**, so the two travel separately

An empty body falls back to the title, and both empty reports an error

The body is **truncated on UTF-8 bytes to 4096**, with a warning when cut

## Priority

`priority` accepts a number or an enum name, with numbers limited to 1 through 5:

| Number | Enum |
| --- | --- |
| 1 | `min` |
| 2 | `low` |
| 3 | `default` |
| 4 | `high` |
| 5 | `max` |

Enum names are compared without regard to case, and the `Priority` header goes out only when priority is configured

## How to call it

### Inline form

```bash
taskpush send --type ntfy \
  --set url='https://ntfy.example.com/mytopic' \
  --set priority=high \
  --title "Build done" --body "commit abc123"
```

```bash
taskpush send --type ntfy \
  --set url='https://ntfy.example.com/mytopic' \
  --set token='${TokenInEnv}' \
  --set priority=high \
  --title "Build done" --body "commit abc123"
```

### Configuration file form

Add one entry to `channels` in `taskpush.json`, with the fields inside `settings`:

```json
{
  "version": 1,
  "channels": [
    {
      "id": "ntfy",
      "type": "ntfy",
      "settings": { "url": "https://ntfy.example.com/mytopic", "token": "${TokenInEnv}" }
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

- The full address and the token are used for sending only; the address in the preview and in errors is masked first, and the token never enters error data
- Control characters in a header value are validated and refused, so a tag whose template renders a newline surfaces at this step

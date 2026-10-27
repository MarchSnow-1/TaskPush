# bark

[**English**](bark.md) | [**简体中文**](bark_zh-CN.md)

Pushes to the Bark app on iOS, one device or a batch of devices

## Minimal configuration

```json
{
  "id": "phone",
  "type": "bark",
  "settings": { "token": "exampleToken" }
}
```

## Fields

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `token` | string | one of two | Single-device key, mutually exclusive with `tokens`; a group key may carry a slash |
| `tokens` | list\<string\> | one of two | Device keys for a batch push, mutually exclusive with `token` |
| `server` | string | no | Server address, defaulting to `https://api.day.app` |
| `subtitle` | string | no | Notification subtitle |
| `group` | string | no | Notification group name |
| `level` | string | no | Interruption level such as `active` / `timeSensitive` / `passive` / `critical` |
| `sound` | string | no | Notification sound name |
| `icon` | string | no | Notification icon URL |
| `image` | string | no | Notification image URL |
| `clickUrl` | string | no | Address opened when the notification is tapped |
| `copy` | string | no | Text copied when the notification is long-pressed |
| `badge` | int | no | App icon badge number, a non-negative integer |
| `ttl` | int | no | Message time to live in seconds, a non-negative integer |
| `autoCopy` | bool | no | Copy the content automatically |

## Request shape

One device goes through `{server}/{token}`, several go through `{server}/push`, and both use a JSON body, with the title and the body landing in the `title` and `body` fields without being joined

## Title and body

`--title` and `--body` **enter the request automatically**, with no variable to reference in `settings`

Neither is **truncated**, so their length is bounded only by the 64KB rendering cap of TaskPush, which sets the channel apart from the others

When both render as blank the channel reports an error, so give at least one

## How to call it

### Inline form

```bash
taskpush send --type bark --set token=exampleToken --title "Build done" --body "commit abc123"
```

```bash
taskpush send --type bark --set token='${TokenInEnv}' --title "Build done" --body "commit abc123"
```

### Configuration file form

Add one entry to `channels` in `taskpush.json`, with the fields inside `settings`:

```json
{
  "version": 1,
  "channels": [
    {
      "id": "phone",
      "type": "bark",
      "settings": { "token": "${TokenInEnv}" }
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

## Batch delivery and retry

A batch push checks `data[].code` per device, records the devices that succeeded, and a retry reaches only the failed ones

When the response carries no `device_key` the set cannot be narrowed, so the next attempt resends everything the configuration lists and raises a warning alongside

## Notes

- Give exactly one of `token` and `tokens`, both or neither reports an error
- `server` can point at a self-hosted Bark server

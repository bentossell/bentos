---
name: gcal
description: Google Calendar surface for syncing upcoming events and proposing/applying safe calendar actions.
accounts:
  - ben.tossell@gmail.com
  - ben@bensbites.com
  - ben@factory.ai
calendar_id: primary
max_events: 50
---

# Google Calendar

This skill uses the `gccli` tool.

## Allowed operations

| Operation | Allowed | Notes |
|---|---:|---|
| sync | yes | reads only |
| create_event | propose only | (apply supports, but off by default) |

## References

- `references/factory-skill.md` (CLI usage)

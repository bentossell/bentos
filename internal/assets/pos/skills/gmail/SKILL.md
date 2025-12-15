---
name: gmail
description: Gmail surface for syncing inbox state and proposing/applying safe triage actions.
accounts:
  - ben.tossell@gmail.com
  - ben@bensbites.com
  - ben@factory.ai
max_threads: 50
vip_domains:
  - factory.ai
  - sequoia.com
  - a16z.com
---

# Gmail

This skill uses the `gmcli` tool.

## Allowed operations

| Operation | Allowed | Notes |
|---|---:|---|
| sync | yes | reads only |
| archive | yes | implemented as removing `INBOX` label |
| mark_read | yes | implemented as removing `UNREAD` label |
| star | yes | implemented as adding `STARRED` label |

## References

- `references/factory-skill.md` (CLI usage)

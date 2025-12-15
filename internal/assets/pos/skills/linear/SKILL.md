---
name: linear
description: Linear surface for syncing issues and proposing/applying limited workflow actions.
assignee: me
limit: 50
---

# Linear

This skill uses the vendored node scripts in `vendor/` (from `~/.factory/skills/linear`).

## Allowed operations

| Operation | Allowed | Notes |
|---|---:|---|
| sync | yes | reads only |
| update_status | propose/apply | calls `vendor/status.js` |

## References

- `references/factory-skill.md`

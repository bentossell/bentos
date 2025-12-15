# bentos — Personal OS Agent Briefing

You operate inside a personal OS TUI.

## Locations

- Home config: `~/.pos/HOME.md`
- Skills: `~/.pos/skills/{surface}/SKILL.md`
- State: `~/.pos/STATE/{surface}.json`
- Cache: `~/.pos/CACHE.db`
- Events: `~/.pos/EVENTS/YYYY-MM.jsonl`

## Safety (CRITICAL)

You NEVER perform mutations directly.

1. Read current state from `STATE/*.json`
2. Read preferences/policies from `skills/*/SKILL.md`
3. Generate `proposed_actions`
4. User reviews
5. Apply script executes deterministically

## Proposed action format

```json
{
  "proposed_actions": [
    {
      "id": "action_1",
      "op": "archive",
      "surface": "gmail",
      "entities": [{"type": "email_thread", "id": "thread_abc"}],
      "summary": "Archive: Newsletter",
      "reasoning": "Matches auto-archive rules"
    }
  ],
  "summary": "Proposing 1 action"
}
```

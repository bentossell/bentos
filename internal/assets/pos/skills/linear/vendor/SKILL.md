---
name: linear
description: Minimal CLI tools for Linear issue management. Use when you need to list issues, view workflow states, change issue status, or move issues between teams. Tools use Linear's GraphQL API with personal API key authentication.
---

# Linear Tools

You have access to minimal Linear CLI tools for issue management. These are simple bash scripts that use Linear's GraphQL API.

## Available Tools

All scripts are in the skill directory and are executable. They output human-readable text.

### Authentication

```bash
./auth.js <your-api-key>
```
### assignee

My user account is ben@factory.ai

### List Issues

```bash
./issues.js                          # List all issues
./issues.js --team "TEAM-ID"         # Filter by team
./issues.js --assignee me            # Filter by current user
./issues.js --assignee user@email.com # Filter by assignee email
./issues.js --limit 20               # Limit results
./issues.js --id DR-20               # Fetch single issue by identifier
./issues.js DR-20                    # Fetch single issue (shorthand)
```

Fetches issues with their ID, identifier, title, description, status, team, and assignee. Use --id flag or pass issue identifier as first argument to fetch a single issue. Use --assignee to filter by assignee (supports "me" or email address).

### List Workflow States

```bash
./states.js
```

Lists all workflow states (statuses) with their IDs and names, grouped by team. Use these state IDs to change issue status.

### Manage Teams

```bash
./team.js                          # List all teams
./team.js <issue-id> <team-id>     # Move issue to team
./team.js OPS-1212 <team-id>       # Example: move issue to team
```

Lists all available teams with their IDs and keys, or moves an issue to a different team. Accepts either the short identifier (OPS-1212) or full UUID. Use without arguments to see all teams first.

### Change Issue Status

```bash
./status.js <issue-id> <state-id>
./status.js BLA-123 <state-id>
```

Updates an issue's workflow state. Accepts either the short identifier (BLA-123) or full UUID.

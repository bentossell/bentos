---
widgets:
  - id: inbox_unread
    title: Gmail Unread
    type: table
    source: state
    surface: gmail
    path: STATE/gmail.json
    query: "unread == true"
    columns: ["from", "subject", "age"]
    max_rows: 8

  - id: linear_assigned
    title: Linear — Assigned
    type: table
    source: state
    surface: linear
    path: STATE/linear.json
    query: "assignee == 'me'"
    columns: ["identifier", "title", "status", "age"]
    max_rows: 8

  - id: github_accounts
    title: GitHub — Accounts
    type: table
    source: state
    surface: github
    path: STATE/github.json
    query: "active == true"
    columns: ["login", "scopes"]
    max_rows: 8

  - id: today_calendar
    title: Calendar — Today
    type: list
    source: state
    surface: gcal
    path: STATE/gcal.json
    query: "start >= today() && start < tomorrow()"
    format: "{time} - {summary}"
    max_rows: 10

  - id: recent_activity
    title: Recent Activity
    type: list
    source: log
    path: EVENTS/*
    query: "ts > now() - 24h"
    format: "{ts} [{surface}.{op}] {summary}"
    max_rows: 10
---

# Home

- `:` open command input
- Suggested commands: `gmail sync`, `gmail propose`, `gmail apply`, `linear sync`, `github sync`

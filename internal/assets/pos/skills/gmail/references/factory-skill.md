---
name: gmail
description: Gmail CLI for searching, reading threads, managing labels, and sending emails. Use when you need to check inbox, read emails, send messages, or manage email labels.
---

# Gmail CLI (gmcli)

You have access to `gmcli` - a minimal Gmail CLI. All commands use the format:

```bash
gmcli <email> <command> [options]
```

## Available Accounts

- ben.tossell@gmail.com
- ben@bensbites.com  
- ben@factory.ai

## Commands

### Search Emails

```bash
gmcli <email> search <query> [--max N]
```

Query examples:
- `in:inbox`, `in:sent`, `in:drafts`
- `is:unread`, `is:starred`, `is:important`
- `from:sender@example.com`, `to:recipient@example.com`
- `subject:keyword`
- `has:attachment`
- `after:2024/01/01`, `before:2024/12/31`
- Combine: `in:inbox is:unread from:boss@company.com`

Examples:
```bash
gmcli ben@factory.ai search "in:inbox" --max 10
gmcli ben@factory.ai search "is:unread" --max 50
gmcli ben@factory.ai search "from:someone@example.com has:attachment"
```

### Read Thread

```bash
gmcli <email> thread <threadId>
gmcli <email> thread <threadId> --download  # Download attachments
```

### List Labels

```bash
gmcli <email> labels list
```

### Modify Labels

```bash
gmcli <email> labels <threadIds...> --add LABEL --remove LABEL
```

System labels: `INBOX`, `UNREAD`, `STARRED`, `IMPORTANT`, `TRASH`, `SPAM`

Examples:
```bash
gmcli ben@factory.ai labels abc123 --remove UNREAD
gmcli ben@factory.ai labels abc123 --add STARRED
```

### Send Email

```bash
gmcli <email> send --to <emails> --subject <s> --body <b> [options]
```

Options:
- `--to <emails>` - Recipients (comma-separated, required)
- `--subject <s>` - Subject line (required)
- `--body <b>` - Message body (required)
- `--cc <emails>` - CC recipients
- `--bcc <emails>` - BCC recipients
- `--reply-to <messageId>` - Reply to a message
- `--attach <file>` - Attach file (can use multiple times)

Examples:
```bash
gmcli ben@factory.ai send --to someone@example.com --subject "Hi" --body "Hello there"
gmcli ben@factory.ai send --to a@x.com --subject "Re: Topic" --body "Reply" --reply-to 19aea1f2f3532db5
```

### Drafts

```bash
gmcli <email> drafts list
gmcli <email> drafts get <draftId>
gmcli <email> drafts create --to <emails> --subject <s> --body <b>
gmcli <email> drafts send <draftId>
gmcli <email> drafts delete <draftId>
```

### Get Gmail URL

```bash
gmcli <email> url <threadIds...>
```

## Notes

- Thread IDs are returned from search results
- Use `--max` to limit search results (default: 10)
- Attachments download to `~/.gmcli/attachments/`

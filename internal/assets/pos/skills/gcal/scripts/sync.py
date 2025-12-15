#!/usr/bin/env python3

import json
import os
import subprocess
import sys
from datetime import datetime, timedelta, timezone
from typing import Any


def emit(event: dict[str, Any]) -> None:
	print(json.dumps(event, ensure_ascii=False))
	sys.stdout.flush()


def parse_frontmatter(md_path: str) -> dict[str, Any]:
	with open(md_path, 'r', encoding='utf-8') as f:
		lines = f.read().splitlines()
	if not lines or lines[0].strip() != '---':
		return {}
	data: dict[str, Any] = {}
	i = 1
	current_list_key: str | None = None
	while i < len(lines):
		line = lines[i]
		i += 1
		if line.strip() == '---':
			break
		if not line.strip() or line.lstrip().startswith('#'):
			continue
		if line.lstrip().startswith('- ') and current_list_key:
			data.setdefault(current_list_key, []).append(line.strip()[2:])
			continue
		current_list_key = None
		if ':' not in line:
			continue
		key, raw = line.split(':', 1)
		key = key.strip()
		raw = raw.strip()
		if raw == '':
			current_list_key = key
			data[key] = []
			continue
		if raw.isdigit():
			data[key] = int(raw)
			continue
		data[key] = raw
	return data


def try_parse_json(text: str) -> Any | None:
	text = text.strip()
	if not text:
		return None
	if not (text.startswith('{') or text.startswith('[')):
		return None
	try:
		return json.loads(text)
	except Exception:
		return None


def extract_json(text: str) -> Any | None:
	text = text.strip()
	if not text:
		return None
	decoder = json.JSONDecoder()
	for i, ch in enumerate(text):
		if ch not in '[{':
			continue
		try:
			obj, _end = decoder.raw_decode(text[i:])
			return obj
		except Exception:
			continue
	return None


def pick_dt(v: Any) -> str:
	if isinstance(v, dict):
		return str(v.get('dateTime') or v.get('date') or '')
	if v is None:
		return ''
	return str(v)


def normalize_event(raw: dict[str, Any], account: str, calendar_id: str) -> dict[str, Any] | None:
	eid = str(raw.get('id') or raw.get('eventId') or '')
	if not eid:
		return None
	summary = raw.get('summary') or raw.get('title') or ''
	start = pick_dt(raw.get('start') or raw.get('startTime'))
	end = pick_dt(raw.get('end') or raw.get('endTime'))
	out: dict[str, Any] = {
		'id': eid,
		'summary': summary,
		'start': start,
		'end': end,
		'account': account,
		'calendar_id': calendar_id,
	}
	for k in ('location', 'description', 'htmlLink'):
		if k in raw and raw.get(k) is not None:
			out[k] = raw.get(k)
	att = raw.get('attendees')
	if isinstance(att, list):
		emails: list[str] = []
		for a in att:
			if isinstance(a, dict):
				email = a.get('email')
				if isinstance(email, str) and email:
					emails.append(email)
			elif isinstance(a, str) and a:
				emails.append(a)
		if emails:
			out['attendees'] = emails
	return out


def parse_events(parsed: Any, account: str, calendar_id: str) -> list[dict[str, Any]]:
	items: list[dict[str, Any]] = []
	if isinstance(parsed, list):
		for e in parsed:
			if not isinstance(e, dict):
				continue
			n = normalize_event(e, account, calendar_id)
			if n:
				items.append(n)
		return items
	if isinstance(parsed, dict):
		arr = parsed.get('events') or parsed.get('items') or []
		if isinstance(arr, list):
			for e in arr:
				if not isinstance(e, dict):
					continue
				n = normalize_event(e, account, calendar_id)
				if n:
					items.append(n)
		return items
	return items


def parse_tsv_events(text: str, account: str, calendar_id: str) -> list[dict[str, Any]]:
	text = (text or '').strip()
	if not text:
		return []
	if text.strip().lower() == 'no events':
		return []
	lines = [l for l in text.splitlines() if l.strip()]
	if not lines:
		return []
	header = lines[0].strip()
	if not header.upper().startswith('ID\tSTART\tEND\tSUMMARY'):
		return []
	out: list[dict[str, Any]] = []
	for line in lines[1:]:
		cols = line.split('\t')
		if len(cols) < 4:
			continue
		eid = cols[0].strip()
		if not eid:
			continue
		start = cols[1].strip()
		end = cols[2].strip()
		summary = '\t'.join(cols[3:]).strip()
		raw = {'id': eid, 'summary': summary, 'start': start, 'end': end}
		n = normalize_event(raw, account, calendar_id)
		if n:
			out.append(n)
	return out


def main() -> int:
	pos_dir = os.environ.get('POS_DIR')
	if not pos_dir:
		emit({'type': 'error', 'message': 'POS_DIR not set'})
		return 1

	skill_md = os.path.join(pos_dir, 'skills', 'gcal', 'SKILL.md')
	prefs = parse_frontmatter(skill_md)
	accounts = prefs.get('accounts') or []
	calendar_id = prefs.get('calendar_id') or 'primary'
	max_events = int(prefs.get('max_events') or 50)
	days_back = int(prefs.get('days_back') or 1)
	days_ahead = int(prefs.get('days_ahead') or 14)
	if not isinstance(accounts, list):
		accounts = []
	if not accounts:
		fallback = prefs.get('account') or ''
		if fallback:
			accounts = [fallback]
	if not accounts:
		emit({'type': 'error', 'message': 'gcal accounts not configured in SKILL.md frontmatter'})
		return 1

	now = datetime.now(timezone.utc)
	start = now - timedelta(days=days_back)
	end = now + timedelta(days=days_ahead)
	start_s = start.isoformat().replace('+00:00', 'Z')
	end_s = end.isoformat().replace('+00:00', 'Z')

	all_events: list[dict[str, Any]] = []
	per_account: dict[str, int] = {}
	per_account_debug: dict[str, Any] = {}
	per_account_errors: dict[str, Any] = {}
	for idx, account in enumerate(accounts):
		if not isinstance(account, str) or not account:
			continue
		pct = 0.1 + 0.7 * (idx / max(len(accounts), 1))
		emit({'type': 'progress', 'message': f'gccli {account} events {calendar_id}', 'pct': pct})

		try:
			proc = subprocess.run(
				[
					'gccli',
					account,
					'events',
					str(calendar_id),
					'--from',
					start_s,
					'--to',
					end_s,
					'--max',
					str(max_events),
				],
				check=True,
				capture_output=True,
				text=True,
			)
		except FileNotFoundError:
			emit({'type': 'error', 'message': 'gccli not found in PATH'})
			return 1
		except subprocess.CalledProcessError as e:
			per_account_errors[account] = {
				'code': e.returncode,
				'stderr': (e.stderr or '').strip(),
			}
			continue

		raw_out = (proc.stdout or '').strip()
		parsed = extract_json(raw_out) or try_parse_json(raw_out)
		events = parse_events(parsed, account, str(calendar_id))
		if parsed is None and not events:
			events = parse_tsv_events(raw_out, account, str(calendar_id))
		if parsed is None and not events:
			per_account_debug[account] = {
				'parse_error': True,
				'stdout_head': raw_out[:2000],
				'stderr_head': (proc.stderr or '').strip()[:2000],
			}
		all_events.extend(events)
		per_account[account] = len(events)

	# Sort by start time where possible.
	def start_key(e: dict[str, Any]) -> str:
		return str(e.get('start') or '')

	all_events.sort(key=start_key)

	last_sync = now.isoformat().replace('+00:00', 'Z')
	state = {
		'last_sync': last_sync,
		'accounts': accounts,
		'calendar_id': calendar_id,
		'range': {'from': start_s, 'to': end_s, 'days_back': days_back, 'days_ahead': days_ahead},
		'events': all_events,
		'stats': {'count': len(all_events), 'per_account_count': per_account},
		'errors': per_account_errors or None,
		'debug': per_account_debug or None,
		'raw': None,
	}

	os.makedirs(os.path.join(pos_dir, 'STATE'), exist_ok=True)
	state_path = os.path.join(pos_dir, 'STATE', 'gcal.json')
	with open(state_path, 'w', encoding='utf-8') as f:
		json.dump(state, f, indent=2, ensure_ascii=False)
		f.write('\n')

	emit({'type': 'artifact', 'path': 'STATE/gcal.json', 'description': 'Updated calendar index state'})
	emit({'type': 'result', 'ok': True, 'data': {'events': len(all_events), 'accounts': accounts, 'calendar_id': calendar_id}})
	return 0


if __name__ == '__main__':
	raise SystemExit(main())

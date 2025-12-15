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
	start = now
	end = now + timedelta(days=7)
	start_s = start.isoformat().replace('+00:00', 'Z')
	end_s = end.isoformat().replace('+00:00', 'Z')

	all_events: list[dict[str, Any]] = []
	per_account: dict[str, int] = {}
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
			emit(
				{
					'type': 'error',
					'message': 'gccli events failed',
					'details': {
						'account': account,
						'code': e.returncode,
						'stderr': (e.stderr or '').strip(),
					},
				},
			)
			return 1

		parsed = try_parse_json(proc.stdout)
		events: list[dict[str, Any]] = []
		if isinstance(parsed, list):
			for e in parsed:
				if not isinstance(e, dict):
					continue
				events.append(
					{
						'id': str(e.get('id') or e.get('eventId') or ''),
						'summary': e.get('summary') or e.get('title') or '',
						'start': e.get('start') or e.get('startTime') or '',
						'end': e.get('end') or e.get('endTime') or '',
						'account': account,
					},
				)
		elif isinstance(parsed, dict):
			items = parsed.get('events') or parsed.get('items') or []
			if isinstance(items, list):
				for e in items:
					if not isinstance(e, dict):
						continue
					events.append(
						{
							'id': str(e.get('id') or ''),
							'summary': e.get('summary') or '',
							'start': (e.get('start') or {}).get('dateTime') if isinstance(e.get('start'), dict) else e.get('start'),
							'end': (e.get('end') or {}).get('dateTime') if isinstance(e.get('end'), dict) else e.get('end'),
							'account': account,
						},
					)

		all_events.extend(events)
		per_account[account] = len(events)

	last_sync = now.isoformat().replace('+00:00', 'Z')
	state = {
		'last_sync': last_sync,
		'accounts': accounts,
		'events': all_events,
		'stats': {'count': len(all_events), 'per_account_count': per_account},
		'raw': None if all_events else 'No events',
	}

	os.makedirs(os.path.join(pos_dir, 'STATE'), exist_ok=True)
	state_path = os.path.join(pos_dir, 'STATE', 'gcal.json')
	with open(state_path, 'w', encoding='utf-8') as f:
		json.dump(state, f, indent=2, ensure_ascii=False)
		f.write('\n')

	emit({'type': 'artifact', 'path': 'STATE/gcal.json', 'description': 'Updated calendar index state'})
	emit({'type': 'result', 'ok': True, 'data': {'events': len(all_events), 'accounts': accounts}})
	return 0


if __name__ == '__main__':
	raise SystemExit(main())

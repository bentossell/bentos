#!/usr/bin/env python3

import json
import os
import re
import subprocess
import sys
from datetime import datetime, timezone
from typing import Any


def emit(event: dict[str, Any]) -> None:
	print(json.dumps(event, ensure_ascii=False))
	sys.stdout.flush()


def parse_frontmatter(md_path: str) -> dict[str, Any]:
	# Minimal YAML-ish frontmatter parser (supports scalars + lists)
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
		if raw.lower() in ('true', 'false'):
			data[key] = raw.lower() == 'true'
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


def parse_search_output(output: str) -> list[dict[str, Any]]:
	# gmcli currently emits a tabular, tab-separated format:
	# ID\tDATE\tFROM\tSUBJECT\tLABELS
	# Parse that first (and ignore the header row).
	threads: list[dict[str, Any]] = []
	for raw in output.splitlines():
		line = raw.strip()
		if not line:
			continue
		if line.startswith('ID\t') or line == 'ID' or line.startswith('ID '):
			continue
		if line.startswith('Total:'):
			continue

		cols = [c.strip() for c in re.split(r'\t+', raw) if c.strip()]
		if len(cols) >= 4:
			thread_id = cols[0]
			date_raw = cols[1]
			from_ = cols[2]
			subject = cols[3]
			labels_raw = cols[4] if len(cols) >= 5 else ''
			labels = [l for l in (labels_raw.split(',') if labels_raw else []) if l]

			# gmcli date looks like: YYYY-MM-DD HH:MM
			date_iso = ''
			try:
				dt = datetime.strptime(date_raw, '%Y-%m-%d %H:%M').replace(tzinfo=timezone.utc)
				date_iso = dt.isoformat().replace('+00:00', 'Z')
			except Exception:
				date_iso = date_raw

			threads.append(
				{
					'id': thread_id,
					'subject': subject,
					'from': from_,
					'date': date_iso,
					'labels': labels,
					'unread': ('UNREAD' in labels) if labels else True,
					'inbox': ('INBOX' in labels) if labels else True,
					'starred': 'STARRED' in labels,
				},
			)
			continue

	if threads:
		return threads

	parsed = try_parse_json(output)
	if isinstance(parsed, list):
		for item in parsed:
			if not isinstance(item, dict):
				continue
			thread_id = item.get('threadId') or item.get('thread_id') or item.get('id')
			if not thread_id:
				continue
			threads.append(
				{
					'id': str(thread_id),
					'subject': item.get('subject') or item.get('title') or '',
					'from': item.get('from') or item.get('sender') or '',
					'date': item.get('date') or item.get('internalDate') or '',
					'unread': True,
					'inbox': True,
				},
			)
		return threads

	if isinstance(parsed, dict):
		items = parsed.get('threads') or parsed.get('results') or parsed.get('messages')
		if isinstance(items, list):
			return parse_search_output(json.dumps(items))

	for line in output.splitlines():
		line = line.strip()
		if not line:
			continue
		if line.startswith('ID') and 'DATE' in line and 'SUBJECT' in line:
			continue
		parts = [p.strip() for p in line.split('|')]
		if len(parts) >= 3:
			thread_id, from_, subject = parts[0], parts[1], ' | '.join(parts[2:])
			threads.append(
				{
					'id': thread_id,
					'subject': subject,
					'from': from_,
					'unread': True,
					'inbox': True,
				},
			)
			continue
		tokens = line.split()
		if tokens:
			thread_id = tokens[0]
			subject = line[len(thread_id) :].strip()
			threads.append(
				{
					'id': thread_id,
					'subject': subject,
					'from': '',
					'unread': True,
					'inbox': True,
				},
			)
	return threads


def main() -> int:
	pos_dir = os.environ.get('POS_DIR')
	if not pos_dir:
		emit({'type': 'error', 'message': 'POS_DIR not set'})
		return 1

	skill_md = os.path.join(pos_dir, 'skills', 'gmail', 'SKILL.md')
	prefs = parse_frontmatter(skill_md)
	account = prefs.get('account') or ''
	max_threads = int(prefs.get('max_threads') or 50)
	if not account:
		emit({'type': 'error', 'message': 'gmail account not configured in SKILL.md frontmatter'})
		return 1

	query = 'in:inbox is:unread'
	emit({'type': 'progress', 'message': f'gmcli search {account} "{query}"', 'pct': 0.1})

	try:
		proc = subprocess.run(
			['gmcli', account, 'search', query, '--max', str(max_threads)],
			check=True,
			capture_output=True,
			text=True,
		)
	except FileNotFoundError:
		emit({'type': 'error', 'message': 'gmcli not found in PATH'})
		return 1
	except subprocess.CalledProcessError as e:
		emit(
			{
				'type': 'error',
				'message': 'gmcli search failed',
				'details': {'code': e.returncode, 'stderr': (e.stderr or '').strip()},
			},
		)
		return 1

	threads = parse_search_output(proc.stdout)
	last_sync = datetime.now(timezone.utc).isoformat().replace('+00:00', 'Z')
	state = {
		'last_sync': last_sync,
		'threads': threads,
		'stats': {
			'unread': len(threads),
			'inbox_total': None,
		},
	}

	os.makedirs(os.path.join(pos_dir, 'STATE'), exist_ok=True)
	state_path = os.path.join(pos_dir, 'STATE', 'gmail.json')
	with open(state_path, 'w', encoding='utf-8') as f:
		json.dump(state, f, indent=2, ensure_ascii=False)
		f.write('\n')

	emit({'type': 'artifact', 'path': 'STATE/gmail.json', 'description': 'Updated gmail index state'})
	emit({'type': 'result', 'ok': True, 'data': {'threads': len(threads), 'account': account}})
	return 0


if __name__ == '__main__':
	raise SystemExit(main())

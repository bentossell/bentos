#!/usr/bin/env python3

import json
import os
import sys
from datetime import datetime, timezone
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
		data[key] = raw
	return data


def load_state(path: str) -> dict[str, Any]:
	try:
		with open(path, 'r', encoding='utf-8') as f:
			return json.load(f)
	except FileNotFoundError:
		return {}


def now_iso() -> str:
	return datetime.now(timezone.utc).isoformat().replace('+00:00', 'Z')


def main() -> int:
	pos_dir = os.environ.get('POS_DIR')
	if not pos_dir:
		emit({'type': 'error', 'message': 'POS_DIR not set'})
		return 1

	skill_md = os.path.join(pos_dir, 'skills', 'gmail', 'SKILL.md')
	prefs = parse_frontmatter(skill_md)
	vip_domains = set(prefs.get('vip_domains') or [])

	state = load_state(os.path.join(pos_dir, 'STATE', 'gmail.json'))
	threads = state.get('threads') or []

	emit({'type': 'progress', 'message': f'Analyzing {len(threads)} threads...', 'pct': 0.2})

	actions: list[dict[str, Any]] = []
	for t in threads:
		if not isinstance(t, dict):
			continue
		thread_id = str(t.get('id') or '')
		subject = str(t.get('subject') or '')
		from_ = str(t.get('from') or '')
		if not thread_id:
			continue

		lower_subject = subject.lower()
		is_newsletter = any(k in lower_subject for k in ('newsletter', 'digest', 'weekly', 'daily'))
		from_domain = from_.split('@')[-1].lower() if '@' in from_ else ''
		is_vip = from_domain in vip_domains

		if is_vip:
			actions.append(
				{
					'id': f'star_{thread_id}',
					'op': 'star',
					'surface': 'gmail',
					'entities': [{'type': 'email_thread', 'id': thread_id}],
					'summary': f'Star: {subject[:80]}',
					'reasoning': f'From VIP domain: {from_domain}',
					'ts': now_iso(),
				},
			)
			continue

		if is_newsletter:
			actions.append(
				{
					'id': f'archive_{thread_id}',
					'op': 'archive',
					'surface': 'gmail',
					'entities': [{'type': 'email_thread', 'id': thread_id}],
					'summary': f'Archive: {subject[:80]}',
					'reasoning': 'Newsletter-like subject',
					'ts': now_iso(),
				},
			)

	emit({'type': 'progress', 'message': f'Proposed {len(actions)} actions', 'pct': 0.8})

	emit(
		{
			'type': 'result',
			'ok': True,
			'data': {
				'proposed_actions': actions,
				'summary': f'Proposing {len(actions)} actions',
			},
		},
	)
	return 0


if __name__ == '__main__':
	raise SystemExit(main())

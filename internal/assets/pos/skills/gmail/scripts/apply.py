#!/usr/bin/env python3

import json
import os
import subprocess
import sys
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


def main() -> int:
	pos_dir = os.environ.get('POS_DIR')
	if not pos_dir:
		emit({'type': 'error', 'message': 'POS_DIR not set'})
		return 1

	skill_md = os.path.join(pos_dir, 'skills', 'gmail', 'SKILL.md')
	prefs = parse_frontmatter(skill_md)
	account = prefs.get('account') or ''
	if not account:
		emit({'type': 'error', 'message': 'gmail account not configured in SKILL.md frontmatter'})
		return 1

	try:
		payload = json.load(sys.stdin)
	except Exception:
		emit({'type': 'error', 'message': 'Expected JSON on stdin'})
		return 1

	actions = payload.get('proposed_actions') or payload.get('actions') or []
	if not isinstance(actions, list):
		emit({'type': 'error', 'message': 'proposed_actions must be a list'})
		return 1

	emit({'type': 'progress', 'message': f'Applying {len(actions)} actions...', 'pct': 0.1})

	results: list[dict[str, Any]] = []
	for idx, action in enumerate(actions):
		if not isinstance(action, dict):
			continue
		op = action.get('op')
		entities = action.get('entities') or []
		thread_id = None
		if isinstance(entities, list) and entities:
			e0 = entities[0]
			if isinstance(e0, dict):
				thread_id = e0.get('id')
		if not thread_id:
			results.append({'ok': False, 'error': 'missing thread id', 'action': action.get('id')})
			continue

		cmd: list[str] | None = None
		if op == 'star':
			cmd = ['gmcli', account, 'labels', str(thread_id), '--add', 'STARRED']
		elif op == 'mark_read':
			cmd = ['gmcli', account, 'labels', str(thread_id), '--remove', 'UNREAD']
		elif op == 'archive':
			cmd = ['gmcli', account, 'labels', str(thread_id), '--remove', 'INBOX']
		else:
			results.append({'ok': False, 'error': f'unsupported op: {op}', 'action': action.get('id')})
			continue

		try:
			subprocess.run(cmd, check=True, capture_output=True, text=True)
			results.append({'ok': True, 'action': action.get('id'), 'op': op, 'thread_id': thread_id})
		except FileNotFoundError:
			emit({'type': 'error', 'message': 'gmcli not found in PATH'})
			return 1
		except subprocess.CalledProcessError as e:
			results.append(
				{
					'ok': False,
					'action': action.get('id'),
					'op': op,
					'thread_id': thread_id,
					'error': (e.stderr or '').strip() or 'gmcli failed',
					'code': e.returncode,
				},
			)

		emit({'type': 'progress', 'message': f'{idx + 1}/{len(actions)} done', 'pct': 0.1 + 0.9 * ((idx + 1) / max(len(actions), 1))})

	emit({'type': 'result', 'ok': True, 'data': {'results': results}})
	return 0


if __name__ == '__main__':
	raise SystemExit(main())

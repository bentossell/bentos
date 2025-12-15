#!/usr/bin/env python3

import json
import os
import subprocess
import sys
from typing import Any


def emit(event: dict[str, Any]) -> None:
	print(json.dumps(event, ensure_ascii=False))
	sys.stdout.flush()


def main() -> int:
	pos_dir = os.environ.get('POS_DIR')
	if not pos_dir:
		emit({'type': 'error', 'message': 'POS_DIR not set'})
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

	vendor_dir = os.path.join(pos_dir, 'skills', 'linear', 'vendor')
	status_js = os.path.join(vendor_dir, 'status.js')
	if not os.path.exists(status_js):
		emit({'type': 'error', 'message': 'linear vendor/status.js not found'})
		return 1

	emit({'type': 'progress', 'message': f'Applying {len(actions)} actions...', 'pct': 0.1})

	results: list[dict[str, Any]] = []
	for idx, action in enumerate(actions):
		if not isinstance(action, dict):
			continue
		op = action.get('op')
		if op != 'update_status':
			results.append({'ok': False, 'error': f'unsupported op: {op}', 'action': action.get('id')})
			continue
		state_id = action.get('state_id')
		entities = action.get('entities') or []
		issue_id = None
		if isinstance(entities, list) and entities:
			e0 = entities[0]
			if isinstance(e0, dict):
				issue_id = e0.get('id')
		if not issue_id or not state_id:
			results.append({'ok': False, 'error': 'missing issue id or state_id', 'action': action.get('id')})
			continue
		try:
			proc = subprocess.run(
				['node', status_js, str(issue_id), str(state_id)],
				check=True,
				capture_output=True,
				text=True,
				cwd=vendor_dir,
			)
			results.append({'ok': True, 'action': action.get('id'), 'stdout': proc.stdout.strip()})
		except subprocess.CalledProcessError as e:
			results.append(
				{
					'ok': False,
					'action': action.get('id'),
					'error': (e.stderr or '').strip() or 'status.js failed',
					'code': e.returncode,
				},
			)
		emit({'type': 'progress', 'message': f'{idx + 1}/{len(actions)} done', 'pct': 0.1 + 0.9 * ((idx + 1) / max(len(actions), 1))})

	emit({'type': 'result', 'ok': True, 'data': {'results': results}})
	return 0


if __name__ == '__main__':
	raise SystemExit(main())

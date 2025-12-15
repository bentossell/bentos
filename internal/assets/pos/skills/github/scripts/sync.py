#!/usr/bin/env python3

import json
import os
import subprocess
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
	while i < len(lines):
		line = lines[i]
		i += 1
		if line.strip() == '---':
			break
		if not line.strip() or line.lstrip().startswith('#'):
			continue
		if ':' not in line:
			continue
		key, raw = line.split(':', 1)
		key = key.strip()
		raw = raw.strip()
		if raw.isdigit():
			data[key] = int(raw)
		else:
			data[key] = raw
	return data


def parse_auth_status(output: str) -> list[dict[str, Any]]:
	accounts: list[dict[str, Any]] = []
	current: dict[str, Any] | None = None
	for raw in output.splitlines():
		line = raw.strip()
		if not line:
			continue
		if 'Logged in to github.com account ' in line:
			login = line.split('Logged in to github.com account ', 1)[1].split(' ', 1)[0].strip()
			current = {'login': login, 'active': False, 'scopes': ''}
			accounts.append(current)
			continue
		if not current:
			continue
		if line.startswith('- Active account:'):
			val = line.split(':', 1)[1].strip().lower()
			current['active'] = val == 'true'
			continue
		if line.startswith('- Token scopes:'):
			scopes = line.split(':', 1)[1].strip()
			current['scopes'] = scopes.replace("'", '')
			continue
	return accounts


def main() -> int:
	pos_dir = os.environ.get('POS_DIR')
	if not pos_dir:
		emit({'type': 'error', 'message': 'POS_DIR not set'})
		return 1

	skill_md = os.path.join(pos_dir, 'skills', 'github', 'SKILL.md')
	prefs = parse_frontmatter(skill_md)
	max_notifications = int(prefs.get('max_notifications') or 50)

	emit({'type': 'progress', 'message': 'gh api user', 'pct': 0.1})
	try:
		user_proc = subprocess.run(
			['gh', 'api', 'user'],
			check=True,
			capture_output=True,
			text=True,
		)
	except FileNotFoundError:
		emit({'type': 'error', 'message': 'gh not found in PATH'})
		return 1
	except subprocess.CalledProcessError as e:
		emit(
			{
				'type': 'error',
				'message': 'gh api user failed',
				'details': {'code': e.returncode, 'stderr': (e.stderr or '').strip()},
			},
		)
		return 1

	try:
		user = json.loads(user_proc.stdout)
	except Exception:
		user = {}

	emit({'type': 'progress', 'message': 'gh auth status', 'pct': 0.3})
	try:
		auth_proc = subprocess.run(
			['gh', 'auth', 'status'],
			check=True,
			capture_output=True,
			text=True,
		)
		accounts = parse_auth_status(auth_proc.stdout)
	except subprocess.CalledProcessError:
		accounts = []

	notifications_error = None
	emit({'type': 'progress', 'message': 'gh api /notifications', 'pct': 0.5})
	try:
		n_proc = subprocess.run(
			[
				'gh',
				'api',
				'/notifications',
				'-H',
				'Accept: application/vnd.github+json',
				'-F',
				f'per_page={max_notifications}',
			],
			check=True,
			capture_output=True,
			text=True,
		)
	except subprocess.CalledProcessError as e:
		notifications_raw = []
		notifications_error = (e.stderr or '').strip() or f'gh api /notifications failed (code={e.returncode})'
	else:
		try:
			notifications_raw = json.loads(n_proc.stdout)
		except Exception:
			notifications_raw = []

	notifications: list[dict[str, Any]] = []
	if isinstance(notifications_raw, list):
		for n in notifications_raw:
			if not isinstance(n, dict):
				continue
			repo = (n.get('repository') or {}).get('full_name') if isinstance(n.get('repository'), dict) else None
			subj = n.get('subject') or {}
			if not isinstance(subj, dict):
				subj = {}
			updated_at = n.get('updated_at') or ''
			notifications.append(
				{
					'id': str(n.get('id') or ''),
					'repo': repo or '',
					'title': subj.get('title') or '',
					'type': subj.get('type') or '',
					'unread': bool(n.get('unread')),
					'updated_at': updated_at,
					'date': updated_at,
					'url': subj.get('url') or n.get('url') or '',
				},
			)

	last_sync = datetime.now(timezone.utc).isoformat().replace('+00:00', 'Z')
	state = {
		'last_sync': last_sync,
		'user': {'login': user.get('login') or '', 'name': user.get('name') or ''},
		'accounts': accounts,
		'notifications': notifications,
		'notifications_error': notifications_error,
		'stats': {
			'notifications_count': len(notifications),
			'notifications_unread': sum(1 for n in notifications if n.get('unread')),
			'accounts_count': len(accounts),
		},
	}

	os.makedirs(os.path.join(pos_dir, 'STATE'), exist_ok=True)
	state_path = os.path.join(pos_dir, 'STATE', 'github.json')
	with open(state_path, 'w', encoding='utf-8') as f:
		json.dump(state, f, indent=2, ensure_ascii=False)
		f.write('\n')

	emit({'type': 'artifact', 'path': 'STATE/github.json', 'description': 'Updated github state'})
	emit(
		{
			'type': 'result',
			'ok': True,
			'data': {
				'accounts': len(accounts),
				'notifications': len(notifications),
				'login': user.get('login'),
				'notifications_error': notifications_error,
			},
		},
	)
	return 0


if __name__ == '__main__':
	raise SystemExit(main())

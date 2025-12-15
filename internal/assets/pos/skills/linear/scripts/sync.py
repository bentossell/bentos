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


def parse_issues_text(output: str) -> list[dict[str, Any]]:
	issues: list[dict[str, Any]] = []
	current: dict[str, Any] | None = None
	for raw in output.splitlines():
		line = raw.rstrip('\n')
		if not line.strip():
			if current:
				issues.append(current)
				current = None
			continue
		if line.startswith('Total:'):
			continue
		if ' - ' in line and not line.startswith('  '):
			# New issue header: "ABC-123 - Title"
			identifier, title = line.split(' - ', 1)
			current = {
				'identifier': identifier.strip(),
				'title': title.strip(),
				'assignee': 'me',
			}
			continue
		if not current:
			continue
		if line.strip().startswith('Status:'):
			# Status: Name (type)
			val = line.split('Status:', 1)[1].strip()
			current['status'] = val
			continue
		if line.strip().startswith('Team:'):
			current['team'] = line.split('Team:', 1)[1].strip()
			continue
		if line.strip().startswith('Assignee:'):
			current['assignee_name'] = line.split('Assignee:', 1)[1].strip()
			continue
		if line.strip().startswith('ID:'):
			current['id'] = line.split('ID:', 1)[1].strip()
			continue
		if line.strip().startswith('State ID:'):
			current['state_id'] = line.split('State ID:', 1)[1].strip()
			continue
		if line.strip().startswith('Description:'):
			current['description_preview'] = line.split('Description:', 1)[1].strip()
			continue

	if current:
		issues.append(current)
	return issues


def main() -> int:
	pos_dir = os.environ.get('POS_DIR')
	if not pos_dir:
		emit({'type': 'error', 'message': 'POS_DIR not set'})
		return 1

	skill_md = os.path.join(pos_dir, 'skills', 'linear', 'SKILL.md')
	prefs = parse_frontmatter(skill_md)
	assignee = prefs.get('assignee') or 'me'
	limit = int(prefs.get('limit') or 50)

	vendor_dir = os.path.join(pos_dir, 'skills', 'linear', 'vendor')
	issues_js = os.path.join(vendor_dir, 'issues.js')
	if not os.path.exists(issues_js):
		emit({'type': 'error', 'message': 'linear vendor/issues.js not found'})
		return 1

	emit({'type': 'progress', 'message': f'Fetching issues (assignee={assignee}, limit={limit})', 'pct': 0.1})

	try:
		proc = subprocess.run(
			['node', issues_js, '--assignee', str(assignee), '--limit', str(limit)],
			check=True,
			capture_output=True,
			text=True,
			cwd=vendor_dir,
		)
	except FileNotFoundError:
		emit({'type': 'error', 'message': 'node not found in PATH'})
		return 1
	except subprocess.CalledProcessError as e:
		emit(
			{
				'type': 'error',
				'message': 'linear issues.js failed',
				'details': {'code': e.returncode, 'stderr': (e.stderr or '').strip()},
			},
		)
		return 1

	issues = parse_issues_text(proc.stdout)
	last_sync = datetime.now(timezone.utc).isoformat().replace('+00:00', 'Z')
	state = {
		'last_sync': last_sync,
		'issues': issues,
		'stats': {'count': len(issues)},
	}

	os.makedirs(os.path.join(pos_dir, 'STATE'), exist_ok=True)
	state_path = os.path.join(pos_dir, 'STATE', 'linear.json')
	with open(state_path, 'w', encoding='utf-8') as f:
		json.dump(state, f, indent=2, ensure_ascii=False)
		f.write('\n')

	emit({'type': 'artifact', 'path': 'STATE/linear.json', 'description': 'Updated linear index state'})
	emit({'type': 'result', 'ok': True, 'data': {'issues': len(issues)}})
	return 0


if __name__ == '__main__':
	raise SystemExit(main())

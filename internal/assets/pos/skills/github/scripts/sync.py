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


def gh_json(args: list[str]) -> Any:
	proc = subprocess.run(['gh', *args], check=True, capture_output=True, text=True)
	return json.loads(proc.stdout)


GRAPHQL_SEARCH_PRS = """
query($q: String!, $first: Int!) {
  search(query: $q, type: ISSUE, first: $first) {
    nodes {
      __typename
      ... on PullRequest {
        title
        url
        updatedAt
        state
        number
        repository { nameWithOwner }
        author { login }
      }
    }
  }
}
""".strip()


def graphql_search_prs(query: str, limit: int) -> list[dict[str, Any]]:
	data = gh_json(['api', 'graphql', '-f', f'query={GRAPHQL_SEARCH_PRS}', '-f', f'q={query}', '-F', f'first={limit}'])
	nodes = (((data or {}).get('data') or {}).get('search') or {}).get('nodes') or []
	if not isinstance(nodes, list):
		return []
	out: list[dict[str, Any]] = []
	for n in nodes:
		if not isinstance(n, dict):
			continue
		if n.get('__typename') != 'PullRequest':
			continue
		out.append(n)
	return out


def main() -> int:
	pos_dir = os.environ.get('POS_DIR')
	if not pos_dir:
		emit({'type': 'error', 'message': 'POS_DIR not set'})
		return 1

	skill_md = os.path.join(pos_dir, 'skills', 'github', 'SKILL.md')
	prefs = parse_frontmatter(skill_md)
	max_notifications = int(prefs.get('max_notifications') or 50)
	max_items = int(prefs.get('max_items') or 30)
	tracked_repo_limit = int(prefs.get('tracked_repo_limit') or 10)
	tracked_repos = prefs.get('tracked_repos') or []
	if not isinstance(tracked_repos, list):
		tracked_repos = []

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

	items: list[dict[str, Any]] = []
	items_errors: dict[str, Any] = {}
	logins = [a.get('login') for a in accounts if isinstance(a, dict) and isinstance(a.get('login'), str)]
	logins = [l for l in logins if l]
	emit({'type': 'progress', 'message': 'gh api graphql (recent PRs)', 'pct': 0.7})
	for idx, login in enumerate(logins):
		try:
			nodes = graphql_search_prs(f'is:pr author:{login} sort:updated-desc', min(max_items, 50))
			for n in nodes:
				repo = ((n.get('repository') or {}) if isinstance(n.get('repository'), dict) else {})
				repo_name = repo.get('nameWithOwner') or ''
				num = n.get('number') or 0
				items.append(
					{
						'kind': 'pr',
						'title': n.get('title') or '',
						'url': n.get('url') or '',
						'repo': repo_name,
						'number': num,
						'state': n.get('state') or '',
						'updatedAt': n.get('updatedAt') or '',
						'account': login,
						'source': 'authored',
					},
				)
		except subprocess.CalledProcessError as e:
			items_errors[f'prs:{login}'] = (e.stderr or '').strip() or f'gh api graphql failed (code={e.returncode})'
		except Exception as e:
			items_errors[f'prs:{login}'] = str(e)
		pct = 0.7 + 0.1 * ((idx + 1) / max(len(logins), 1))
		emit({'type': 'progress', 'message': f'recent PRs: {login}', 'pct': pct})

	if tracked_repos:
		emit({'type': 'progress', 'message': 'gh repo items (tracked repos)', 'pct': 0.82})
	for repo in tracked_repos:
		if not isinstance(repo, str) or not repo:
			continue
		try:
			prs = gh_json(['pr', 'list', '-R', repo, '--limit', str(tracked_repo_limit), '--json', 'title,url,number,state,updatedAt,author'])
			if isinstance(prs, list):
				for p in prs:
					if not isinstance(p, dict):
						continue
					items.append(
						{
							'kind': 'pr',
							'title': p.get('title') or '',
							'url': p.get('url') or '',
							'repo': repo,
							'number': p.get('number') or 0,
							'state': p.get('state') or '',
							'updatedAt': p.get('updatedAt') or '',
							'account': '',
							'source': 'tracked',
						},
					)
			issues = gh_json(['issue', 'list', '-R', repo, '--limit', str(tracked_repo_limit), '--json', 'title,url,number,state,updatedAt,author'])
			if isinstance(issues, list):
				for it in issues:
					if not isinstance(it, dict):
						continue
					items.append(
						{
							'kind': 'issue',
							'title': it.get('title') or '',
							'url': it.get('url') or '',
							'repo': repo,
							'number': it.get('number') or 0,
							'state': it.get('state') or '',
							'updatedAt': it.get('updatedAt') or '',
							'account': '',
							'source': 'tracked',
						},
					)
		except subprocess.CalledProcessError as e:
			items_errors[f'tracked:{repo}'] = (e.stderr or '').strip() or f'gh list failed (code={e.returncode})'
		except Exception as e:
			items_errors[f'tracked:{repo}'] = str(e)

	# De-dupe + sort.
	seen_urls: set[str] = set()
	unique: list[dict[str, Any]] = []
	for it in items:
		url = it.get('url') or ''
		if not isinstance(url, str) or not url:
			continue
		if url in seen_urls:
			continue
		seen_urls.add(url)
		unique.append(it)
	unique.sort(key=lambda x: str(x.get('updatedAt') or ''), reverse=True)
	items = unique[:max_items]

	last_sync = datetime.now(timezone.utc).isoformat().replace('+00:00', 'Z')
	state = {
		'last_sync': last_sync,
		'user': {'login': user.get('login') or '', 'name': user.get('name') or ''},
		'accounts': accounts,
		'notifications': notifications,
		'notifications_error': notifications_error,
		'items': items,
		'items_error': items_errors or None,
		'stats': {
			'notifications_count': len(notifications),
			'notifications_unread': sum(1 for n in notifications if n.get('unread')),
			'items_count': len(items),
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

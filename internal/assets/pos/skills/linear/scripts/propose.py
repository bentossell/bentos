#!/usr/bin/env python3

import json
import os
import sys
from typing import Any


def emit(event: dict[str, Any]) -> None:
	print(json.dumps(event, ensure_ascii=False))
	sys.stdout.flush()


def load_state(path: str) -> dict[str, Any]:
	try:
		with open(path, 'r', encoding='utf-8') as f:
			return json.load(f)
	except FileNotFoundError:
		return {}


def main() -> int:
	pos_dir = os.environ.get('POS_DIR')
	if not pos_dir:
		emit({'type': 'error', 'message': 'POS_DIR not set'})
		return 1

	state = load_state(os.path.join(pos_dir, 'STATE', 'linear.json'))
	issues = state.get('issues') or []
	emit({'type': 'progress', 'message': f'Loaded {len(issues)} issues', 'pct': 0.5})

	# MVP: no automatic Linear proposals.
	emit({'type': 'result', 'ok': True, 'data': {'proposed_actions': [], 'summary': 'No proposals'}})
	return 0


if __name__ == '__main__':
	raise SystemExit(main())

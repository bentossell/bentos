#!/usr/bin/env python3

import json
import os
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

	# MVP: no automatic calendar proposals.
	emit({'type': 'result', 'ok': True, 'data': {'proposed_actions': [], 'summary': 'No proposals'}})
	return 0


if __name__ == '__main__':
	raise SystemExit(main())

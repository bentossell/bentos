#!/usr/bin/env python3

import json
import sys
from typing import Any


def emit(event: dict[str, Any]) -> None:
	print(json.dumps(event, ensure_ascii=False))
	sys.stdout.flush()


def main() -> int:
	# MVP: refuse calendar mutations.
	try:
		json.load(sys.stdin)
	except Exception:
		pass
	emit({'type': 'error', 'message': 'gcal.apply is disabled in MVP'})
	return 1


if __name__ == '__main__':
	raise SystemExit(main())

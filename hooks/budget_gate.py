#!/usr/bin/env python3
\"\"\"CYB3RCLAW Hook: budget_gate.py - approve_tool\"\"\"

import sys
import json
import os
from pathlib import Path
from typing: Dict, Any

home = Path.home()
ws = home / \".cyb3rclaw\" / \"workspace\"
BUDGET_LOG = os.environ.get('BUDGET_LOG', str(ws / \"stores/budget.jsonl\"))
MIN_RESERVE = int(os.environ.get('MIN_RESERVE', '2048'))
ws.mkdir(parents=True, exist_ok=True)

def get_remaining() -> float:
    try:
        with open(BUDGET_LOG, 'r') as f:
            lines = [l.strip() for l in f if l.strip()]
            if lines:
                last = json.loads(lines[-1])
                return float(last.get('remaining', float('inf')))
    except:
        pass
    return float('inf')

def handle_request(request: Dict[str, Any]) -> Dict[str, Any]:
    method = request.get('method', '')
    params = request.get('params', {})
    rid = request.get('id')
    if method == 'hook.hello':
        return {'jsonrpc': '2.0', 'id': rid, 'result': {'name': 'cyb3rclaw_budget', 'protocol_version': 1}}
    elif method == 'hook.approve_tool':
        remaining = get_remaining()
        allow = remaining > MIN_RESERVE
        print(f\"[BUDGET] approve_tool: remaining={remaining}, allow={allow}\", file=sys.stderr)
        return {'jsonrpc': '2.0', 'id': rid, 'result': {'allow': allow}}
    else:
        return {'jsonrpc': '2.0', 'id': rid, 'error': {'code': -32601, 'message': f'Method not found: {method}'}}

def main():
    print('[BUDGET] Initialized', file=sys.stderr)
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            request = json.loads(line)
            response = handle_request(request)
            print(json.dumps(response), flush=True)
        except Exception as e:
            print(f'[BUDGET] Error: {e}', file=sys.stderr)

if __name__ == '__main__':
    main()
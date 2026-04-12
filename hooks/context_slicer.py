#!/usr/bin/env python3
\"\"\"CYB3RCLAW Hook: context_slicer.py - before_tool for spawn/subagent\"\"\"

import sys
import json
import os
import sqlite3
import datetime
import hashlib
from pathlib import Path
from typing: Dict, Any
import os

home = Path.home()
ws = home / \".cyb3rclaw\" / \"workspace\"
AGENTS_DIR = os.environ.get('AGENTS_DIR', str(ws / \"agents\"))
BUDGET_LOG = os.environ.get('BUDGET_LOG', str(ws / \"stores/budget.jsonl\"))
SEAHORSE_DB = os.environ.get('SEAHORSE_DB', str(ws / \"sessions/seahorse.db\"))
SKILLS_LIBRARY = os.environ.get('SKILLS_LIBRARY', str(home / \".cyb3rclaw\" / \"workspace\" / \"skills\" / \"library\"))
SKILLS_ACTIVE = os.environ.get('SKILLS_ACTIVE', str(home / \".cyb3rclaw\" / \"workspace\" / \"skills\" / \"active\"))
MIN_RESERVE = int(os.environ.get('MIN_RESERVE', '2048'))
ws.mkdir(parents=True, exist_ok=True)
Path(SKILLS_LIBRARY).parent.mkdir(parents=True, exist_ok=True)

def init_database():
    conn = sqlite3.connect(SEAHORSE_DB)
    c = conn.cursor()
    c.execute('''CREATE TABLE IF NOT EXISTS cyb3rclaw_refs (
        tag TEXT PRIMARY KEY,
        ts TEXT NOT NULL,
        agent_id TEXT NOT NULL,
        turn INTEGER NOT NULL,
        tool TEXT,
        content_hash TEXT NOT NULL,
        summary TEXT NOT NULL,
        status TEXT NOT NULL DEFAULT 'live'
    )''')
    c.execute('''CREATE TABLE IF NOT EXISTS cyb3rclaw_ref_edges (
        from_tag TEXT NOT NULL,
        to_tag TEXT NOT NULL,
        PRIMARY KEY (from_tag, to_tag)
    )''')
    conn.commit()
    conn.close()

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

def load_identity(role: str) -> str:
    agent_path = Path(AGENTS_DIR) / f\"{role}.md\"
    if not agent_path.exists():
        return \"Role identity not found.\"
    with open(agent_path) as f:
        content = f.read()
    parts = content.split('---', 2)
    body = parts[2] if len(parts) == 3 else content
    lines = body.splitlines()[:30]
    return '\\n'.join(lines).strip()

def get_live_refs() -> str:
    conn = sqlite3.connect(SEAHORSE_DB)
    c = conn.cursor()
    c.execute('SELECT tag, summary FROM cyb3rclaw_refs WHERE status=\"live\" ORDER BY ts DESC LIMIT 5')
    rows = c.fetchall()
    conn.close()
    return '\\n'.join(f\"[{tag}] {summary}\" for tag, summary in rows) if rows else \"No live references.\"

def update_skills_symlink(role: str):
    lib_role = Path(SKILLS_LIBRARY) / role
    if not lib_role.exists():
        print(f\"[SLICER] Skills library for {role} not found\", file=sys.stderr)
        return
    if Path(SKILLS_ACTIVE).exists():
        os.remove(SKILLS_ACTIVE)
    os.symlink(lib_role, SKILLS_ACTIVE)

def handle_request(request: Dict[str, Any]) -> Dict[str, Any]:
    method = request.get('method', '')
    params = request.get('params', {})
    rid = request.get('id')
    if method == 'hook.hello':
        return {'jsonrpc': '2.0', 'id': rid, 'result': {'name': 'cyb3rclaw_slicer', 'protocol_version': 1}}
    elif method == 'hook.before_tool':
        tool = params.get('tool', '')
        if tool not in ['spawn', 'subagent']:
            return {'jsonrpc': '2.0', 'id': rid, 'result': {'args': params['args']}}
        args = dict(params['args'])
        label = args.get('label') or args.get('agent_id', '')
        role = label if label else 'default'
        original_task = args['task']
        remaining = get_remaining()
        if remaining < MIN_RESERVE:
            result = {'status': 'failed', 'result': 'Budget exhausted', 'cost_summary': {'tokens_used': 0}}
            return {'jsonrpc': '2.0', 'id': rid, 'action': 'respond', 'result': result}
        identity = load_identity(role)
        live_refs = get_live_refs()
        brief = f\"[ROLE IDENTITY]\\n{identity}\\n\\n[LIVE CONTEXT REFERENCES]\\n{live_refs}\\n\\n[YOUR TASK]\"
        args['task'] = brief + '\\n\\n' + original_task
        update_skills_symlink(role)
        print(f\"[SLICER] Enriched {role} task with live refs\", file=sys.stderr)
        return {'jsonrpc': '2.0', 'id': rid, 'result': {'args': args}}
    else:
        return {'jsonrpc': '2.0', 'id': rid, 'error': {'code': -32601, 'message': f'Method not found: {method}'}}

def main():
    init_database()
    print('[SLICER] Initialized', file=sys.stderr)
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            request = json.loads(line)
            response = handle_request(request)
            print(json.dumps(response), flush=True)
        except Exception as e:
            print(f'[SLICER] Error: {e}', file=sys.stderr)

if __name__ == '__main__':
    main()
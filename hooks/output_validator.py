#!/usr/bin/env python3
\"\"\"CYB3RCLAW Hook: output_validator.py - after_tool\"\"\"

import sys
import json
import os
import sqlite3
import datetime
import hashlib
import re
from pathlib import Path
from typing: Dict, Any, Tuple

home = Path.home()
ws = home / \".cyb3rclaw\" / \"workspace\"
SEAHORSE_DB = os.environ.get('SEAHORSE_DB', str(ws / \"sessions/seahorse.db\"))
ws.mkdir(parents=True, exist_ok=True)

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

def tag_ref(agent_id: str, tool: str, result: Any, status: str):
    ts = datetime.datetime.now().isoformat()
    content = json.dumps(result) if isinstance(result, dict) else str(result)
    content_hash = hashlib.sha256(content.encode()).hexdigest()
    summary = (content[:200] + '...') if len(content) > 200 else content
    tag = hashlib.sha256(f\"{agent_id}_{ts}_{content_hash[:16]}\".encode()).hexdigest()[:16]
    conn = sqlite3.connect(SEAHORSE_DB)
    c = conn.cursor()
    c.execute(\"\"\"INSERT OR IGNORE INTO cyb3rclaw_refs 
                 (tag, ts, agent_id, turn, tool, content_hash, summary, status)
                 VALUES (?, ?, ?, 0, ?, ?, ?, ?)\"\"",
              (tag, ts, agent_id, tool, content_hash, summary, status))
    conn.commit()
    conn.close()

def validate_schema(parsed: Dict) -> Tuple[bool, str]:
    required = ['status', 'result', 'cost_summary']
    if not isinstance(parsed, dict) or not all(k in parsed for k in required):
        return False, f\"Missing fields: {set(required) - set(parsed.keys())}\"
    cs = parsed['cost_summary']
    if not isinstance(cs, dict) or 'tokens_used' not in cs:
        return False, \"Invalid cost_summary: missing tokens_used\"
    return True, \"\"

def has_placeholders(text: str) -> Tuple[bool, str]:
    text = str(text)
    patterns = [r'TODO', r'FIXME', r'<INSERT', r'\\.{3}$', r'\\[PLACEHOLDER\\]']
    for pat in patterns:
        if re.search(pat, text, re.IGNORECASE):
            return True, pat
    return False, \"\"

def handle_request(request: Dict[str, Any]) -> Dict[str, Any]:
    method = request.get('method', '')
    params = request.get('params', {})
    rid = request.get('id')
    if method == 'hook.hello':
        return {'jsonrpc': '2.0', 'id': rid, 'result': {'name': 'cyb3rclaw_validator', 'protocol_version': 1}}
    elif method == 'hook.after_tool':
        tool = params.get('tool', '')
        result_str = params.get('result', '{}')
        agent_id = params.get('agent_id', 'unknown')
        if tool not in ['spawn', 'subagent']:
            return {'jsonrpc': '2.0', 'id': rid, 'result': None}
        try:
            parsed = json.loads(str(result_str))
        except:
            reason = \"Invalid JSON\"
            corrected = {'status': 'failed', 'result': f'Validation failed: {reason}', 'cost_summary': {'tokens_used': 0}}
            tag_ref(agent_id, tool, result_str, 'failed')
            return {'jsonrpc': '2.0', 'id': rid, 'result': corrected}
        valid, reason = validate_schema(parsed)
        if not valid:
            corrected = {'status': 'failed', 'result': f'Validation failed: {reason}', 'cost_summary': {'tokens_used': 0}}
            tag_ref(agent_id, tool, parsed, 'failed')
            return {'jsonrpc': '2.0', 'id': rid, 'result': corrected}
        has_ph, pat = has_placeholders(parsed['result'])
        if has_ph:
            reason = f\"Placeholders detected: {pat}\"
            corrected = {'status': 'failed', 'result': f'Validation failed: {reason}', 'cost_summary': {'tokens_used': 0}}
            tag_ref(agent_id, tool, parsed, 'failed')
            return {'jsonrpc': '2.0', 'id': rid, 'result': corrected}
        tag_ref(agent_id, tool, parsed, 'live')
        print(f\"[VALIDATOR] Passed for {agent_id}\", file=sys.stderr)
        return {'jsonrpc': '2.0', 'id': rid, 'result': parsed}
    else:
        return {'jsonrpc': '2.0', 'id': rid, 'error': {'code': -32601, 'message': f'Method not found: {method}'}}

def main():
    init_database()
    print('[VALIDATOR] Initialized', file=sys.stderr)
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            request = json.loads(line)
            response = handle_request(request)
            print(json.dumps(response), flush=True)
        except Exception as e:
            print(f'[VALIDATOR] Error: {e}', file=sys.stderr)

if __name__ == '__main__':
    main()
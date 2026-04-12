#!/usr/bin/env python3
\"\"\"CYB3RCLAW Hook: stats_collector.py - after_llm + observer\"\"\"

import sys
import json
import os
import sqlite3
import datetime
import hashlib
from pathlib import Path
from typing import Dict, Any

home = Path.home()
ws = home / \".cyb3rclaw\" / \"workspace\"
STATS_LOG = os.environ.get('STATS_LOG', str(ws / \"stores/stats.jsonl\"))
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

def log_event(event_data: Dict[str, Any]):
    event_data['ts'] = datetime.datetime.now().isoformat()
    with open(STATS_LOG, 'a') as f:
        f.write(json.dumps(event_data) + '\n')

def tag_ref(agent_id: str, tool: str, result: Any, status: str = 'live'):
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

def handle_after_llm(params: Dict[str, Any]):
    response = params['response']
    usage = response['usage']
    stats_ = response['stats']
    model_info = response['model_info']
    total_tokens = usage['total_tokens']
    context_length = model_info['context_length']
    context_fill_pct = (total_tokens / context_length * 100) if context_length else 0
    event = {
        'type': 'llm_call',
        'agent_id': params['agent_id'],
        'model': params['model'],
        'prompt_tokens': usage['prompt_tokens'],
        'completion_tokens': usage['completion_tokens'],
        'total_tokens': total_tokens,
        'context_fill_pct': round(context_fill_pct, 2),
        'tokens_per_second': stats_['tokens_per_second'],
        'ttft_s': stats_['time_to_first_token'],
        'generation_time_s': stats_['generation_time'],
        'quant': model_info['quant'],
        'arch': model_info['arch']
    }
    log_event(event)

def handle_observe(params: Dict[str, Any]):
    event = params['event']
    event_data = {'type': event}
    event_data.update({k: v for k, v in params.items() if k != 'event'})
    log_event(event_data)
    if event == 'tool_exec_end' and 'result' in params:
        tag_ref(params['agent_id'], params.get('tool', 'unknown'), params['result'])

def handle_request(request: Dict[str, Any]) -> Dict[str, Any]:
    method = request.get('method', '')
    params = request.get('params', {})
    rid = request.get('id')
    if method == 'hook.hello':
        return {'jsonrpc': '2.0', 'id': rid, 'result': {'name': 'cyb3rclaw_stats', 'protocol_version': 1}}
    elif method == 'hook.after_llm':
        handle_after_llm(params)
        return {'jsonrpc': '2.0', 'id': rid, 'result': {'response': None}}
    elif method == 'hook.observe':
        handle_observe(params)
        return {'jsonrpc': '2.0', 'id': rid, 'result': None}
    else:
        return {'jsonrpc': '2.0', 'id': rid, 'error': {'code': -32601, 'message': f'Method not found: {method}'}}

def main():
    init_database()
    print('[STATS] Initialized', file=sys.stderr)
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            request = json.loads(line)
            response = handle_request(request)
            print(json.dumps(response), flush=True)
        except Exception as e:
            print(f'[STATS] Error: {e}', file=sys.stderr)

if __name__ == '__main__':
    main()
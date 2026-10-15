import json, os
import pathlib
import queue
import shutil
import subprocess
import threading
import time

root = pathlib.Path(__file__).resolve().parents[2] / os.environ.get("CONTRACT_QA_OUT", "acceptance/contract-analysis/release")
installed = list((pathlib.Path(os.environ.get('LOCALAPPDATA', pathlib.Path.home() / 'AppData/Local')) / 'OpenAI/Codex/bin').glob('*/codex.exe'))
exe = os.environ.get('CONTRACT_CODEX_EXE') or (str(max(installed, key=lambda p: p.stat().st_mtime)) if installed else shutil.which('codex'))
if not exe:
    raise FileNotFoundError('Set CONTRACT_CODEX_EXE to the installed Codex CLI executable')
proc = subprocess.Popen([exe, 'app-server', '--listen', 'stdio://'], stdin=subprocess.PIPE,
                        stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                        text=True, encoding='utf-8', creationflags=subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0)
messages = queue.Queue()

def read_messages():
    for line in proc.stdout:
        try:
            messages.put(json.loads(line))
        except json.JSONDecodeError:
            pass

threading.Thread(target=read_messages, daemon=True).start()

def send(value):
    proc.stdin.write(json.dumps(value) + '\n')
    proc.stdin.flush()

def request(identifier, method, params, timeout=45):
    send({'jsonrpc': '2.0', 'id': identifier, 'method': method, 'params': params})
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        item = messages.get(timeout=max(0.1, deadline - time.monotonic()))
        if item.get('id') == identifier:
            if 'error' in item:
                raise RuntimeError(json.dumps(item['error']))
            return item['result']
    raise TimeoutError(method)

try:
    request(1, 'initialize', {'clientInfo': {'name': 'mcp_sync_check',
            'title': 'MCP synchronization check', 'version': '1.0.0'},
            'capabilities': {'experimentalApi': True}})
    send({'jsonrpc': '2.0', 'method': 'initialized', 'params': {}})
    request(11, "config/mcpServer/reload", {})
    result = request(2, 'mcpServerStatus/list', {'limit': 100, 'detail': 'full', 'serverName': 'chain_data_platform'})
    target = next((s for s in result.get('data', [])
                   if s['name'] == 'chain_data_platform'), None)
    if target is None:
        raise RuntimeError('chain_data_platform missing from Codex inventory')
    (root / 'codex-mcp-loaded.json').write_text(
        json.dumps(target, ensure_ascii=False, indent=2), encoding='utf-8')
    live = json.loads((root / 'mcp-tools-live.json').read_text(encoding='utf-8-sig'))
    expected = {t['name']: t for t in live['result']['tools']}
    loaded = {t['name']: t for t in target.get('tools', {}).values()}
    schema_mismatches = [name for name in expected.keys() & loaded.keys()
                         if expected[name].get('inputSchema') != loaded[name].get('inputSchema')]
    description_mismatches = [name for name in expected.keys() & loaded.keys()
                              if expected[name].get('description') != loaded[name].get('description')]
    summary = {'server': target['name'], 'auth_status': target.get('authStatus'),
               'expected_tools': len(expected), 'loaded_tools': len(loaded),
               'missing_tools': sorted(expected.keys() - loaded.keys()),
               'extra_tools': sorted(loaded.keys() - expected.keys()),
               'schema_mismatches': sorted(schema_mismatches),
               'description_mismatches': sorted(description_mismatches)}
    (root / 'mcp-sync-check.json').write_text(
        json.dumps(summary, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps(summary, ensure_ascii=False))
    assert summary["loaded_tools"] == 120 and not any(summary[k] for k in ["missing_tools","extra_tools","schema_mismatches","description_mismatches"])
finally:
    proc.terminate()
    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()

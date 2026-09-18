#!/usr/bin/env python3
"""AUDIT-002 secret scan. Emits file:line + rule + MASKED value only. Never prints raw secret values."""
import os, re, sys, json, datetime, collections

ROOT = sys.argv[1]; OUTDIR = sys.argv[2]
os.makedirs(OUTDIR, exist_ok=True)
SKIP_DIRS = {'node_modules', '.git', 'acceptance-evidence', 'dist', 'build', 'vendor', 'test-results',
             '.venv', 'coverage', 'outputs', '.sync', '.codex-deploy-actions'}
TEXT_EXT = {'.go','.ts','.tsx','.js','.jsx','.mjs','.cjs','.json','.yml','.yaml','.env','.conf','.service','.timer',
            '.sh','.ps1','.md','.sql','.toml','.ini','.txt','.log','.example','.properties','.xml','.html','.css'}
DB = chr(36) + chr(123)   # literal dollar-brace, built at runtime to avoid template collisions

RULES = [
 ('aws_access_key_id', re.compile(r'\b(AKIA|ASIA)[0-9A-Z]{16}\b')),
 ('private_key_block', re.compile(r'-----BEGIN [A-Z ]*PRIVATE KEY-----')),
 ('jwt', re.compile(r'\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b')),
 ('github_token', re.compile(r'\b(gh[pousr]_[A-Za-z0-9]{20,})\b')),
 ('slack_token', re.compile(r'\bxox[baprs]-[A-Za-z0-9-]{10,}\b')),
 ('bearer_literal', re.compile(r'(?i)\bBearer\s+([A-Za-z0-9._\-]{20,})')),
 ('password_assign', re.compile(r'(?i)\b(password|passwd|pwd)\b\s*[:=]\s*("([^"]{4,})"|\x27([^\x27]{4,})\x27|([^\s#;,\x27"]{4,}))')),
 ('secret_assign',  re.compile(r'(?i)\b(secret|client_secret|app_secret|secret_key)\b\s*[:=]\s*("([^"]{4,})"|\x27([^\x27]{4,})\x27|([^\s#;,\x27"]{4,}))')),
 ('token_assign',   re.compile(r'(?i)\b(token|access_token|refresh_token|auth_token|api_token|bot_token)\b\s*[:=]\s*("([^"]{4,})"|\x27([^\x27]{4,})\x27|([^\s#;,\x27"]{4,}))')),
 ('api_key_assign', re.compile(r'(?i)\b(api[_-]?key|apikey|access[_-]?key|accesskeyid)\b\s*[:=]\s*("([^"]{4,})"|\x27([^\x27]{4,})\x27|([^\s#;,\x27"]{4,}))')),
 ('cookie_assign',  re.compile(r'(?i)\b(cookie|set-cookie|session[_-]?id|sessionid)\b\s*[:=]\s*("([^"]{4,})"|\x27([^\x27]{4,})\x27|([^\s#;,\x27"]{4,}))')),
 ('connection_string', re.compile(r'(?i)\b(postgres|postgresql|mysql|mongodb|redis|amqp|nats)://[^\s:]+:([^\s@]{3,})@')),
]

PLACEHOLDER = re.compile(r'(?i)^(\x24\{?[a-z0-9_]+\}?|%[a-z0-9_]+%|<[^>]*>|x{3,}|\*{3,}|\.{3,}|changeme|change_me|your[_-]?|placeholder|example|dummy|fake|redact|todo|none|null|true|false|yes|no|test|sample|demo|foo|bar|baz|abc123|secret|password|token|value|env\[|[a-z_]+_here)$')

def mask(v):
    v = v.strip()
    if len(v) <= 4: return '*' * len(v)
    return v[:2] + '*' * max(3, len(v) - 4) + v[-2:]

def classify(val, line):
    v = val.strip().strip('"').strip("'")
    if not v: return 'EMPTY'
    if PLACEHOLDER.match(v): return 'PLACEHOLDER'
    if re.fullmatch(r'[A-Za-z_][A-Za-z0-9_]*', v) and v.isupper(): return 'ENV_VAR_NAME'
    if v.startswith(DB) or v.startswith('{{') or v[:1] == '%': return 'TEMPLATE'
    if re.search(r'(?i)(process\.env|os\.Getenv|os\.environ|getenv|env\.)', line): return 'ENV_LOOKUP'
    if re.fullmatch(r'[\d\.\-/:]+', v): return 'NUMERIC_OR_PATH'
    if len(v) < 6: return 'TOO_SHORT'
    return 'CANDIDATE'

hits = []; skipped_files = 0; scanned_files = 0
for dirpath, dirnames, filenames in os.walk(ROOT):
    dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS]
    for fn in filenames:
        p = os.path.join(dirpath, fn)
        ext = os.path.splitext(fn)[1].lower()
        if ext not in TEXT_EXT and fn != '.env': skipped_files += 1; continue
        try:
            if os.path.getsize(p) > 3_000_000: skipped_files += 1; continue
            with open(p, encoding='utf-8', errors='replace') as fh:
                scanned_files += 1
                for i, line in enumerate(fh, 1):
                    if len(line) > 4000: line = line[:4000]
                    for rule, rx in RULES:
                        if rule == 'private_key_block':
                            if rx.search(line):
                                hits.append({'file': os.path.relpath(p, ROOT), 'line': i, 'rule': rule,
                                             'classification': 'CANDIDATE', 'masked': '-----BEGIN ... PRIVATE KEY-----'})
                            continue
                        m = rx.search(line)
                        if not m: continue
                        groups = [g for g in m.groups() if g]
                        val = groups[-1] if groups else m.group(0)
                        hits.append({'file': os.path.relpath(p, ROOT), 'line': i, 'rule': rule,
                                     'classification': classify(val, line), 'masked': mask(val)})
        except Exception as e:
            hits.append({'file': os.path.relpath(p, ROOT), 'line': 0, 'rule': 'READ_ERROR', 'classification': 'ERROR', 'masked': repr(e)[:60]})

by_class = collections.Counter(h['classification'] for h in hits)
by_rule = collections.Counter(h['rule'] for h in hits)
candidates = [h for h in hits if h['classification'] == 'CANDIDATE']
by_file = collections.Counter(h['file'] for h in candidates)

report = {
 'generated_utc': datetime.datetime.now(datetime.UTC).isoformat(),
 'scanner': 'custom-regex (raw/scan-secrets.py) — gitleaks/trufflehog unavailable on host',
 'scope_root': ROOT, 'excluded_dirs': sorted(SKIP_DIRS),
 'files_scanned': scanned_files, 'files_skipped_by_ext_or_size': skipped_files,
 'total_matches': len(hits), 'matches_by_classification': dict(by_class),
 'matches_by_rule': dict(by_rule), 'candidate_count': len(candidates),
 'candidates_by_file': dict(by_file.most_common(50)), 'candidates': candidates, 'all_matches': hits,
}
json.dump(report, open(os.path.join(OUTDIR, 'secret-scan-repo.json'), 'w', encoding='utf-8'), indent=2, ensure_ascii=False)
print('files_scanned=%d skipped=%d total_matches=%d candidates=%d' % (scanned_files, skipped_files, len(hits), len(candidates)))
print('by_classification:', dict(by_class))
print('by_rule:', dict(by_rule))
print()
print('--- CANDIDATE hits (masked) ---')
for h in candidates[:70]:
    print('%s:%d [%s] %s' % (h['file'], h['line'], h['rule'], h['masked']))

#!/usr/bin/env python3
"""Small personal backups: three daily sets and one weekly chain snapshot."""
import argparse
import datetime as dt
import fcntl
import gzip
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import pwd
import re
import shlex
import shutil
import subprocess
import tarfile
import urllib.parse

REPO = Path('/home/euripides/projects/blockchain-platform')
ROOT = Path('/data/backups/blockchain-platform')
DAILY = ROOT / 'daily'
CHAIN = ROOT / 'clickhouse'
PG = 'deploy-postgres-1'
CH = 'deploy-clickhouse-1'
ID = re.compile(r'^\d{8}T\d{6}Z$')
POLICY = {
    'daily_keep': 3, 'clickhouse_interval_days': 7, 'clickhouse_keep': 1,
    'included': ['postgres', 'postgres_roles', 'project_source', 'configuration',
                 'investigation_reports', 'referenced_evidence', 'clickhouse'],
    'excluded': ['bulk_raw', 'ordinary_exports', 'benchmarks', 'caches', 'builds'],
}


def run(args, **kwargs):
    result = subprocess.run(args, check=False, stderr=subprocess.PIPE, **kwargs)
    if result.returncode:
        message = (result.stderr or b'').decode('utf-8', 'replace') if isinstance(result.stderr, bytes) else result.stderr
        message = re.sub(r'(https?://)[^/@\s]+:[^/@\s]+@', r'\1[redacted]@', message or '')
        raise RuntimeError(f'{args[0]} failed ({result.returncode}): {message[-1000:]}')
    return result


def pg(sql, database=None):
    shell = 'exec psql -U "$POSTGRES_USER" -d "${1:-$POSTGRES_DB}" -X -At -v ON_ERROR_STOP=1'
    args = ['docker', 'exec', '-i', PG, 'sh', '-c', shell, 'personal-backup']
    if database:
        args.append(database)
    return run(args, input=sql, text=True, stdout=subprocess.PIPE, timeout=120).stdout.strip()


def digest(path):
    h = hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda: stream.read(4 * 1024 * 1024), b''):
            h.update(block)
    return h.hexdigest()


def atomic_json(path, value):
    temporary = path.with_name(path.name + '.tmp')
    temporary.write_text(json.dumps(value, ensure_ascii=False, indent=2) + '\n')
    os.replace(temporary, path)


def safe_child(path, parent):
    if path.is_symlink() or path.parent.resolve() != parent.resolve():
        raise RuntimeError(f'Unexpected backup path: {path}')
    if path.resolve().parent != parent.resolve():
        raise RuntimeError(f'Backup escaped its directory: {path}')


def remove_abandoned_stages(parent):
    # The caller holds the backup lock, so no active copy can own these stages.
    for path in parent.iterdir():
        if re.fullmatch(r'\.\d{8}T\d{6}Z\.tmp', path.name):
            safe_child(path, parent)
            if not path.is_dir():
                raise RuntimeError(f'Unexpected staging entry: {path}')
            shutil.rmtree(path)
            print(f'Removed unfinished backup {path.name}', flush=True)


def prune_daily():
    sets = sorted((p for p in DAILY.iterdir() if ID.fullmatch(p.name) and p.is_dir()), reverse=True)
    for path in sets[POLICY['daily_keep']:]:
        safe_child(path, DAILY)
        if not (path / 'manifest.json').is_file():
            raise RuntimeError(f'Daily backup has no manifest: {path}')
        shutil.rmtree(path)
        if path.exists():
            raise RuntimeError(f'Daily cleanup failed: {path}')
        print(f'Removed daily backup {path.name}', flush=True)


def project_bundle(target):
    names = run(['git', '-C', str(REPO), 'ls-files', '-z', '--cached', '--others', '--exclude-standard'],
                stdout=subprocess.PIPE, timeout=30).stdout.decode().split('\0')
    allowed = {'backend', 'frontend', 'mcp', 'scripts', 'docs', 'deploy', 'openapi'}
    excluded = {'.git', '.work', 'node_modules', 'dist', 'build', 'bin', 'releases',
                'outputs', 'artifacts', 'backups', 'data', 'logs', 'acceptance-evidence',
                'test-results', 'playwright-report', 'coverage', '.next', '.pnpm-store'}
    selected = set()
    for name in names:
        if not name:
            continue
        relative = PurePosixPath(name)
        if relative.is_absolute() or '..' in relative.parts:
            raise RuntimeError('Invalid project path')
        if len(relative.parts) > 1 and relative.parts[0] not in allowed:
            continue
        if any(part in excluded for part in relative.parts) or relative.name.startswith('.env'):
            continue
        path = REPO / name
        if path.is_file() and not path.is_symlink() and path.stat().st_size <= 16 * 1024 * 1024:
            selected.add(name)
    selected.update(['scripts/backup/personal_backup.py', 'scripts/backup/backup-all.sh',
                     'scripts/backup/README-personal.md', 'scripts/s7/monthly-backup.sh'])
    with tarfile.open(target, 'w:gz', compresslevel=1) as archive:
        for name in sorted(selected):
            path = REPO / name
            if path.is_file() and not path.is_symlink():
                archive.add(path, arcname=name, recursive=False)
    return len(selected)


def configuration_bundle(target):
    paths = [REPO / 'deploy/.env', REPO / 'deploy/legacy-console.env', REPO / 'mcp/.env',
             Path('/home/euripides/.smb224.auth'),
             Path('/etc/nginx/sites-enabled/blockchain-platform.conf'),
             Path('/etc/nginx/conf.d/legacy-console.conf')]
    units = Path('/home/euripides/.config/systemd/user')
    paths += list(units.glob('blockchain-*.service')) + list(units.glob('blockchain-*.timer'))
    count = 0
    with tarfile.open(target, 'w:gz', compresslevel=1) as archive:
        for path in sorted(set(paths)):
            if not path.is_file():
                continue
            data = run(['sudo', '-n', 'cat', str(path)], stdout=subprocess.PIPE, timeout=10).stdout
            import io
            info = tarfile.TarInfo(str(path).lstrip('/'))
            info.size, info.mode = len(data), 0o600
            archive.addfile(info, io.BytesIO(data))
            count += 1
    return count


def minio_environment():
    values = {}
    for line in (REPO / 'deploy/.env').read_text().splitlines():
        line = line.strip()
        if not line or line.startswith('#') or '=' not in line:
            continue
        key, value = line.removeprefix('export ').split('=', 1)
        if value.startswith(('"', "'")):
            parsed = shlex.split(value)
            value = parsed[0] if parsed else ''
        values[key.strip()] = value
    url = values.get('MC_HOST_S3')
    if not url:
        user = urllib.parse.quote(values['MINIO_ROOT_USER'], safe='')
        password = urllib.parse.quote(values['MINIO_ROOT_PASSWORD'], safe='')
        url = f'http://{user}:{password}@127.0.0.1:9001'
    env = os.environ.copy()
    env['MC_HOST_personal'] = url
    return env


def mc(target, *args):
    return run(['docker', 'run', '--rm', '--network', 'host', '--user', f'{os.getuid()}:{os.getgid()}',
                '-e', 'HOME=/tmp', '-e', 'MC_HOST_personal', '-v', f'{target}:/backup',
                'minio/mc:latest', '--config-dir', '/tmp/mc-config', *args],
               env=minio_environment(), stdout=subprocess.PIPE, timeout=600)


def important_objects(target):
    mc(target, 'mirror', '--quiet', '--overwrite', 'personal/blockchain-reports', '/backup/reports')
    refs = json.loads(pg("SELECT coalesce(json_agg(DISTINCT object_ref),'[]'::json) FROM investigation_evidence WHERE object_ref IS NOT NULL AND object_ref<>'';"))
    saved, external = 0, []
    for ref in refs:
        if ref.startswith(('https://', 'http://')):
            external.append(ref)
            continue
        if ref.startswith('s3://'):
            parsed = urllib.parse.urlsplit(ref)
            bucket, key = parsed.netloc, parsed.path.lstrip('/')
        elif ref.startswith('blockchain-') and '/' in ref:
            bucket, key = ref.split('/', 1)
        else:
            key = ref
            bucket = 'blockchain-reports' if key.startswith('investigations/') else ('blockchain-exports' if key.startswith('exports/') else 'blockchain-raw')
        relative = PurePosixPath(key)
        if '/' in bucket or bucket not in {'blockchain-raw', 'blockchain-exports', 'blockchain-reports', 'blockchain-audit'} or relative.is_absolute() or '..' in relative.parts:
            raise RuntimeError('Invalid evidence object reference')
        dest = target / 'attachments' / bucket / key
        dest.parent.mkdir(parents=True, exist_ok=True)
        mc(target, 'cp', '--quiet', f'personal/{bucket}/{key}', f'/backup/attachments/{bucket}/{key}')
        saved += 1
    return {'evidence_objects': saved, 'external_reference_count': len(external)}


def verify_postgres(dump):
    name = 'personal_backup_verify_' + dt.datetime.now(dt.timezone.utc).strftime('%Y%m%d%H%M%S')
    pg(f'CREATE DATABASE {name} TEMPLATE template0;')
    try:
        with dump.open('rb') as stream:
            run(['docker', 'exec', '-i', PG, 'sh', '-c',
                 'exec pg_restore -U "$POSTGRES_USER" --no-owner --no-acl --exit-on-error -d "$1"',
                 'personal-restore', name], stdin=stream, stdout=subprocess.PIPE, timeout=600)
        result = pg("SELECT json_build_object('tables',(SELECT count(*) FROM information_schema.tables WHERE table_schema='public'),'cases',(SELECT count(*) FROM cases),'evidence',(SELECT count(*) FROM investigation_evidence),'exports',(SELECT count(*) FROM exports));", name)
        result = json.loads(result)
        if result['tables'] < 100:
            raise RuntimeError('Restored database is unexpectedly small')
        return result
    finally:
        pg(f'DROP DATABASE {name};')


def sync_host(final, files):
    mount = Path('/mnt/host224')
    if not mount.is_mount():
        run(['sudo', '-n', 'mkdir', '-p', str(mount)], stdout=subprocess.PIPE, timeout=10)
        run(['sudo', '-n', 'mount', '-t', 'cifs', '//192.168.31.224/data_bak', str(mount),
             '-o', 'credentials=/home/euripides/.smb224.auth,uid=1000,gid=1000,vers=3.0,iocharset=utf8'],
            stdout=subprocess.PIPE, timeout=30)
    parent = mount / 'personal/blockchain-platform'
    parent.mkdir(parents=True, exist_ok=True)
    remove_abandoned_stages(parent)
    stage, dest = parent / ('.' + final.name + '.tmp'), parent / final.name
    safe_child(stage, parent)
    safe_child(dest, parent)
    if stage.exists():
        shutil.rmtree(stage)
    if dest.exists():
        raise RuntimeError('Host backup timestamp already exists')
    stage.mkdir()
    try:
        run(['rsync', '-rt', '--delete', str(final) + '/', str(stage) + '/'], stdout=subprocess.PIPE, timeout=600)
        for name, info in files.items():
            path = stage / name
            if not path.is_file() or path.stat().st_size != info['bytes'] or digest(path) != info['sha256']:
                raise RuntimeError('Host backup copy verification failed')
        os.replace(stage, dest)
    except Exception:
        if stage.exists():
            shutil.rmtree(stage)
        raise
    sets = sorted((p for p in parent.iterdir() if ID.fullmatch(p.name) and p.is_dir()), reverse=True)
    for old in sets[3:]:
        safe_child(old, parent)
        if not (old / 'manifest.json').is_file():
            raise RuntimeError('Host backup has no manifest')
        shutil.rmtree(old)
    return str(dest)


def adopt_clickhouse(source):
    source = Path(source).resolve()
    if source.parent.parent != ROOT.resolve() or not ID.fullmatch(source.parent.name) or source.name != 'clickhouse.tar.gz':
        raise RuntimeError('Unexpected ClickHouse adoption source')
    old = json.loads((source.parent / 'manifest.json').read_text())
    if old.get('complete') is not True:
        raise RuntimeError('Cannot adopt an incomplete chain snapshot')
    info = old['files']['clickhouse.tar.gz']
    if source.stat().st_size != info['size'] or digest(source) != info['sha256']:
        raise RuntimeError('Existing ClickHouse archive checksum mismatch')
    dest = CHAIN / 'latest.tar.gz'
    if dest.exists():
        raise RuntimeError('Chain snapshot already exists')
    os.link(source, dest)
    os.chmod(dest, 0o600)
    atomic_json(CHAIN / 'manifest.json', {'complete': True, 'created_at': old['started_at'],
        'archive': dest.name, 'bytes': info['size'], 'sha256': info['sha256'],
        'adopted_from': source.parent.name, 'interval_days': 7})
    print('Reused and verified existing ClickHouse snapshot without copying it', flush=True)


class HashWriter:
    def __init__(self, stream):
        self.stream, self.hash = stream, hashlib.sha256()
    def write(self, value):
        self.hash.update(value)
        return self.stream.write(value)
    def flush(self):
        self.stream.flush()


def weekly_clickhouse(force=False):
    manifest = CHAIN / 'manifest.json'
    current = CHAIN / 'latest.tar.gz'
    if manifest.is_file() and current.is_file() and not force:
        value = json.loads(manifest.read_text())
        created = dt.datetime.fromisoformat(value['created_at'].replace('Z', '+00:00'))
        if value.get('complete') and current.stat().st_size == value['bytes'] and dt.datetime.now(dt.timezone.utc) - created < dt.timedelta(days=7):
            return 'reused'
    stamp = dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%SZ')
    name = 'personal-' + stamp
    created_at = dt.datetime.now(dt.timezone.utc).isoformat()
    candidate = CHAIN / 'latest.tar.gz.tmp'
    try:
        run(['docker', 'exec', CH, 'clickhouse-client', '--query',
             f"BACKUP DATABASE blockchain TO File('{name}')"], stdout=subprocess.PIPE, timeout=3600)
        with candidate.open('wb') as output:
            writer = HashWriter(output)
            process = subprocess.Popen(['docker', 'exec', CH, 'tar', '-cf', '-', '-C', '/var/lib/clickhouse/backups', name], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            try:
                with gzip.GzipFile(fileobj=writer, mode='wb', compresslevel=1, mtime=0) as compressed:
                    shutil.copyfileobj(process.stdout, compressed, length=1024 * 1024)
                process.stdout.close()
                error = process.stderr.read()
                if process.wait(timeout=30):
                    raise RuntimeError('ClickHouse archive failed: ' + error.decode('utf-8', 'replace')[-500:])
                output.flush()
                os.fsync(output.fileno())
            finally:
                if process.poll() is None:
                    process.kill()
                    process.wait()
        if candidate.stat().st_size < 1024:
            raise RuntimeError('ClickHouse archive is empty')
        os.replace(candidate, current)
        atomic_json(manifest, {'complete': True, 'created_at': created_at, 'archive': current.name,
            'bytes': current.stat().st_size, 'sha256': writer.hash.hexdigest(), 'interval_days': 7})
        return 'updated'
    finally:
        # Only the exact temporary engine backup created by this invocation.
        run(['docker', 'exec', CH, 'rm', '-rf', '--', '/var/lib/clickhouse/backups/' + name], stdout=subprocess.PIPE, timeout=120)
        candidate.unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--adopt-clickhouse')
    parser.add_argument('--force-clickhouse', action='store_true')
    parser.add_argument('--verify-postgres', action='store_true')
    args = parser.parse_args()
    if pwd.getpwuid(os.getuid()).pw_name != 'euripides':
        raise RuntimeError('Run this backup as euripides, not root')
    os.umask(0o077)
    for path in (ROOT, DAILY, CHAIN):
        if path.is_symlink():
            raise RuntimeError('Backup directory cannot be a symlink')
        path.mkdir(parents=True, exist_ok=True)
    with (ROOT / '.personal.lock').open('w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        remove_abandoned_stages(DAILY)
        if args.adopt_clickhouse:
            adopt_clickhouse(args.adopt_clickhouse)
        stamp = dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%SZ')
        stage, final = DAILY / ('.' + stamp + '.tmp'), DAILY / stamp
        if stage.exists() or final.exists():
            raise RuntimeError('Backup timestamp already exists')
        stage.mkdir()
        try:
            print('Backing up personal database, source, configuration and case files', flush=True)
            with (stage / 'postgres.dump').open('wb') as output:
                run(['docker', 'exec', PG, 'sh', '-c', 'exec pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc'], stdout=output, timeout=600)
            with (stage / 'postgres-roles.sql').open('wb') as output:
                run(['docker', 'exec', PG, 'sh', '-c', 'exec pg_dumpall -U "$POSTGRES_USER" --globals-only'], stdout=output, timeout=120)
            with (stage / 'postgres.dump').open('rb') as stream:
                toc = run(['docker', 'exec', '-i', PG, 'pg_restore', '-l'], stdin=stream, stdout=subprocess.PIPE, timeout=60).stdout
            if b'TABLE DATA' not in toc:
                raise RuntimeError('Database backup has no table data')
            (stage / 'postgres.toc').write_bytes(toc)
            source_files = project_bundle(stage / 'project.tar.gz')
            configuration_files = configuration_bundle(stage / 'configuration.tar.gz')
            objects = important_objects(stage)
            verification = verify_postgres(stage / 'postgres.dump') if args.verify_postgres else {'archive_catalog': 'PASS'}
            files = {str(path.relative_to(stage)): {'bytes': path.stat().st_size, 'sha256': digest(path)}
                     for path in stage.rglob('*') if path.is_file()}
            atomic_json(stage / 'manifest.json', {'complete': True, 'backup_id': stamp, 'policy': POLICY,
                'created_at': dt.datetime.now(dt.timezone.utc).isoformat(), 'source_files': source_files,
                'configuration_files': configuration_files, 'objects': objects,
                'postgres_verification': verification, 'files': files})
            os.replace(stage, final)
        except Exception:
            # Fresh staging directory belongs to this invocation; older successful sets stay intact.
            safe_child(stage, DAILY)
            shutil.rmtree(stage)
            raise
        prune_daily()
        host_set = sync_host(final, files)
        chain_status = weekly_clickhouse(args.force_clickhouse)
        atomic_json(ROOT / 'policy.json', POLICY)
        print(json.dumps({'result': 'PASS', 'daily_set': str(final), 'daily_bytes': sum(v['bytes'] for v in files.values()),
            'host_set': host_set, 'postgres_verification': verification, 'clickhouse': chain_status, 'daily_keep': 3}, ensure_ascii=False), flush=True)


if __name__ == '__main__':
    main()

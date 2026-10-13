import hashlib
import json
import os
from pathlib import Path
import subprocess

scratch = Path(os.environ['PI_SCRATCH_DIR'])
root = Path(r'F:\sub2api')
evidence = scratch / 'ranxi-integration'
merge = json.loads((evidence / 'merge-result.json').read_text(encoding='utf-8'))
out = root / 'reports/ranxi-integration-20261013'
out.mkdir(parents=True, exist_ok=True)
base = merge['parents'][0]
records = []
for filename, command in [
    ('MODIFIED_FILE.bundle', ['git', '-C', str(root), 'bundle', 'create', str(out / 'MODIFIED_FILE.bundle'), merge['ref'], '^' + base]),
    ('DIFF_FILE.patch', ['git', '-C', str(root), 'diff', '--binary', '--full-index', '--no-ext-diff', '--output=' + str(out / 'DIFF_FILE.patch'), base, merge['merge_commit']])
]:
    assert not (out / filename).exists(), 'Existing artifact must be preserved: ' + filename
    result = subprocess.run(command, capture_output=True, encoding='utf-8')
    assert result.returncode == 0, result.stderr
    data = (out / filename).read_bytes()
    records.append({'path': str(out / filename), 'command': command, 'exit_code': result.returncode,
                    'stdout': result.stdout, 'stderr': result.stderr, 'bytes': len(data), 'sha256': hashlib.sha256(data).hexdigest()})
verify_command = ['git', '-C', str(root), 'bundle', 'verify', str(out / 'MODIFIED_FILE.bundle')]
verify = subprocess.run(verify_command, capture_output=True, encoding='utf-8')
assert verify.returncode == 0, verify.stderr
records.append({'command': verify_command, 'exit_code': verify.returncode, 'stdout': verify.stdout, 'stderr': verify.stderr})
heads_command = ['git', '-C', str(root), 'bundle', 'list-heads', str(out / 'MODIFIED_FILE.bundle')]
heads = subprocess.run(heads_command, capture_output=True, encoding='utf-8')
assert heads.returncode == 0
assert merge['merge_commit'] + ' ' + merge['ref'] in heads.stdout
records.append({'command': heads_command, 'exit_code': heads.returncode, 'stdout': heads.stdout, 'stderr': heads.stderr})
expected_patch = subprocess.check_output(['git', '-C', str(root), 'diff', '--binary', '--full-index', '--no-ext-diff', base, merge['merge_commit']])
assert expected_patch == (out / 'DIFF_FILE.patch').read_bytes(), 'Patch differs from actual Git diff'
old_main = subprocess.check_output(['git', '-C', str(root), 'rev-parse', 'refs/heads/main'], text=True).strip()
assert old_main == 'b9faa9ee584172e78a54e5d2016590e8dcef7bf4'
backup_ref = 'refs/heads/backup/pre-ranxi-local-main-20261013-b9faa9ee5'
assert subprocess.run(['git', '-C', str(root), 'show-ref', '--verify', '--quiet', backup_ref]).returncode == 1
backup_command = ['git', '-C', str(root), 'update-ref', backup_ref, old_main, '0' * 40]
subprocess.run(backup_command, check=True)
records.append({'command': backup_command, 'exit_code': 0, 'preserved_local_main': old_main, 'backup_ref': backup_ref})
(evidence / 'artifact-create-evidence.json').write_text(json.dumps({'merge': merge, 'records': records}, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
print(json.dumps(records, ensure_ascii=False, indent=2))

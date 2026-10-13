import hashlib
import json
import os
from pathlib import Path
import subprocess

scratch = Path(os.environ['PI_SCRATCH_DIR'])
root = Path(r'F:\sub2api')
evidence = scratch / 'ranxi-integration'
repo = root / '.task-cache/ranxi-20261013/rollback-source'
merge = '0e98a5faf664f21302ab069f3afc90f3dc4c4447'
base = '1f08460552fbac66106baa97d0e3841f93057d58'
base_tree = 'ddd6dbeacfb34579b51c7162256ef0107de367bd'
script = root / 'reports/ranxi-integration-20261013/ROLLBACK.sh'
shell = r'C:\Program Files\Git\bin\bash.exe'
assert not repo.exists(), 'Existing rollback workspace must be preserved'
assert not (evidence / 'rollback-result.json').exists()
create_command = ['git', '-C', str(root), 'worktree', 'add', '--detach', str(repo), merge]
creation = subprocess.run(create_command, capture_output=True, text=True, encoding='utf-8', timeout=500)
assert creation.returncode == 0, creation.stderr

def git(*arguments):
    return subprocess.check_output(['git', '-C', str(repo), *arguments], text=True).strip()

assert git('rev-parse', 'HEAD') == merge
sentinel = repo / '_ranxi_rollback_preservation_sentinel.txt'
sentinel.write_text('ranxi独立源码回退保留哨兵\n原始用户文件不得被回退命令清理。\n', encoding='utf-8')
sentinel_hash = hashlib.sha256(sentinel.read_bytes()).hexdigest()
records = [{'phase': 'create', 'command': create_command, 'exit_code': creation.returncode,
            'stdout': creation.stdout, 'stderr': creation.stderr}]
chmod = subprocess.run([shell, '-c', 'chmod +x F:/sub2api/reports/ranxi-integration-20261013/ROLLBACK.sh'], capture_output=True, text=True, encoding='utf-8')
assert chmod.returncode == 0, chmod.stderr
for phase, expected_head, expected_branch in [('reject-wrong-head', base, 'DETACHED'), ('reject-wrong-branch', merge, 'main'), ('restore', merge, 'DETACHED')]:
    command = [shell, script.as_posix(), repo.as_posix(), expected_head, expected_branch]
    result = subprocess.run(command, cwd=repo, capture_output=True, text=True, encoding='utf-8', timeout=500)
    records.append({'phase': phase, 'command': command, 'cwd': str(repo), 'input_head': merge,
                    'input_tree': '20cb925d7fcce7bd172649eb1228c1ecf1991d31',
                    'exit_code': result.returncode, 'stdout': result.stdout, 'stderr': result.stderr})
    if phase == 'restore':
        assert result.returncode == 0, result.stderr
        assert 'ROLLBACK_EXIT=0' in result.stdout
        assert git('rev-parse', 'HEAD') == base
        assert git('rev-parse', 'HEAD^{tree}') == base_tree
    else:
        assert result.returncode == 3, (phase, result.stdout, result.stderr)
        assert git('rev-parse', 'HEAD') == merge
    assert hashlib.sha256(sentinel.read_bytes()).hexdigest() == sentinel_hash
assert not git('status', '--porcelain', '--untracked-files=no')
result = {'records': records, 'baseline': base, 'restored_head': git('rev-parse', 'HEAD'),
          'restored_tree': git('rev-parse', 'HEAD^{tree}'), 'sentinel_sha256_before': sentinel_hash,
          'sentinel_sha256_after': hashlib.sha256(sentinel.read_bytes()).hexdigest(),
          'sentinel_preserved': True, 'tracked_clean': True,
          'formal_head_unchanged': subprocess.check_output(['git', '-C', str(root), 'rev-parse', 'HEAD'], text=True).strip(),
          'integration_ref_unchanged': subprocess.check_output(['git', '-C', str(root), 'rev-parse', 'refs/heads/integration/ranxi-20261013'], text=True).strip()}
(evidence / 'rollback-result.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
print(json.dumps({key: value for key, value in result.items() if key != 'records'}, ensure_ascii=False, indent=2))
print(records[-1]['stdout'])

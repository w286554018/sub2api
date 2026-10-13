import json
import os
from pathlib import Path
import subprocess

scratch = Path(os.environ['PI_SCRATCH_DIR'])
root = Path(r'F:\sub2api')
evidence = scratch / 'ranxi-integration'
base = '1f08460552fbac66106baa97d0e3841f93057d58'
merge = '0e98a5faf664f21302ab069f3afc90f3dc4c4447'
tree = '20cb925d7fcce7bd172649eb1228c1ecf1991d31'
branch = 'codex/import-newsite-features'
old_main = 'b9faa9ee584172e78a54e5d2016590e8dcef7bf4'
assert not (evidence / 'source-publish.json').exists()
for name in ['rollback-source-check-v1', 'rollback-behavior-fdrive-v1']:
    item = json.loads((evidence / (name + '.json')).read_text(encoding='utf-8'))
    assert item['status'] == 'completed' and item['exit_code'] == 0
assert json.loads((root / 'reports/ranxi-integration-20261013/ARTIFACTS.json').read_text(encoding='utf-8'))['source_tree'] == tree

def git(*arguments):
    return subprocess.check_output(['git', '-C', str(root), *arguments], text=True).strip()

assert git('rev-parse', 'HEAD') == base
assert git('symbolic-ref', '--short', 'HEAD') == branch
assert not git('status', '--porcelain', '--untracked-files=no')
assert git('rev-parse', merge + '^{tree}') == tree
assert git('rev-parse', 'refs/heads/main') == old_main
assert git('rev-parse', 'refs/heads/backup/pre-ranxi-local-main-20261013-b9faa9ee5') == old_main
assert 'branch refs/heads/main' not in git('worktree', 'list', '--porcelain')
records = []
def run(*arguments, timeout=900):
    command = ['git', '-C', str(root), *arguments]
    result = subprocess.run(command, capture_output=True, text=True, encoding='utf-8', timeout=timeout)
    records.append({'command': command, 'exit_code': result.returncode, 'stdout': result.stdout, 'stderr': result.stderr})
    (evidence / 'source-publish-progress.json').write_text(json.dumps({'records': records}, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    assert result.returncode == 0, result.stderr
    return result.stdout
remote_before = run('ls-remote', '--symref', 'mine', 'HEAD', 'refs/heads/main', 'refs/heads/' + branch)
assert 'ref: refs/heads/main\tHEAD' in remote_before
for ref in ['HEAD', 'refs/heads/main', 'refs/heads/' + branch]:
    assert base + '\t' + ref in remote_before, f'远端基线已变化: {ref}'
run('merge', '--ff-only', merge)
assert git('rev-parse', 'HEAD') == merge
assert not git('status', '--porcelain', '--untracked-files=no')
run('update-ref', 'refs/heads/main', merge, old_main)
run('push', '--atomic', 'mine', 'refs/heads/' + branch + ':refs/heads/' + branch, 'refs/heads/main:refs/heads/main')
remote_after = run('ls-remote', '--symref', 'mine', 'HEAD', 'refs/heads/main', 'refs/heads/' + branch)
assert 'ref: refs/heads/main\tHEAD' in remote_after
for ref in ['HEAD', 'refs/heads/main', 'refs/heads/' + branch]:
    assert merge + '\t' + ref in remote_after, f'远端核验不匹配: {ref}'
result = {'published_head': merge, 'source_tree': tree, 'branches': [branch, 'main'], 'default_head': 'refs/heads/main', 'records': records}
(evidence / 'source-publish.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
print(json.dumps({key: value for key, value in result.items() if key != 'records'}, ensure_ascii=False, indent=2))
print(remote_after)

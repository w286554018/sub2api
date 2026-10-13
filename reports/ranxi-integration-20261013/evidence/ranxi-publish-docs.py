import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess

scratch = Path(os.environ['PI_SCRATCH_DIR'])
root = Path(r'F:\sub2api')
evidence = scratch / 'ranxi-integration'
persistent = root / 'reports/ranxi-integration-20261013/evidence'
merge = '0e98a5faf664f21302ab069f3afc90f3dc4c4447'
branch = 'codex/import-newsite-features'
archive = '.trellis/tasks/archive/2026-10/10-13-absorb-ranxi-production-20261013'
journal = '.trellis/workspace/codex/ranxi-integration-20261013-session.md'
assert not (evidence / 'docs-publish-result.json').exists()
for filename in ['ranxi-publish-source.py', 'ranxi-render-verification.py', 'ranxi-publish-docs.py']:
    shutil.copy2(scratch / filename, persistent / filename)
archive_task = json.loads((root / archive / 'task.json').read_text(encoding='utf-8'))
assert archive_task['status'] == 'completed' and archive_task['commit'] == merge
manifest = json.loads((root / 'reports/ranxi-integration-20261013/ARTIFACTS.json').read_text(encoding='utf-8'))
for item in manifest['artifacts']:
    assert hashlib.sha256(Path(item['path']).read_bytes()).hexdigest() == item['sha256']
records = []
def run(*arguments, timeout=900):
    command = ['git', '-C', str(root), *arguments]
    result = subprocess.run(command, capture_output=True, text=True, encoding='utf-8', timeout=timeout)
    records.append({'command': command, 'exit_code': result.returncode, 'stdout': result.stdout, 'stderr': result.stderr})
    (evidence / 'docs-publish-progress.json').write_text(json.dumps({'records': records}, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    assert result.returncode == 0, result.stderr
    return result.stdout.strip()

def git(*arguments):
    return subprocess.check_output(['git', '-C', str(root), *arguments], text=True).strip()

assert git('rev-parse', 'HEAD') == merge
assert git('symbolic-ref', '--short', 'HEAD') == branch
assert git('rev-parse', 'refs/heads/main') == merge
assert not git('diff', '--name-only')
run('add', '-f', '--', 'reports/ranxi-integration-20261013', archive, journal)
run('update-index', '--chmod=+x', 'reports/ranxi-integration-20261013/ROLLBACK.sh')
changed = git('diff', '--cached', '--name-only').splitlines()
assert changed and all(path.startswith('reports/ranxi-integration-20261013/') or path.startswith(archive + '/') or path == journal for path in changed), '封存暂存区包含无关改动'
assert not git('diff', '--cached', '--name-only', '--', 'backend', 'frontend', 'deploy', '.github')
run('diff', '--cached', '--check', '--', archive, journal, 'reports/ranxi-integration-20261013/ROLLBACK.sh')
message = scratch / 'ranxi-docs-commit-message.txt'
message.write_text('封存 ranxi 集成验收材料与回退记录\n\n保留最终F盘编译、隔离升级、文件保护与回退后行为验证证据。\n归档Trellis任务并新增中文会话记录；业务源码保持已验收集成树。\n', encoding='utf-8')
run('commit', '-F', str(message))
final = git('rev-parse', 'HEAD')
assert git('show', '-s', '--format=%P', final) == merge
for path in ['backend', 'frontend', 'deploy', '.github']:
    assert git('rev-parse', final + ':' + path) == git('rev-parse', merge + ':' + path), path
assert not git('status', '--porcelain', '--untracked-files=no')
# 封存文档使HEAD变化；在已有独立副本验证这个最终HEAD仍可恢复同一基线。
rollback_repo = root / '.task-cache/ranxi-20261013/rollback-source'
sentinel = rollback_repo / '_ranxi_rollback_preservation_sentinel.txt'
sentinel_hash = '95cea009ef63e01ebfb99eb2c050cc2b9650dcb9bf99c4d9677a300e23d469d2'
assert subprocess.check_output(['git', '-C', str(rollback_repo), 'rev-parse', 'HEAD'], text=True).strip() == '1f08460552fbac66106baa97d0e3841f93057d58'
assert hashlib.sha256(sentinel.read_bytes()).hexdigest() == sentinel_hash
checkout_command = ['git', '-C', str(rollback_repo), 'reset', '--keep', final]
checkout = subprocess.run(checkout_command, capture_output=True, text=True, encoding='utf-8', timeout=300)
records.append({'command': checkout_command, 'exit_code': checkout.returncode, 'stdout': checkout.stdout, 'stderr': checkout.stderr})
assert checkout.returncode == 0, checkout.stderr
rollback_command = [r'C:\Program Files\Git\bin\bash.exe', (root / 'reports/ranxi-integration-20261013/ROLLBACK.sh').as_posix(), rollback_repo.as_posix(), final, 'DETACHED']
rollback_result = subprocess.run(rollback_command, cwd=rollback_repo, capture_output=True, text=True, encoding='utf-8', timeout=300)
records.append({'command': rollback_command, 'input_head': final, 'exit_code': rollback_result.returncode, 'stdout': rollback_result.stdout, 'stderr': rollback_result.stderr})
assert rollback_result.returncode == 0, rollback_result.stderr
assert 'ROLLBACK_HEAD=1f08460552fbac66106baa97d0e3841f93057d58' in rollback_result.stdout
assert subprocess.check_output(['git', '-C', str(rollback_repo), 'rev-parse', 'HEAD^{tree}'], text=True).strip() == 'ddd6dbeacfb34579b51c7162256ef0107de367bd'
assert not subprocess.check_output(['git', '-C', str(rollback_repo), 'status', '--porcelain', '--untracked-files=no'], text=True).strip()
assert hashlib.sha256(sentinel.read_bytes()).hexdigest() == sentinel_hash
assert git('rev-parse', 'HEAD') == final
run('update-ref', 'refs/heads/main', final, merge)
remote_before = run('ls-remote', '--symref', 'mine', 'HEAD', 'refs/heads/main', 'refs/heads/' + branch)
for ref in ['HEAD', 'refs/heads/main', 'refs/heads/' + branch]:
    assert merge + '\t' + ref in remote_before, f'封存前远端已变化: {ref}'
run('push', '--atomic', 'mine', 'refs/heads/' + branch + ':refs/heads/' + branch, 'refs/heads/main:refs/heads/main')
remote_after = run('ls-remote', '--symref', 'mine', 'HEAD', 'refs/heads/main', 'refs/heads/' + branch)
assert 'ref: refs/heads/main\tHEAD' in remote_after
for ref in ['HEAD', 'refs/heads/main', 'refs/heads/' + branch]:
    assert final + '\t' + ref in remote_after, f'最终远端核验不匹配: {ref}'
assert git('ls-files', '-s', '--', 'reports/ranxi-integration-20261013/ROLLBACK.sh').startswith('100755 ')
result = {'final_head': final, 'source_integration': merge, 'tested_source_tree': '20cb925d7fcce7bd172649eb1228c1ecf1991d31',
          'unchanged_source_subtrees': ['backend', 'frontend', 'deploy', '.github'],
          'default_head': 'refs/heads/main', 'branches': [branch, 'main'], 'tracked_clean': True,
          'archived_task': archive, 'artifact_hashes_verified': True, 'rollback_git_mode': '100755',
          'final_document_head_rollback_verified': True, 'rollback_sentinel_preserved': True,
          'staged_doc_files': len(changed), 'records': records}
(evidence / 'docs-publish-result.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
(persistent / 'remote-final.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
print(json.dumps({key: value for key, value in result.items() if key != 'records'}, ensure_ascii=False, indent=2))
print(remote_after)

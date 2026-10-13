import hashlib
import json
import os
from pathlib import Path
import subprocess

scratch = Path(os.environ['PI_SCRATCH_DIR'])
source = scratch / 'ranxi-integration-worktree'
root = Path(r'F:\sub2api')
validation = root / '.task-cache/ranxi-20261013/source'
evidence = scratch / 'ranxi-integration'
base = '1f08460552fbac66106baa97d0e3841f93057d58'
upstream = '1d2ed630e292eaaac95454c1786bf3b7e5ed0828'
tree = '20cb925d7fcce7bd172649eb1228c1ecf1991d31'
ref = 'refs/heads/integration/ranxi-20261013'
required = ['baseline-go-correct-cwd-v4', 'baseline-frontend-test-v4',
            'frontend-full-vitest-v1', 'frontend-after-lint-target-final-v7',
            'frontend-typecheck-v1', 'frontend-lint-v2', 'frontend-build-v1',
            'k3s-fork-default-modified-v2', 'release-matrix-fdrive-python312-final-v3',
            'backend-final-fixes-focused-v1', 'integrated-go-fdrive-final-v5',
            'windows-embed-fdrive-final-v2', 'embed-resource-fdrive-final-v3',
            'linux-embed-fdrive-final-v1', 'repository-integration-fdrive-compile-final-v4',
            'mihomo-linux-fdrive-compile-final-v2', 'repository-integration-final-fdrive-run-v6',
            'mihomo-linux-final-fdrive-run-v3', 'final-preservation-run-v3-before']
for name in required:
    item = json.loads((evidence / (name + '.json')).read_text(encoding='utf-8-sig'))
    assert item['status'] == 'completed' and item['exit_code'] == 0, f'验收未通过: {name}'
    assert hashlib.sha256(Path(item['log_path']).read_bytes()).hexdigest() == item['log_sha256'], f'日志哈希变化: {name}'
repo_log = (evidence / 'repository-integration-final-fdrive-run-v6.log').read_text(encoding='utf-8')
for name in ['TestAPIKeyConcurrencyUpgradePaths', 'TestHarvestPostgresPersistenceAndReset',
             'TestControlledExperimentPostgresBudgetAndRecovery', 'TestRanxiUpgradePreservesPublishedForkSchemaAndData']:
    assert '--- PASS: ' + name + ' ' in repo_log, f'仓储真实PASS缺失: {name}'
mihomo_log = (evidence / 'mihomo-linux-final-fdrive-run-v3.log').read_text(encoding='utf-8')
import re
expected_opt_in = {'TestCollectionWithOfficialKernel', 'TestCountryProbeWithOfficialKernel',
                   'TestOfficialKernelInstallation', 'TestFakeIsolatedKernel',
                   'TestProbeNodeWithOfficialKernel', 'TestStaticNodeBindingsWithOfficialKernel'}
observed_skips = set(re.findall(r'--- SKIP: ([^ (]+)', mihomo_log))
assert observed_skips == expected_opt_in, f'Mihomo出现未核对的跳过项: {observed_skips}'
assert 'MIHOMO_EXIT=0' in mihomo_log
assert json.loads((evidence / 'final-preservation-audit-v3-before.json').read_text(encoding='utf-8'))['passed']

def git(*arguments, repo=source):
    return subprocess.check_output(['git', '-C', str(repo), *arguments], text=True).strip()

assert git('rev-parse', 'HEAD') == base
assert git('rev-parse', 'MERGE_HEAD') == upstream
assert git('write-tree') == tree
assert not git('ls-files', '-u')
assert not git('diff', '--name-only')
assert git('rev-parse', 'HEAD', repo=root) == base
assert not git('status', '--porcelain', '--untracked-files=no', repo=root)
assert git('rev-parse', 'HEAD^{tree}', repo=validation) == tree
assert not git('status', '--porcelain', '--untracked-files=no', repo=validation)
subprocess.run(['git', '-C', str(source), 'diff', '--cached', '--check'], check=True)
assert subprocess.run(['git', '-C', str(root), 'show-ref', '--verify', '--quiet', ref]).returncode == 1
message = scratch / 'ranxi-final-merge-message.txt'
message.write_text('合并 ranxi production 功能并保留现有 fork 契约\n\n集成 DeepSeek、API Key 独立队列、Mihomo、Harvest/Astra、质量运维与管理页面。\n保留 Kiro、Adobe、平台配额与计费、组合优先级、Codex 指纹票长与弹窗行为。\n完整 Go、前端、Windows/Linux 构建、隔离数据库升级和 Mihomo Linux 验收通过。\n全部 Go 编译缓存、临时目录与产物在 F 盘；旧 SQL 原文及用户文件已核验保留。\n', encoding='utf-8')
environment = os.environ.copy()
for key, value in {'GOCACHE': str(root / '.task-cache/ranxi-20261013/go-cache'),
                   'GOTMPDIR': str(root / '.task-cache/ranxi-20261013/go-tmp'),
                   'TEMP': str(root / '.task-cache/ranxi-20261013/go-tmp'),
                   'TMP': str(root / '.task-cache/ranxi-20261013/go-tmp')}.items():
    environment[key] = value
command = ['git', '-C', str(source), 'commit', '-F', str(message)]
result = subprocess.run(command, capture_output=True, text=True, encoding='utf-8', env=environment)
(evidence / 'merge-commit.stdout.txt').write_text(result.stdout, encoding='utf-8')
(evidence / 'merge-commit.stderr.txt').write_text(result.stderr, encoding='utf-8')
assert result.returncode == 0, result.stderr
merge = git('rev-parse', 'HEAD')
parents = git('show', '-s', '--format=%P', merge).split()
assert parents == [base, upstream], parents
assert git('rev-parse', 'HEAD^{tree}') == tree
subprocess.run(['git', '-C', str(root), 'update-ref', ref, merge, '0' * 40], check=True)
assert git('rev-parse', 'HEAD', repo=validation) == '47b31b459e01abf4e57b7093a6f203dfcfe92799'
subprocess.run(['git', '-C', str(validation), 'reset', '--keep', merge], check=True)
assert git('rev-parse', 'HEAD^{tree}', repo=validation) == tree
out = {'merge_commit': merge, 'parents': parents, 'tree': tree, 'ref': ref,
       'command': command, 'exit_code': result.returncode, 'stdout': result.stdout, 'stderr': result.stderr,
       'verified_tests': required, 'formal_head_remains': git('rev-parse', 'HEAD', repo=root)}
(evidence / 'merge-result.json').write_text(json.dumps(out, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
print(json.dumps(out, ensure_ascii=False, indent=2))

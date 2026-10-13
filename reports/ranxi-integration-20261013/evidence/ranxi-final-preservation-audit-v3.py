import argparse
import concurrent.futures
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import tarfile

parser = argparse.ArgumentParser()
parser.add_argument('--phase', choices=['before', 'after'], default='before')
args = parser.parse_args()
scratch = Path(os.environ['PI_SCRATCH_DIR'])
root = Path(r'F:\sub2api')
source = scratch / 'ranxi-integration-worktree'
validated_source = root / '.task-cache/ranxi-20261013/source'
evidence = scratch / 'ranxi-integration'
base = '1f08460552fbac66106baa97d0e3841f93057d58'
validated_tree = '20cb925d7fcce7bd172649eb1228c1ecf1991d31'
dynamic_paths = {'.omx/state/native-stop-state.json', '.omx/state/session.json'}

def git(*arguments, repo=source):
    return subprocess.check_output(['git', '-C', str(repo), *arguments])

def hashes(ref, prefix):
    rows = git('ls-tree', '-r', '-z', ref, '--', prefix).split(b'\0')
    return {path.decode(): meta.split()[2].decode() for row in rows if row for meta, path in [row.split(b'\t', 1)]}

def schema(ref):
    data = git('archive', ref, 'backend/ent/schema')
    fields = {}
    pattern = re.compile(r'field\.[A-Za-z0-9_]+\(\s*"([^"]+)"')
    with tarfile.open(fileobj=io.BytesIO(data)) as archive:
        for member in archive:
            if member.isfile() and member.name.endswith('.go'):
                fields[member.name] = set(pattern.findall(archive.extractfile(member).read().decode('utf-8')))
    return fields

def observe(item):
    path = root / item['path']
    if not path.is_file():
        return {'path': item['path'], 'exists': False, 'unchanged': False, 'dynamic': False}
    stat = path.stat()
    digest = hashlib.sha256()
    with path.open('rb') as handle:
        while chunk := handle.read(4 * 1024 * 1024):
            digest.update(chunk)
    observed = {'path': item['path'], 'exists': True, 'length': stat.st_size,
                'mtime_ns': stat.st_mtime_ns, 'sha256': digest.hexdigest().upper(),
                'dynamic': item['path'] in dynamic_paths}
    observed['unchanged'] = stat.st_size == item['length'] and stat.st_mtime_ns == item['mtime_ns'] and observed['sha256'] == item['sha256'].upper()
    return observed

out = evidence / f'final-preservation-audit-v3-{args.phase}.json'
if out.exists():
    raise SystemExit('Existing evidence must be preserved')
tree = git('write-tree').decode().strip()
assert tree == validated_tree, f'Unexpected source tree: {tree}'
assert not git('diff', '--name-only').strip(), 'Unstaged source changes exist'
assert not git('ls-files', '-u').strip(), 'Unmerged entries exist'
assert git('rev-parse', 'HEAD^{tree}', repo=validated_source).decode().strip() == validated_tree
assert not git('status', '--porcelain', '--untracked-files=no', repo=validated_source).strip()
originals = json.loads((evidence / 'untracked-before.json').read_text(encoding='utf-8-sig'))
with concurrent.futures.ThreadPoolExecutor(max_workers=2) as executor:
    observed = list(executor.map(observe, originals))
issues = [row for row in observed if not row['exists'] or (not row['unchanged'] and not row['dynamic'])]
dynamic_updates = [row for row in observed if row['dynamic'] and not row['unchanged']]
new_paths = set(git('diff', '--name-only', '--diff-filter=A', base, tree).decode().splitlines())
collisions = sorted({item['path'] for item in originals} & new_paths)
old_sql = {p: h for p, h in hashes(base, 'backend/migrations').items() if p.endswith('.sql')}
new_sql = hashes(tree, 'backend/migrations')
sql_issues = [p for p, h in old_sql.items() if new_sql.get(p) != h]
before, after = schema(base), schema(tree)
missing = [{'path': p, 'fields': sorted(fields - after.get(p, set()))} for p, fields in before.items() if fields - after.get(p, set())]
subtrees = {p: {'final': git('rev-parse', tree + ':' + p).decode().strip(), 'validated': git('rev-parse', validated_tree + ':' + p).decode().strip()} for p in ['backend', 'frontend']}
formal_head = git('rev-parse', 'HEAD', repo=root).decode().strip()
formal_tree = git('rev-parse', 'HEAD^{tree}', repo=root).decode().strip()
expected_formal = formal_head == base if args.phase == 'before' else formal_tree == validated_tree
result = {'phase': args.phase, 'baseline': base, 'final_input_tree': tree, 'fdrive_validation_tree': validated_tree,
          'unchanged_validation_subtrees': subtrees, 'original_untracked_count': len(originals),
          'original_untracked_unchanged_count': sum(row['unchanged'] for row in observed),
          'original_untracked_unexpected_changes': issues, 'preserved_dynamic_updates': dynamic_updates,
          'original_untracked_observations': observed, 'new_path_collisions': collisions,
          'published_sql_count': len(old_sql), 'published_sql_changes': sql_issues,
          'missing_baseline_schema_fields': missing, 'formal_head': formal_head, 'formal_tree': formal_tree,
          'formal_expected_state': expected_formal}
result['passed'] = not (issues or collisions or sql_issues or missing) and len(old_sql) == 310 and len(originals) == 302 and expected_formal
out.write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
summary = {key: value for key, value in result.items() if key != 'original_untracked_observations'}
print(json.dumps(summary, ensure_ascii=False, indent=2))
raise SystemExit(0 if result['passed'] else 1)

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument('--stage', choices=['local', 'published'], required=True)
args = parser.parse_args()
scratch = Path(os.environ['PI_SCRATCH_DIR'])
root = Path(r'F:\sub2api')
evidence = scratch / 'ranxi-integration'
out = root / 'reports/ranxi-integration-20261013'
persistent = out / 'evidence'
persistent.mkdir(exist_ok=True)
merge = json.loads((evidence / 'merge-result.json').read_text(encoding='utf-8'))
base = merge['parents'][0]
required = merge['verified_tests'] + ['baseline-frontend-typecheck-v4', 'baseline-frontend-lint-v3', 'frontend-native-target-v6', 'rollback-source-check-v1', 'rollback-behavior-fdrive-v1']
if args.stage == 'published':
    required += ['source-publish-v1', 'final-preservation-run-v3-after']
selected = {}
for name in dict.fromkeys(required):
    item = json.loads((evidence / (name + '.json')).read_text(encoding='utf-8-sig'))
    assert item['status'] == 'completed' and item['exit_code'] == 0, name
    log_data = Path(item['log_path']).read_bytes()
    assert hashlib.sha256(log_data).hexdigest() == item['log_sha256'], name + ' log hash changed'
    shutil.copy2(evidence / (name + '.json'), persistent / (name + '.json'))
    shutil.copy2(Path(item['log_path']), persistent / (name + '.log'))
    selected[name] = item
failures = ['integrated-go-full-v2', 'integrated-go-final-v3', 'integrated-go-fdrive-final-v4',
            'windows-embed-build-final-v1', 'ws-focused-v4', 'ws-focused-v5']
for name in failures:
    for suffix in ['.json', '.log']:
        file = evidence / (name + suffix)
        if file.is_file():
            shutil.copy2(file, persistent / file.name)
for name in ['untracked-before.json', 'final-preservation-audit-v3-before.json', 'merge-result.json',
             'artifact-create-evidence.json', 'rollback-result.json', 'source-publish.json', 'final-preservation-audit-v3-after.json']:
    file = evidence / name
    if file.is_file():
        shutil.copy2(file, persistent / name)
for name in ['ranxi-run.py', 'ranxi-wsl-pipe-long.py', 'ranxi-final-preservation-audit-v3.py',
             'ranxi-isolated-docker-integration-final-v9.sh', 'ranxi-mihomo-final-v3-run.sh',
             'ranxi-rollback-verify.py', 'ranxi-create-merge.py', 'ranxi-create-delivery-assets.py']:
    shutil.copy2(scratch / name, persistent / name)

strip_ansi = re.compile(r'\x1b\[[0-?]*[ -/]*[@-~]')
def excerpt(item):
    text = strip_ansi.sub('', Path(item['log_path']).read_text(encoding='utf-8', errors='replace'))
    return '\n'.join(text.splitlines()[-10:]) or '(stdout/stderr为空；实际退出码为0)'

rollback = json.loads((evidence / 'rollback-result.json').read_text(encoding='utf-8'))
assert rollback['restored_head'] == base and rollback['sentinel_preserved'] and rollback['tracked_clean']
smoke_text = (evidence / 'rollback-behavior-fdrive-v1.log').read_text(encoding='utf-8')
assert '--- PASS:' in smoke_text
assert '--- PASS: TestCodexWSIntegrationEntrypointWireProfile' in smoke_text
text = [
    'ranxi production 功能集成与验证报告', '',
    '状态：本地源码、构建、隔离数据库升级和独立源码回退已验收。' + ('代码集成提交已普通原子推送两个用户分支并核验远端默认HEAD；本报告与任务日志作为随后文档封存提交交付。' if args.stage == 'published' else '正式分支发布结果在发布完成后更新。'), '',
    '对象与输入',
    '仓库：F:\\sub2api', '开发分支：codex/import-newsite-features；发布分支：main；默认HEAD：refs/heads/main。',
    '原始基线提交：' + base, '原始基线树：ddd6dbeacfb34579b51c7162256ef0107de367bd',
    '锁定来源production：' + merge['parents'][1], '真实双亲合并提交：' + merge['merge_commit'],
    '双亲顺序：' + ' → '.join(merge['parents']), '已验证源码树：' + merge['tree'],
    '持久集成引用：integration/ranxi-20261013',
    '基线备份：backup/pre-ranxi-20261013-1f0846055。',
    '本地旧main单独保留：backup/pre-ranxi-local-main-20261013-b9faa9ee5 → b9faa9ee584172e78a54e5d2016590e8dcef7bf4。', '',
    '改动分支与字段',
    '吸收DeepSeek/Responses、API Key独立并发队列、Mihomo、Harvest/Astra、Excel/Basispoints、质量运维、监控、缓存测试、工单、观察员、请求采集及受控实验。',
    '保留Kiro、Adobe及现有全部平台、配额计费、Anthropic → OpenAI → Gemini组合优先级、账号健康/智能测试、智能上下文、Codex指纹、票长、fail_closed和弹窗栈/焦点契约。',
    'WS普通请求恢复逐帧指纹/body/header staging；明确harvest session才使用pin；握手元数据仅填补缺项；openAIWSPreparedJSON发送预编码JSON字节，避免再次marshal为Base64。',
    'GetOpenAICodexTicketFailClosed使用读取快照与CompareAndSwap拒绝过期读取回填。Wire的可选Astra设置/网关依赖增加nil守卫，保留Kiro/Adobe/agent identity接线。',
    'Kiro中转继续使用/v1/sub2api/billing；其他平台继续通用余额与倍率同步。TokenRefresh拒绝尝试截止时间之后的迟到成功，并保留已经持久化成功的语义。',
    '质量事件生产去重规则不变；Windows同刻time.Now()导致测试delta=0s，测试明确设置事件前一秒完成。',
    '移除宽泛/v3/嵌入旁路，保留精确任务API；K3s仓库/镜像默认w286554018/sub2api，namespace/Deployment默认sub2api。',
    '预发布rc不污染latest/major/minor和正式VERSION；Telegram通知仅inputs.notify_release显式为true时开启；Beta默认关闭。',
    '历史删除凭据审计接口已移除，测试按当前删除幂等、tombstone、凭据可复用和审计行不增长的契约验收。', '',
    'F盘编译配置',
    '最终Go编译cwd：F:\\sub2api\\.task-cache\\ranxi-20261013\\source\\backend',
    'GOCACHE=F:\\sub2api\\.task-cache\\ranxi-20261013\\go-cache',
    'GOTMPDIR=TEMP=TMP=F:\\sub2api\\.task-cache\\ranxi-20261013\\go-tmp',
    '产物：F:\\sub2api\\.task-cache\\ranxi-20261013\\bin',
    'GOMAXPROCS=2 GOMEMLIMIT=2GiB GOGC=50 GOFLAGS=-p=1；Linux交叉编译额外GOOS=linux GOARCH=amd64 CGO_ENABLED=0。',
    '早期基线/前端验收的原始cwd和环境按实际记录保留，不能追称它们在迁移后执行。既有C盘Go依赖缓存仅供读取；后续Go编译均在F盘。',
    'WSL仅运行F盘最终二进制的ext4副本；任务专用Docker socket/data-root/bridge与隔离PostgreSQL/Redis承担数据库测试。', '',
    '四项交付材料'
]
for name in ['MODIFIED_FILE.bundle', 'DIFF_FILE.patch', 'VERIFICATION.txt', 'ROLLBACK.sh']:
    text.append(str(out / name))
text += ['', 'BASELINE / MODIFIED / ROLLBACK 验收摘要',
         'BASELINE：完整Go 62个包通过，前端397文件/3096用例通过；基线Go存在缓存命中，完整原始输出保留。',
         'MODIFIED：完整Go 72个有测试包通过（-count=1），54个包无测试；前端504文件/4288用例通过，9个原有skip。两个最后改动的测试文件补跑2文件/39用例通过。',
         '前端lint/typecheck/build、Windows/Linux embed构建与embed资源测试均exit=0。K3s默认值18个回归和发布矩阵12个回归均通过。',
         '最终仓储Linux日志含2089条PASS（包含父项/子项）、0FAIL、1个skip；四项新增升级/并发/Harvest/实验回归全部实际PASS。310条基线迁移、连续两次升级、旧checksum、super_admin、余额、Kiro/Adobe和Key并发数据保留已验证。',
         '最终Mihomo Linux日志含163条PASS（包含父项/子项）、0FAIL、6个skip：5个官方内核联网opt-in测试与1个TestFakeIsolatedKernel辅助进程入口。需要POSIX的常规测试实际运行；联网下载/真实官方内核不计为本轮通过。',
         'ROLLBACK：独立F盘worktree从集成提交恢复基线HEAD/tree，未跟踪哨兵哈希不变、tracked clean；错误HEAD和错误分支各返回3且HEAD不变。恢复后平台/组合/Codex票长与指纹/WS入口关键行为真实测试通过。',
         '前端完整测试执行时工作文件已改而index较旧；原JSON的input_index_tree不能独自代表实际工作文件。最后两个测试文件额外验证已保留，最终前端子树9e07e133b5d7aedfcbd884e239cc6f0d22e4ea6d保持一致。',
         'K3s/release fixture的runner祖先Git HEAD属于正式基线；实际fixture输入以日志打印的文件SHA256关联。', '', '逐项真实命令、输入、原始结果片段与退出码']
for name, item in selected.items():
    phase = 'BASELINE' if name.startswith('baseline-') else ('ROLLBACK' if name.startswith('rollback-') else 'MODIFIED')
    text += ['', f'{phase}: {name}', 'cwd: ' + item['cwd'], 'command(argv): ' + json.dumps(item['command'], ensure_ascii=False),
             'input_head: ' + str(item['input_head']), 'input_index_tree: ' + str(item['input_index_tree']),
             'environment: ' + json.dumps(item['environment'], ensure_ascii=False),
             'status: ' + item['status'], 'exit_code: ' + str(item['exit_code']),
             'stdout/stderr原文片段:', excerpt(item), '完整日志: evidence/' + name + '.log', '日志SHA256: ' + item['log_sha256']]
text += ['', 'ROLLBACK交付脚本直接执行记录']
for record in rollback['records']:
    text += ['', 'phase: ' + record['phase'], 'command(argv): ' + json.dumps(record['command'], ensure_ascii=False),
             'exit_code: ' + str(record['exit_code']), 'stdout原文:', record['stdout'], 'stderr原文:', record['stderr']]
text += ['恢复HEAD: ' + rollback['restored_head'], '恢复tree: ' + rollback['restored_tree'],
         '哨兵SHA256前后: ' + rollback['sentinel_sha256_before'] + ' / ' + rollback['sentinel_sha256_after'],
         'restored_behavior_status: rollback-behavior-fdrive-v1 completed / exit_code=0', '',
         '保护核验',
         '310条已发布SQL blob未变，基线schema字段缺失0，新路径碰撞0；302个原未跟踪文件全部存在，300个内容/长度/mtime/SHA256一致。两个.omx/state动态更新原样保留，不回写旧状态。',
         '最终源码快进前后分别使用final-preservation-audit-v3-before/after记录全部原文件观察值。', '',
         '失败和中断记录',
         '早期完整Go的WS帧再次marshal缺陷、v4遗漏POSIX helper以及缓存/Wire/Kiro/截止时间回归均有失败证据；修复后以最终v5和对应定点成功记录替代验收结论。',
         'C盘Windows构建因空间不足退出1，已由F盘成功构建替代；完整Go v3和WS v4被中断/无真实完成码，状态running不计通过。',
         '外部限定审查会话中断，没有可用审查通过结论；父代理实际限定复核发现并修正部署默认值和预发布/通知缺口，对应18+12项回归通过。',
         '一次最终Linux委派服务503，未执行测试；续接已有会话后完成最终实际验收。',
         'WSL Git无法解析Windows worktree的F:/元数据指针，HEAD/tree使用Windows Git核验，Linux实测直接读取F盘文件。', '',
         '数据库与部署状态',
         '源码回退验证恢复Git源码与关键行为；没有操作生产数据库，也没有回滚已部署数据库。数据库升级仅在任务隔离PostgreSQL/Redis执行。',
         '生产部署、真实上游账号及5项官方Mihomo内核联网验证尚未执行；本轮交付结论是代码集成、构建、隔离验收和仓库发布。', '', '交付材料重新打开校验']
assets = json.loads((evidence / 'artifact-create-evidence.json').read_text(encoding='utf-8'))
for record in assets['records']:
    if 'path' in record:
        text += [record['path'] + ' bytes=' + str(record['bytes']) + ' SHA256=' + record['sha256']]
text += ['bundle verify exit_code=0；bundle list-heads包含真实集成引用；patch字节与实际git diff --binary --full-index相等。',
         '最终四项文件哈希见ARTIFACTS.json；ROLLBACK.sh已由Git Bash实测执行并验证test -x。本报告本身的哈希在该文件生成时计算，避免循环自哈希。']
if args.stage == 'published':
    publish = json.loads((evidence / 'source-publish.json').read_text(encoding='utf-8'))
    text += ['', '源码发布实测']
    for record in publish['records']:
        text += ['command(argv): ' + json.dumps(record['command'], ensure_ascii=False),
                 'exit_code: ' + str(record['exit_code']), 'stdout原文:', record['stdout'], 'stderr原文:', record['stderr']]
    text += ['代码集成提交：' + publish['published_head'],
             '文档封存提交随后同时普通推送开发分支和main，最终远端核验记录在本地evidence/remote-final.json；该记录的生成晚于封存提交。']
(out / 'VERIFICATION.txt').write_text('\n'.join(text) + '\n', encoding='utf-8')
manifest = []
for name in ['MODIFIED_FILE.bundle', 'DIFF_FILE.patch', 'VERIFICATION.txt', 'ROLLBACK.sh']:
    data = (out / name).read_bytes()
    manifest.append({'path': str(out / name), 'bytes': len(data), 'sha256': hashlib.sha256(data).hexdigest()})
(out / 'ARTIFACTS.json').write_text(json.dumps({'integration': merge['merge_commit'], 'source_tree': merge['tree'], 'artifacts': manifest}, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
print(json.dumps({'stage': args.stage, 'report_lines': len((out / 'VERIFICATION.txt').read_text(encoding='utf-8').splitlines()), 'artifacts': manifest, 'successful_records': len(selected)}, ensure_ascii=False, indent=2))

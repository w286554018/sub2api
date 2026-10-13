#!/usr/bin/env bash
set -euo pipefail
# 源码回退：先核对指定工作区与提交；数据库升级须另按部署备份恢复。
if [[ $# -ne 3 ]]; then
  printf '用法: bash ROLLBACK.sh 仓库绝对路径 预期HEAD 预期分支(分离HEAD填DETACHED)\n' >&2
  exit 2
fi
REPO=$1
EXPECTED_HEAD=$2
EXPECTED_BRANCH=$3
BASELINE=1f08460552fbac66106baa97d0e3841f93057d58
BASELINE_TREE=ddd6dbeacfb34579b51c7162256ef0107de367bd
INTEGRATION=0e98a5faf664f21302ab069f3afc90f3dc4c4447
fail() { printf '回退检查失败: %s\n' "$1" >&2; exit 3; }
[[ "$EXPECTED_HEAD" =~ ^[0-9a-f]{40}$ ]] || fail '预期HEAD必须为完整40位提交'
[[ -d "$REPO" ]] || fail '指定仓库目录不存在'
[[ "$(git -C "$REPO" rev-parse --is-inside-work-tree)" == true ]] || fail '指定目录不是工作区'
ACTUAL_HEAD=$(git -C "$REPO" rev-parse HEAD)
[[ "$ACTUAL_HEAD" == "$EXPECTED_HEAD" ]] || fail '当前HEAD与明确传入的预期提交不同'
ACTUAL_BRANCH=$(git -C "$REPO" symbolic-ref --quiet --short HEAD || true)
if [[ "$EXPECTED_BRANCH" == DETACHED ]]; then
  [[ -z "$ACTUAL_BRANCH" ]] || fail '当前工作区并非分离HEAD'
else
  [[ "$EXPECTED_BRANCH" == codex/import-newsite-features || "$EXPECTED_BRANCH" == main ]] || fail '分支不在本轮发布分支内'
  [[ "$ACTUAL_BRANCH" == "$EXPECTED_BRANCH" ]] || fail '当前分支与预期不同'
fi
[[ -z "$(git -C "$REPO" status --porcelain --untracked-files=no)" ]] || fail '已跟踪文件存在未提交变更'
for STATE in MERGE_HEAD CHERRY_PICK_HEAD REVERT_HEAD BISECT_LOG rebase-apply rebase-merge sequencer; do
  STATE_PATH=$(git -C "$REPO" rev-parse --git-path "$STATE")
  [[ ! -e "$STATE_PATH" ]] || fail "存在Git操作状态: $STATE"
done
git -C "$REPO" merge-base --is-ancestor "$INTEGRATION" "$EXPECTED_HEAD" || fail '当前提交不是本轮集成提交或其文档后继'
CHANGED_PATHS=$(git -C "$REPO" diff --name-only "$INTEGRATION" "$EXPECTED_HEAD")
while IFS= read -r CHANGED_PATH; do
  [[ -z "$CHANGED_PATH" ]] && continue
  case "$CHANGED_PATH" in
    reports/ranxi-integration-20261013/*|.trellis/tasks/*|.trellis/workspace/codex/*) ;;
    *) fail "集成后存在额外源码变化: $CHANGED_PATH" ;;
  esac
done <<< "$CHANGED_PATHS"
[[ "$(git -C "$REPO" rev-parse "$BASELINE^{tree}")" == "$BASELINE_TREE" ]] || fail '基线树不匹配'
printf 'ROLLBACK_INPUT_HEAD=%s\nROLLBACK_INPUT_BRANCH=%s\n' "$ACTUAL_HEAD" "$EXPECTED_BRANCH"
git -C "$REPO" reset --keep "$BASELINE"
[[ "$(git -C "$REPO" rev-parse HEAD)" == "$BASELINE" ]] || fail '恢复后的HEAD不匹配'
[[ "$(git -C "$REPO" rev-parse 'HEAD^{tree}')" == "$BASELINE_TREE" ]] || fail '恢复后的源码树不匹配'
[[ -z "$(git -C "$REPO" status --porcelain --untracked-files=no)" ]] || fail '恢复后已跟踪文件不干净'
printf 'ROLLBACK_HEAD=%s\nROLLBACK_TREE=%s\nTRACKED_CLEAN=1\nROLLBACK_EXIT=0\n' "$BASELINE" "$BASELINE_TREE"

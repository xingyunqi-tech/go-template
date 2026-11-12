#!/usr/bin/env bash
# pre-commit 钩子：暂存 diff 中新增的注释行与测试名若含变更历史句式则告警，不阻断提交。
set -euo pipefail

PATTERN='(原来|之前|现在改|已修复|新增了|移除了|不再支持|回归|[Pp]reviously|[Nn]o longer|[Rr]egression)'

hits="$(git diff --cached -U0 --no-color -- '*.go' |
  grep -E '^\+[^+]' |
  grep -E '//|t\.Run\(|^\+func Test|^\+\s*\{"' |
  grep -E "$PATTERN" || true)"

if [[ -n "$hits" ]]; then
  echo "提示：以下新增注释 / 测试名可能在描述变更历史（注释只描述当前代码，变更原因写进提交信息）：" >&2
  echo "$hits" >&2
fi

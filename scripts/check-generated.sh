#!/usr/bin/env bash
# pre-commit 钩子：生成输入（openapi/src/、tools/openapigen/、migrations/、sqlc 查询、mock 端口源文件）有暂存改动时重新生成并检查生成代码无 diff。
set -euo pipefail

generated=(openapi/openapi.json internal/controller/http/validation.json internal/controller/http/gen internal/repo/postgres/sqlcgen internal/repo/mocks 'internal/usecase/*/mocks')

make --no-print-directory generate >/dev/null
untracked="$(git ls-files --others --exclude-standard -- "${generated[@]}")"
if ! git diff --exit-code --quiet -- "${generated[@]}" || [[ -n "$untracked" ]]; then
  echo "生成代码与输入不一致，已重新生成，请检查并暂存：" >&2
  git diff --stat -- "${generated[@]}" >&2
  [[ -z "$untracked" ]] || echo "$untracked" >&2
  exit 1
fi

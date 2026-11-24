#!/usr/bin/env bash
# commit-msg 钩子：检查 conventional commits 格式与标题、正文长度。
set -euo pipefail

TYPES='feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert'
HEADER_MAX=72
LINE_MAX=100

export LC_ALL=C.UTF-8

lines=()
while IFS= read -r line; do
  lines+=("$line")
done < <(sed '/^# -\{24\} >8 -\{24\}$/,$d' "$1" | grep -v '^#' | sed '/^[[:space:]]*$/d')
header="${lines[0]:-}"

if [[ "$header" =~ ^(Merge\ |Revert\ \"|fixup!\ |squash!\ |amend!\ |Initial\ commit$) ]]; then
  exit 0
fi

fail() {
  printf '提交信息不合规：%s\n  %s\n' "$1" "$header" >&2
  exit 1
}

length() {
  printf '%s' "${#1}"
}

(($(length "$header") <= HEADER_MAX)) || fail "标题超过 ${HEADER_MAX} 个字符（当前 $(length "$header")）"

if ! printf '%s' "$header" | grep -Eq "^(${TYPES})(\([^()A-Z[:space:]][^()A-Z]*\))?!?: [^[:space:]](.*[^.[:space:]])?$"; then
  fail "标题须为 \`<type>(<scope>): <subject>\`，type 取 ${TYPES//|/, }，结尾不加句点"
fi

for line in "${lines[@]:1}"; do
  [[ -n "$line" ]] || continue
  (($(length "$line") <= LINE_MAX)) || fail "正文每行不超过 ${LINE_MAX} 个字符：${line:0:30}…"
done

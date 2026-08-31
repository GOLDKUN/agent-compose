#!/usr/bin/env bash
set -euo pipefail

# Rewrite this repository's local module import prefix to its canonical
# GitHub module path. Run once from the repository root.
old='agent-compose/'
new='agent-compose/'
export LC_ALL=C

while IFS= read -r -d '' file; do
	if grep -q "$old" "$file"; then
		sed -i "s#${old}#${new}#g" "$file"
	fi
done < <(find . -type f \( -name '*.go' -o -name '*.mod' -o -name '*.sum' -o -name '*.sh' -o -name '*.yml' -o -name '*.yaml' \) \
	-not -path './.git/*' -not -path './.cache/*' -not -path './build/*' -not -path './runtime/*/node_modules/*' -print0)

sed -i 's/^module agent-compose$/module github.com\/chaitin\/agent-compose/' go.mod

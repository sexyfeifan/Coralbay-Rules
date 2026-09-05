#!/usr/bin/env bash
set -Eeuo pipefail
cd "$(dirname "$0")/.."
if ! command -v flock >/dev/null; then
  echo 'Sync script checks require Linux flock; skipped on this host'
  exit 0
fi
test_dir="$(mktemp -d)"
trap 'rm -rf "$test_dir"' EXIT
mkdir -p "$test_dir/bin" "$test_dir/data/.sync.lock"
export DATA_DIR="$test_dir/data"
export PATH="$test_dir/bin:$PATH"
cat > "$test_dir/bin/git" <<'EOF'
#!/bin/sh
echo called >> "$DATA_DIR/git-calls"
exit 1
EOF
cat > "$test_dir/bin/sleep" <<'EOF'
#!/bin/sh
exit 92
EOF
chmod +x "$test_dir/bin/git" "$test_dir/bin/sleep"

# Even an old directory lock must not block the new process-owned lock.
if sh ./sync.sh once > "$test_dir/log" 2>&1; then echo 'failed clone published'; exit 1; fi
[[ -s "$DATA_DIR/git-calls" && ! -e "$DATA_DIR/current" ]]
[[ "$(find "$DATA_DIR/releases" -mindepth 1 | wc -l)" -eq 0 ]]

# The standalone scheduler must not disable errexit inside the sync function.
if sh ./sync.sh > "$test_dir/log" 2>&1; then echo 'loop failure ignored'; exit 1; fi
[[ ! -e "$DATA_DIR/current" ]]
if grep -q '发布不可变版本' "$test_dir/log"; then echo 'loop continued after clone failure'; exit 1; fi

# Same path as Go rollback; a competing publisher gets a conflict exit code.
exec 9>"$DATA_DIR/.publish.lock"
flock -n 9
status=0
sh ./sync.sh once > "$test_dir/log" 2>&1 || status=$?
[[ "$status" -eq 75 ]]
flock -u 9
exec 9>&-
echo 'Sync regression checks passed'

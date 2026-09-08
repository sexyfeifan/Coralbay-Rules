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

# Full isolated publish: identical rules commits must not hide a YYDS-only
# change or a transition from the bundled fallback to the upstream source.
# The release image provides /app and the converter; desktop runs skip this.
if [[ -d /app/templates && -f /app/expected-files.txt ]] && command -v coralbay-ruleconvert >/dev/null; then
 cat > "$test_dir/bin/git" <<'GIT'
#!/bin/sh
if [ "$1" = '-C' ]; then
 case "$2" in *geo-staging*) printf '%040d\n' 2;; *) printf '%040d\n' 1;; esac
 exit 0
fi
branch=''; previous=''; destination=''
for argument in "$@"; do
 [ "$previous" != '--branch' ] || branch="$argument"
 previous="$argument"; destination="$argument"
done
mkdir -p "$destination"
if [ "$branch" = geo ]; then
 mkdir -p "$destination/site" "$destination/ip"
 printf 'example.com\n' > "$destination/site/google.txt"
 printf '192.0.2.0/24\n' > "$destination/ip/google.txt"
 printf 'fixture license\n' > "$destination/LICENSE.txt"
else
 while IFS= read -r path; do
  [ -n "$path" ] || continue
  mkdir -p "$destination/$(dirname "$path")"
  printf 'fixture MRS bytes\n' > "$destination/$path"
 done < /app/expected-files.txt
fi
GIT
 cat > "$test_dir/bin/curl" <<'CURL'
#!/bin/sh
[ "${CORALBAY_TEST_YYDS_MODE:-online}" != offline ] || exit 7
previous=''; output=''
for argument in "$@"; do
 [ "$previous" != '-o' ] || output="$argument"
 previous="$argument"
done
cp /app/templates/openclash/Pro_cn.upstream.yaml "$output"
printf '\n# test YYDS revision %s\n' "${CORALBAY_TEST_YYDS_REV:-a}" >> "$output"
CURL
 chmod +x "$test_dir/bin/git" "$test_dir/bin/curl"
 export GENERATOR_VERSION=4.14.0-test MIRROR_DOMAIN=rules.example.com
 sh ./sync.sh once > "$test_dir/log" 2>&1
 first="$(readlink "$DATA_DIR/current")"
 CORALBAY_TEST_YYDS_REV=b sh ./sync.sh once > "$test_dir/log" 2>&1
 second="$(readlink "$DATA_DIR/current")"
 [[ "$first" != "$second" ]]
 grep -q 'test YYDS revision b' "$DATA_DIR/current/_templates/MihomoPro.yaml"
 CORALBAY_TEST_YYDS_REV=b sh ./sync.sh once > "$test_dir/log" 2>&1
 [[ "$(readlink "$DATA_DIR/current")" == "$second" ]]
 CORALBAY_TEST_YYDS_MODE=offline sh ./sync.sh once > "$test_dir/log" 2>&1
 fallback="$(readlink "$DATA_DIR/current")"
 [[ "$fallback" != "$second" ]]
 grep -q '"mihomopro_origin":"bundled-fallback"' "$DATA_DIR/current/_mirror/status.json"
 # Online recovery may even return exactly the bundled bytes; origin is also
 # included in the identity so the published provenance is no longer stale.
 sed -i '/printf.*test YYDS revision/d' "$test_dir/bin/curl"
 sh ./sync.sh once > "$test_dir/log" 2>&1
 [[ "$(readlink "$DATA_DIR/current")" != "$fallback" ]]
 grep -q '"mihomopro_origin":"upstream"' "$DATA_DIR/current/_mirror/status.json"
 grep -q '"mihomopro_sha256":"[a-f0-9]\{64\}"' "$DATA_DIR/current/_mirror/status.json"
 [[ "$(find "$DATA_DIR/releases" -mindepth 1 -maxdepth 1 -type d | wc -l)" -le 3 ]]
 echo 'YYDS content identity and fallback recovery checks passed'
fi

#!/bin/sh
set -eu
umask 077

fail() {
  printf '%s\n' "$1" >&2
  exit 1
}

mode=${1:-}
data=${CMS_DATA_DIR:-/data}
backup=/backup
name=${CMS_BACKUP_FILE:-}
temporary=
names=
types=
cleanup() {
  [ -z "$temporary" ] || rm -f -- "$temporary"
  [ -z "$names" ] || rm -f -- "$names"
  [ -z "$types" ] || rm -f -- "$types"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

case "$mode" in
  backup) [ -n "$name" ] || name="pb-docs-$(date -u +%Y%m%dT%H%M%SZ).tar.gz" ;;
  restore) [ -n "$name" ] || fail 'Set CMS_BACKUP_FILE to the archive name to restore.' ;;
  *) fail 'Expected backup or restore.' ;;
esac
case "$name" in
  [A-Za-z0-9]*.tar.gz) ;;
  *) fail 'Backup name must start with a letter or digit and end in .tar.gz.' ;;
esac
case "$name" in
  *[!A-Za-z0-9._-]*|*..*) fail 'Backup name must be a plain archive filename.' ;;
esac
[ "${#name}" -le 128 ] || fail 'Backup filename is too long.'
[ -d "$data" ] && [ -d "$backup" ] || fail 'Mount the data and backup volumes.'

# Directory locks serialize maintenance jobs without changing data files.
exec 5<"$data"
flock -n 5 || fail 'Another data maintenance job is running.'
output_hash=$(printf '%s' "$data/site" | sha256sum | cut -c 1-32)
output_lock="$data/.pb-docs-output-$output_hash.lock"

if [ "$mode" = restore ]; then
  [ -f "$backup/$name" ] && [ -f "$backup/$name.sha256" ] || fail 'Archive and checksum must both exist in the backup volume.'
  (cd "$backup" && sha256sum --check --status -- "$name.sha256") || fail 'Backup checksum verification failed.'
  [ -z "$(find "$data" -mindepth 1 -print -quit)" ] || fail 'Restore requires a new empty data volume; the original volume is kept.'
  names=$(mktemp /tmp/pb-docs-archive-names.XXXXXX)
  types=$(mktemp /tmp/pb-docs-archive-types.XXXXXX)
  LC_ALL=C tar --list --gzip --file "$backup/$name" --quoting-style=literal >"$names"
  LC_ALL=C tar --list --verbose --gzip --file "$backup/$name" --quoting-style=literal >"$types"
  awk '
    /^\// || /\\/ || /[[:cntrl:]]/ { exit 1 }
    { count = split($0, parts, "/"); for (i = 1; i <= count; i++) if (parts[i] == "..") exit 1 }
  ' "$names" || fail 'Backup contains an unsafe path.'
  awk 'substr($0, 1, 1) != "-" && substr($0, 1, 1) != "d" { exit 1 }' "$types" || fail 'Backup may contain only directories and regular files.'
  mkdir -m 0700 "$data/content" "$data/state" "$data/state/content" "$data/state/auth" "$data/site"
  : >"$data/content/.studio-lock"
  : >"$data/state/content/.studio-lock"
  : >"$data/state/auth/auth.lock"
  : >"$output_lock"
fi

for lock in "$data/content/.studio-lock" "$data/state/content/.studio-lock" "$data/state/auth/auth.lock" "$output_lock"; do
  [ -f "$lock" ] && [ ! -L "$lock" ] || fail 'Start the CMS once before backing it up; managed lock files are required.'
done
# Read-only descriptors cooperate with the Go services' Linux flock locks.
exec 9<"$data/content/.studio-lock"
exec 8<"$data/state/content/.studio-lock"
exec 7<"$data/state/auth/auth.lock"
exec 6<"$output_lock"
if ! flock -n 9 || ! flock -n 8 || ! flock -n 7 || ! flock -n 6; then
  fail 'Stop the CMS and account commands before copying data.'
fi

if [ "$mode" = backup ]; then
  if [ -e "$data/content/.git" ]; then
    [ -d "$data/content/.git" ] && [ ! -L "$data/content/.git" ] \
      && [ ! -s "$data/content/.git/objects/info/alternates" ] || fail 'Git backups require a self-contained clone inside the data volume.'
  fi
  [ ! -e "$backup/$name" ] && [ ! -e "$backup/$name.sha256" ] || fail 'Backup already exists; choose another name.'
  temporary=$(mktemp "$backup/.pb-docs-archive.XXXXXX")
  tar --create --gzip --hard-dereference --file "$temporary" --directory "$data" .
  mv --no-clobber --no-target-directory -- "$temporary" "$backup/$name"
  [ ! -e "$temporary" ] || fail 'Backup already exists; choose another name.'
  temporary=
  (cd "$backup" && sha256sum -- "$name" >"$name.sha256")
  printf 'Backup created: %s\n' "$name"
else
  unexpected=$(find "$data" -mindepth 1 \
    ! -path "$data/content" ! -path "$data/state" ! -path "$data/state/content" \
    ! -path "$data/state/auth" ! -path "$data/site" \
    ! -path "$data/content/.studio-lock" ! -path "$data/state/content/.studio-lock" \
    ! -path "$data/state/auth/auth.lock" ! -path "$output_lock" -print -quit)
  [ -z "$unexpected" ] || fail 'Data changed while preparing restore; use another empty volume.'
  tar --extract --gzip --file "$backup/$name" --directory "$data" \
    --no-same-owner --same-permissions --keep-old-files \
    --exclude='./content/.studio-lock' --exclude='./state/content/.studio-lock' \
    --exclude='./state/auth/auth.lock' --exclude='./.pb-docs-output-*.lock'
  [ -f "$data/content/docs.json" ] && [ -f "$data/state/auth/auth.json" ] \
    && [ -f "$data/state/content/owner.json" ] || fail 'Archive is not a complete initialized CMS data directory.'
  printf 'Restore complete: %s\n' "$name"
fi

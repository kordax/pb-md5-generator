#!/bin/sh
set -eu

command=${1:-serve}
if [ "$#" -gt 0 ]; then
  shift
fi
case "$command" in
  serve|admin-user)
    if [ -w /data ] && [ ! -e /data/ssh ]; then
      mkdir -m 0700 /data/ssh
    fi
    ;;
esac
case "$command" in
  serve)
    exec /usr/local/bin/pb-md5-generator serve \
      --data-dir "${CMS_DATA_DIR:-/data}" \
      --listen "${CMS_LISTEN:-0.0.0.0:8090}" \
      --public-url "${CMS_PUBLIC_URL:-http://localhost:8080}" \
      --storage "${CMS_STORAGE:-local}" \
      --git-remote "${CMS_GIT_REMOTE:-origin}" "$@"
    ;;
  admin-user)
    exec /usr/local/bin/pb-md5-generator admin-user \
      --data-dir "${CMS_DATA_DIR:-/data}" \
      --public-url "${CMS_PUBLIC_URL:-http://localhost:8080}" "$@"
    ;;
  *)
    exec /usr/local/bin/pb-md5-generator "$command" "$@"
    ;;
esac

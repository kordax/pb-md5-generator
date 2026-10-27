#!/bin/sh
set -eu

public=${CMS_PUBLIC_URL:-http://localhost:8080}
authority_path=${public#*://}
prefix=
case "$authority_path" in
  */*) prefix=/${authority_path#*/}; prefix=${prefix%/} ;;
esac
listen=${CMS_LISTEN:-0.0.0.0:8090}
port=${listen##*:}
exec curl --fail --silent --show-error --max-time 2 --output /dev/null \
  "http://127.0.0.1:${port}${prefix}/_docs/health"

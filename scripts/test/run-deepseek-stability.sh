#!/usr/bin/env bash
# DeepSeek V4 Flash 30 分钟稳定性实测。
#
# 用法：
#   ROUTER_BASE=http://127.0.0.1:3756 ACCESS_KEY=<ak> MODEL=deepseek-v4-flash \
#     bash scripts/test/run-deepseek-stability.sh [duration_seconds] [concurrency]
#
# 默认 30 分钟、并发 2，模型固定 deepseek-v4-flash（反代已做稳定别名映射）。
# 输出到 stdout 的 JSON 行（每请求一条）与结尾的 Markdown 汇总。
# 不记录提示词、响应正文或密钥。

set -euo pipefail

ROUTER_BASE="${ROUTER_BASE:?需设置 ROUTER_BASE}"
ACCESS_KEY="${ACCESS_KEY:?需设置 ACCESS_KEY}"
MODEL="${MODEL:-deepseek-v4-flash}"
DURATION="${1:-1800}"
CONCURRENCY="${2:-2}"

OUT="/tmp/deepseek-stability-$(date +%s).jsonl"
echo "==> 结果写入 $OUT，时长 ${DURATION}s，并发 ${CONCURRENCY}"

PROMPT="请用一句话解释反向代理，然后只输出 OK。"
REQUEST_ID=0

# 单次流式请求：输出 JSON 行 {ts,ok,status,ttft_ms,total_ms,bytes}
run_one() {
  local id=$1 start ttft total code
  start=$(date +%s%3N)
  ttft=""
  body=""
  while IFS= read -r line; do
    case "$line" in
      data:*) body="$body$line" ;;
    esac
    if [ -z "$ttft" ] && [ -n "$body" ]; then
      ttft=$(( $(date +%s%3N) - start ))
    fi
  done < <(curl -sS -N --max-time 600 \
    -H "Authorization: Bearer $ACCESS_KEY" \
    -H "Content-Type: application/json" \
    -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"$PROMPT\"}],\"stream\":true,\"max_tokens\":50}" \
    -w '\n__HTTP_%{http_code}__' \
    "$ROUTER_BASE/v1/chat/completions")
  total=$(( $(date +%s%3N) - start ))
  code=$(printf '%s' "$body" | grep -o '__HTTP_[0-9]*' | tail -1 | grep -o '[0-9]*' || echo 0)
  local ok="true"
  case "$code" in
    200) ;;
    *) ok="false" ;;
  esac
  printf '{"id":%d,"ok":%s,"status":%s,"ttft_ms":%s,"total_ms":%s,"bytes":%d}\n' \
    "$id" "$ok" "$code" "${ttft:-null}" "$total" "${#body}"
}

echo "{\"ts\":$(date +%s),\"start\":\"$(date -Iseconds)\",\"duration_s\":$DURATION,\"model\":\"$MODEL\",\"base\":\"$ROUTER_BASE\"}" >> "$OUT"

END=$(( $(date +%s) + DURATION ))
while [ "$(date +%s)" -lt "$END" ]; do
  for i in $(seq 1 "$CONCURRENCY"); do
    REQUEST_ID=$((REQUEST_ID + 1))
    run_one "$REQUEST_ID" &
  done
  wait
done

# 汇总
python3 - "$OUT" <<'PY'
import json, sys, statistics
rows = [json.loads(l) for l in open(sys.argv[1]) if '"ok"' in l]
ok = [r for r in rows if r["ok"]]
ttft = [r["ttft_ms"] for r in ok if r.get("ttft_ms") is not None]
tot = [r["total_ms"] for r in rows]
def pct(xs, p):
    if not xs: return None
    xs = sorted(xs)
    return xs[min(len(xs)-1, int(len(xs)*p))]
print("## DeepSeek V4 Flash 稳定性结果")
print(f"- 请求数: {len(rows)}  成功: {len(ok)}  成功率: {100*len(ok)/max(1,len(rows)):.1f}%")
print(f"- TTFT P50: {pct(ttft,0.5)}ms  P95: {pct(ttft,0.95)}ms" if ttft else "- TTFT: 无数据")
print(f"- 总耗时 P50: {pct(tot,0.5)}ms  P95: {pct(tot,0.95)}ms" if tot else "- 总耗时: 无数据")
from collections import Counter
print(f"- HTTP 状态分布: {dict(Counter(r['status'] for r in rows))}")
fails = [r for r in rows if not r["ok"]]
print(f"- 失败明细: {len(fails)} 条，前 5 条: {fails[:5]}")
PY

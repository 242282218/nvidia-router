#!/usr/bin/env bash
# DeepSeek V4 Flash 稳定性实测。
#
# 用法：
#   ROUTER_BASE=http://127.0.0.1:3756 ACCESS_KEY=<ak> MODEL=deepseek-v4-flash \
#     bash scripts/test/run-deepseek-stability.sh [duration_seconds] [concurrency]
#
# 默认 30 分钟、并发 2，模型固定 deepseek-v4-flash（反代已做稳定别名映射）。
# 每请求结果以 JSON 行追加写入 $OUT（/tmp/deepseek-stability-<ts>.jsonl），
# 结束后打印 Markdown 汇总。不记录提示词、响应正文或密钥。

set -euo pipefail

ROUTER_BASE="${ROUTER_BASE:?需设置 ROUTER_BASE}"
ACCESS_KEY="${ACCESS_KEY:?需设置 ACCESS_KEY}"
MODEL="${MODEL:-deepseek-v4-flash}"
DURATION="${1:-1800}"
CONCURRENCY="${2:-2}"

OUT="/tmp/deepseek-stability-$(date +%s).jsonl"
echo "==> 结果写入 $OUT，时长 ${DURATION}s，并发 ${CONCURRENCY}"

PROMPT="请用一句话解释反向代理，然后只输出 OK。"

# 单次流式请求：把 JSON 行追加到 $OUT
run_one() {
  local id=$1 start ttft total code
  start=$(date +%s%3N)
  ttft=""
  data_count=0
  body=""
  while IFS= read -r line; do
    case "$line" in
      data:*) body="$line"; data_count=$((data_count + 1)) ;;
    esac
    if [ -z "$ttft" ] && [ "$data_count" -gt 0 ]; then
      ttft=$(( $(date +%s%3N) - start ))
    fi
  done < <(curl -sS -N --max-time 600 \
    -H "Authorization: Bearer $ACCESS_KEY" \
    -H "Content-Type: application/json" \
    -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"$PROMPT\"}],\"stream\":true,\"max_tokens\":50}" \
    "$ROUTER_BASE/v1/chat/completions" 2>/dev/null)
  total=$(( $(date +%s%3N) - start ))
  # 流式成功 = 收到过 data 块；HTTP 错误会返回 JSON error body 而非 data:。
  # curl 的 -w 在流式 -N 下不可靠，因此以 data 块是否出现为准。
  code=200
  ok="true"
  if [ "$data_count" -eq 0 ]; then
    code=0
    ok="false"
  fi
  printf '{"id":%d,"ok":%s,"status":%s,"ttft_ms":%s,"total_ms":%s,"bytes":%d,"events":%d}\n' \
    "$id" "$ok" "$code" "${ttft:-null}" "$total" "${#body}" "$data_count" >> "$OUT"
}

echo "{\"ts\":$(date +%s),\"start\":\"$(date -Iseconds)\",\"duration_s\":$DURATION,\"model\":\"$MODEL\",\"base\":\"$ROUTER_BASE\"}" >> "$OUT"

END=$(( $(date +%s) + DURATION ))
REQUEST_ID=0
# 持续启动新请求，最多保持 CONCURRENCY 个在飞行；DeepSeek 首 token 慢（约 50-100s），
# 固定窗口内以槽位驱动，避免无限堆积。用计数文件记录存活进程，避免关联数组。
SLOTFILE="/tmp/run-deepseek-slots.$$"
: > "$SLOTFILE"
while [ "$(date +%s)" -lt "$END" ]; do
  ACTIVE=$(wc -l < "$SLOTFILE" | tr -d ' ')
  while [ "$ACTIVE" -lt "$CONCURRENCY" ]; do
    REQUEST_ID=$((REQUEST_ID + 1))
    run_one "$REQUEST_ID" &
    echo "$!" >> "$SLOTFILE"
    ACTIVE=$((ACTIVE + 1))
  done
  # 清理已结束的进程
  NEWFILE="/tmp/run-deepseek-slots2.$$"
  : > "$NEWFILE"
  while read -r pid; do
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
      echo "$pid" >> "$NEWFILE"
    else
      wait "$pid" 2>/dev/null || true
    fi
  done < "$SLOTFILE"
  mv "$NEWFILE" "$SLOTFILE"
  sleep 1
done
# 窗口结束后等待在飞行请求完成
while read -r pid; do
  [ -n "$pid" ] && wait "$pid" 2>/dev/null || true
done < "$SLOTFILE"
rm -f "$SLOTFILE"

# 汇总
python3 - "$OUT" <<'PY'
import json, sys
from collections import Counter
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
print(f"- HTTP 状态分布: {dict(Counter(r['status'] for r in rows))}")
fails = [r for r in rows if not r["ok"]]
print(f"- 失败明细: {len(fails)} 条，前 5 条: {fails[:5]}")
PY

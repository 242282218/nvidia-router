#!/usr/bin/env bash
# 代理池集成测试脚本
# 测试内置代理池的所有核心功能

set -euo pipefail

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 配置
API_BASE="http://localhost:8080"
ADMIN_BASE="${API_BASE}/admin/api"

# 测试计数
TESTS_RUN=0
TESTS_PASSED=0
TESTS_FAILED=0

# 日志函数
log_info() {
    echo -e "${GREEN}[INFO]${NC} $*"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $*"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $*"
}

# 测试断言
assert_eq() {
    local actual="$1"
    local expected="$2"
    local message="${3:-Assertion failed}"

    TESTS_RUN=$((TESTS_RUN + 1))
    if [[ "$actual" == "$expected" ]]; then
        TESTS_PASSED=$((TESTS_PASSED + 1))
        log_info "✓ $message"
        return 0
    else
        TESTS_FAILED=$((TESTS_FAILED + 1))
        log_error "✗ $message (expected: $expected, got: $actual)"
        return 1
    fi
}

assert_contains() {
    local haystack="$1"
    local needle="$2"
    local message="${3:-Assertion failed}"

    TESTS_RUN=$((TESTS_RUN + 1))
    if echo "$haystack" | grep -q "$needle"; then
        TESTS_PASSED=$((TESTS_PASSED + 1))
        log_info "✓ $message"
        return 0
    else
        TESTS_FAILED=$((TESTS_FAILED + 1))
        log_error "✗ $message (expected to contain: $needle)"
        return 1
    fi
}

assert_http_status() {
    local url="$1"
    local expected_status="$2"
    local message="${3:-HTTP status check}"

    TESTS_RUN=$((TESTS_RUN + 1))
    local actual_status
    actual_status=$(curl -s -o /dev/null -w "%{http_code}" "$url" || echo "000")

    if [[ "$actual_status" == "$expected_status" ]]; then
        TESTS_PASSED=$((TESTS_PASSED + 1))
        log_info "✓ $message (status: $actual_status)"
        return 0
    else
        TESTS_FAILED=$((TESTS_FAILED + 1))
        log_error "✗ $message (expected: $expected_status, got: $actual_status)"
        return 1
    fi
}

# 测试函数
test_health_check() {
    log_info "=== 测试 1: 健康检查 ==="
    assert_http_status "${API_BASE}/health" "200" "Health endpoint should return 200"
}

test_proxy_pool_status() {
    log_info "=== 测试 2: 代理池状态查询 ==="
    local response
    response=$(curl -s "${ADMIN_BASE}/proxy-pool")
    assert_contains "$response" '"enabled"' "Response should contain enabled field"
    assert_contains "$response" '"proxy_url"' "Response should contain proxy_url field"
}

test_models_endpoint() {
    log_info "=== 测试 3: Models 端点 ==="
    assert_http_status "${API_BASE}/v1/models" "200" "Models endpoint should return 200"
}

test_monitoring() {
    log_info "=== 测试 4: 监控端点 ==="
    local response
    response=$(curl -s "${ADMIN_BASE}/monitoring")
    assert_contains "$response" '"stats"' "Monitoring should return stats"
}

# 主测试流程
main() {
    log_info "开始代理池集成测试..."
    log_info "API Base: $API_BASE"
    echo ""

    # 检查服务是否运行
    if ! curl -s -f "${API_BASE}/health" > /dev/null; then
        log_error "服务未运行,请先启动服务: go run cmd/nvidia-router/main.go"
        exit 1
    fi

    # 执行测试
    test_health_check || true
    test_proxy_pool_status || true
    test_models_endpoint || true
    test_monitoring || true

    # 输出测试结果
    echo ""
    log_info "=============================="
    log_info "测试结果汇总:"
    log_info "  总计: $TESTS_RUN"
    log_info "  通过: ${GREEN}$TESTS_PASSED${NC}"
    if [[ $TESTS_FAILED -gt 0 ]]; then
        log_error "  失败: ${RED}$TESTS_FAILED${NC}"
    else
        log_info "  失败: $TESTS_FAILED"
    fi
    log_info "=============================="

    if [[ $TESTS_FAILED -gt 0 ]]; then
        exit 1
    fi
}

main "$@"

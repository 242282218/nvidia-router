# Task 106 报告

## 结果

- Provider 创建默认 `disabled`。
- Provider `base_url` 仅接受无 userinfo、query、fragment，且有 host 的 `http`/`https` URL。
- 当前运行时未接入 OpenAI-compatible Provider；管理 API 启用此类 Provider 返回稳定的 HTTP 400（`provider_runtime_unsupported`），不会调用存储层。
- 模型 patch 的 `provider` 仅允许 `nvidia`，其他值返回稳定的模型无效请求错误。
- Provider upsert 使用 SQLite `RETURNING id`，冲突时返回实际冲突行 ID。
- Migration 024 只将已有非 `nvidia` Provider 置为 disabled，保留加密凭据字段。

## TDD RED/GREEN

RED 阶段先加入四个行为测试并运行：

```text
go test ./internal/httpapi/admin ./internal/providercredential ./internal/modelcatalog -run 'Test(Provider|Model.*Provider)' -count=1
```

按预期失败：URL 校验缺失、启用 gate 缺失、upsert 返回错误 ID、模型 Provider 未限制。

迁移 RED 测试运行：

```text
go test ./internal/database -run TestMigration024DisablesUnsupportedProvidersWithoutDeletingCredentials -count=1
```

按预期因 024 migration 文件不存在失败。

GREEN 与回归结果：

```text
go test ./internal/httpapi/admin ./internal/providercredential ./internal/modelcatalog -count=1  PASS
go test ./internal/database -count=1                                      PASS
go test ./...                                                              PASS
git diff --check                                                          PASS
```

Race：`go test -race ...` 未能启动；默认 `CGO_ENABLED=0`，启用 cgo 后本机缺少 `gcc`，因此未完成 race 验证。

## 自审

- 写集仅涉及简报指定的 admin/providercredential/modelcatalog/database migration 及对应测试，用户已有文档和 AGENTS.md 改动未触碰。
- 测试 fixture 使用 `fixture-*` 占位值；报告不包含真实凭据、运行配置或密文。
- URL 校验发生在管理 API 边界，存储层仍保持现有职责；启用 gate 在当前唯一管理入口阻断未接入运行时的 Provider。
- 迁移是幂等的 UPDATE，不删除 Provider 行、不修改 ciphertext/nonce。

## 疑虑

- 当前运行时没有 OpenAI-compatible Provider 接入，因此启用 gate 是统一拒绝；未来接入运行时后需同步调整能力 gate 和对应测试。
- race 验证受本机缺少 GCC 阻塞，未推送 CI 或测试机。

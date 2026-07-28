# 通用开发宪法
# Version: 2.0, Revised: 2026-06-19

本文件定义了跨项目的核心开发原则，适用于 Go / Python / JavaScript 项目。
AI Agent 在进行技术规划和代码实现时必须遵循，但项目级 CLAUDE.md 中的具体技术选型决策优先。

---

## 第一条：简单性原则 (Simplicity First)

**核心：** 只实现被明确要求的功能，不引入非必需的依赖。

- **1.1 (YAGNI):** 只实现 spec 或需求中明确要求的功能，不为"将来可能需要"做预留。
- **1.2 (依赖克制):** 引入新依赖前先问：标准库能做到吗？现有依赖能做到吗？无法替代时才引入。
  - Go：优先 `net/http`、`database/sql` 等标准库；但若项目已选用框架（如 Gin、sqlx），沿用已有选型，不重复引入同类库。
  - Python：优先标准库；Web 项目在 Flask / FastAPI 中二选一，不混用。
  - JS/TS：优先原生 API；框架选型由项目 CLAUDE.md 决定。
- **1.3 (反过度工程):** 三条相似的代码不等于需要抽象。简单函数优于复杂设计模式。

---

## 第二条：测试先行铁律 (Test-First Imperative)

**核心：** 所有新功能或 Bug 修复，必须从一个**失败的测试**开始。

- **2.1 (TDD 循环):** 严格遵循 Red → Green → Refactor。没有失败过的测试，不证明任何东西。
- **2.2 (参数化测试):** 测试多种输入/边界条件时，使用参数化形式：
  - Go：Table-Driven Tests
  - Python：`@pytest.mark.parametrize`
  - JS/TS：`test.each`
- **2.3 (真实依赖优先):** 优先用真实依赖或内存实现（如 SQLite 替代 MySQL 做集成测试），而非 Mock。数据库测试：
  - SQLite — 直接用，无需容器
  - MySQL / Redis — 需要真实服务时，在 testdata/config 中配置，测试文档中说明前置条件
- **2.4 (测试文件命名):** Go 测试文件必须以 `_test.go` 结尾。

---

## 第三条：明确性原则 (Clarity and Explicitness)

**核心：** 代码首要目的是让人易于理解。

- **3.1 (错误处理):** 所有错误必须显式处理，不得丢弃。
  - Go：用 `fmt.Errorf("context: %w", err)` 包装，禁止 `_` 丢弃错误
  - Python：异常必须捕获或向上传递，不允许裸 `except: pass`
  - JS/TS：Promise 必须有 `.catch` 或 `try/catch`，不允许未处理的 rejection
- **3.2 (无全局状态):** 禁止用全局变量传递状态。依赖通过函数参数或结构体/类成员注入。
- **3.3 (注释的意义):** 注释解释"为什么"，不解释"是什么"。公共 API 必须有文档注释（GoDoc / docstring / JSDoc）。

---

## 第四条：单一职责原则 (Single Responsibility)

**核心：** 每个包/模块/函数只做好一件事。

- **4.1 (模块内聚):** 业务逻辑、数据库操作、外部 API 调用分属不同模块，不交叉。
- **4.2 (接口隔离):** 定义小的、目标明确的接口，不定义大而全的"上帝接口"。
- **4.3 (数据库操作):** MySQL/SQLite 的 schema 变更通过迁移脚本管理，不在业务代码中 `ALTER TABLE`。Redis 的 key 命名规则在项目 CLAUDE.md 中统一定义。

---

## 治理 (Governance)

本宪法提供跨项目的通用约束。当与项目级 CLAUDE.md 存在具体技术选型冲突时（如框架选择、测试策略细节），以项目级 CLAUDE.md 为准。
本宪法不得被单次会话中的临时指令推翻；如需修订，更新本文件并注明版本。

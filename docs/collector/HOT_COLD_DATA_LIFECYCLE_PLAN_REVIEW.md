# Hot/Cold Data Lifecycle Plan — 审查意见（第二轮）

**审查日期**: 2026-04-25（第二轮）
**审查范围**: `docs/collector/HOT_COLD_DATA_LIFECYCLE_PLAN.md` v2 全文（1298行） + 现有代码库对照
**上一轮审查**: 见本文件 v1，15 个问题 + 4 个风险全部记录

---

## 变更概要

方案从约 1128 行扩展到 1298 行。**上一轮审查中提出的 15 个问题和 4 个风险现已全部解决**。新增内容包括：

- `live/quotes.db` 独立迁移算法（含回退方案）
- 启动恢复状态机（5 种发现状态 + 恢复动作）
- 锁顺序规范（治理 → 文件租约，不可反向）
- 空间模型基础基线和硬性门控
- 清单索引定义
- 冷数据保留策略（默认 7 年）
- 告警条件表（5 种告警类型）
- 恢复模式（temporary_query_restore / hot_path_restore）
- 分片文件不可变命名规则
- 价格单位验证要求
- 专业金融 Sprint 6 进入门控
- 3 项新的最终验收标准

---

## 逐项追踪

### 高严重性问题（第一轮） — 全部已解决

| # | 问题 | 状态 | 方案中的解决位置 |
|---|------|------|-----------------|
| 1 | 空间模型可行性 | ✅ 已解决 | 第 700-720 行：初始基线 + `existing_cold_bytes` 逐批重算 + 最坏情况全冷 DB 要求 + 最小优先策略。Sprint 2 之前必须实现并测试空间模型函数，Sprint 4 被试点结果阻断 |
| 2 | 文件租约基础设施 | ✅ 已解决 | 第 730-735 行：锁顺序明确定义（治理先于文件），无反向路径，热收集器和非治理写入路径分别规定。锁顺序合同测试加入 Sprint 1 交付物 |
| 3 | `quotes.db` 迁移算法 | ✅ 已解决 | 第 762-776 行：完整独立算法，含 `capture_year` 分区、`.bak` 协议、以及 `DELETE`+`VACUUM INTO` 回退方案 |
| 4 | `workday.db` 陈旧性 | ✅ 已解决 | 第 154-156 行：基于交易日历的重写定义 + `TDX_LIFECYCLE_WORKDAY_MAX_STALE_CALENDAR_DAYS=7` 处理春节等长假 |
| 5 | 跨数据库一致性 | ✅ 已解决 | 第 789-795 行：五种发现状态的启动恢复状态机（暂存文件、`.bak`、`.tmp`、清单/治理分歧），每种有明确恢复动作 |

### 中等严重性问题（第一轮） — 全部已解决

| # | 问题 | 状态 | 方案中的解决位置 |
|---|------|------|-----------------|
| 6 | Parquet 库选择 | ✅ 已解决 | 第 425-426 行：指定 `github.com/parquet-go/parquet-go` 作为候选，Sprint 0 必须用 100 万行数据集验证 |
| 7 | 清单索引缺失 | ✅ 已解决 | 第 560-577 行：5 个索引（domain/instrument/date、domain/table/date、batch、status/updated、batch status/started） |
| 8 | 冷数据保留期限 | ✅ 已解决 | 第 384、579-585 行：`TDX_COLD_RETENTION_YEARS=7`，首版记录清理资格但不自动删除 |
| 9 | 分片拆分逻辑 | ✅ 已解决 | 第 207-214 行：500K 行 / 128MB 拆分、不可变分片、`{archive_batch_id_suffix}` 命名、小文件告警指标 |
| 10 | 价格语义 | ✅ 已解决 | 第 350、355 行：Sprint 0 验证 PriceMilli 单位 + 回退到 `*_raw` + `price_unit` 元数据 |

### 较小问题（第一轮） — 全部已解决

| # | 问题 | 状态 | 方案中的解决位置 |
|---|------|------|-----------------|
| 11 | URI 方案名称 | ✅ 已解决 | 第 364、367 行：改为 `tdx-cold://` 逻辑方案 + `storage_scheme=local` 映射 |
| 12 | 校验和编码规范 | ✅ 已解决 | 第 638 行：`strconv.FormatFloat(v, 'G', 16, 64)`，NaN/Inf 触发批次阻断错误 |
| 13 | Sprint 6 规范不足 | ✅ 已解决 | 第 913-921、1151-1168 行：硬性进入门控（表大小清单、端点依赖图、热/冷表分离） |
| 14 | 监控告警缺失 | ✅ 已解决 | 第 1006-1015 行：5 种告警条件 + 严重级别，首版通过 ops JSON + 结构化日志暴露 |
| 15 | `collector.db` 细节 | ✅ 已解决 | 第 931-933 行：90 天默认保留 + `TDX_GOVERNANCE_TASK_RETENTION_DAYS` |

### 未解决风险（第一轮） — 全部已解决

| # | 风险 | 状态 | 方案中的解决位置 |
|---|------|------|-----------------|
| R1 | 永远被跳过的标的 | ✅ 已解决 | 第 394、735 行：`TDX_LIFECYCLE_MAX_SKIP_COUNT=10` + 跳过 N 次后强制优先处理 |
| R2 | 低磁盘空间赶工 | ✅ 已解决 | 第 718 行：最小 DB 优先策略创建余量，单 DB 超限则拆分为表/年块 |
| R3 | 竞态条件 | ✅ 已解决 | 第 393、859 行：`TDX_LIFECYCLE_POST_GOVERNANCE_COOLDOWN_MINUTES=5`，19:35 后才能运行 |
| R4 | 操作员错误 | ✅ 已解决 | 第 388、1240 行：`TDX_LIFECYCLE_ALLOW_PRUNE_MIN_VERIFIED_SEGMENTS=100`，未达标拒绝修剪 |

---

## 新增内容的审查意见

### 恢复模式设计 — 良好

第 544-556 行的双模式恢复（`temporary_query_restore` / `hot_path_restore`）设计清晰。关键保障到位：临时恢复必须有过期时间且输出到独立目录；热路径恢复需要显式 `retention_override_until` 以防立即被重新归档。

### 启动恢复状态机 — 正确

第 789-795 行的五种状态覆盖了所有中断场景。唯一需要注意的是 `{source_db}.pre_lifecycle.{archive_batch_id}.bak` 存在且状态为 `hot_pruned`/`active` 时的恢复动作（"verify replacement DB before deleting `.bak`"）—— 这里的"验证"应明确为 `PRAGMA integrity_check` + 行数对比，与正常替换的验证保持一致。

### 分片命名规则 — 良好

第 209 行的 `part-{archive_batch_id_suffix}-{seq:03d}.parquet` 解决了不同批次写入同一标的-年份分区时的命名冲突。`archive_batch_id_suffix`（而非完整 ID）足够唯一，且保持文件名可读。

### 空间模型基线 — 实用但有注意事项

第 700-708 行的初始基线假设多年代 trade/live DB 的热数据占比为 5%-20%。这对于 2019 年之前开始采集的标的是正确的，但对于 2024 年才开始的标的，热数据可能占 50%+。Sprint 0 的最坏情况全冷 DB 测试会覆盖这一点 —— 确保测试集中同时包含"全部冷"和"边界情况"（热/冷各占 50%）的 DB。

### `quotes.db` 回退方案 — 需要追踪

第 774 行的回退方案："run a separate pilot comparing full replacement against bounded DELETE plus VACUUM INTO; do not prune quotes.db until that pilot passes." 这实际上是一个 Sprint 2.5 的任务。建议在 Sprint 2 退出标准中明确记录：如果 `quotes.db` 试点未通过，`quotes.db` 的归档在后续 sprint 中单独处理，不阻塞其他域的进展。

---

## 剩余注意事项（非阻断性）

### 1. Parquet 库的维护状态需要验证

方案指定了 `github.com/parquet-go/parquet-go` 作为"segmentio/parquet-go 的维护后继"。Sprint 0 已要求验证维护状态（第 430 行）。需要验证的具体内容：最近一次发布距今多久、是否有未解决的关键 bug、Go 版本兼容性、以及作者对长期支持的承诺。

### 2. 热替换 DB 的索引重建未详细说明

第 753 行要求"创建所需的表和索引"，但未指定这些索引是否需要与原始 DB 完全一致。对于热查询性能，需要保证替换 DB 的索引与原始 DB 的索引在功能上等效。建议在 Sprint 3 中添加一项检查：对比替换前后 `sqlite_master` 中的索引列表。

### 3. 专业金融的 `prof_finance.db` 大小可能包含 WAL

第 54 行显示 `fundamentals` 为 65G，"mostly `professional_finance/prof_finance.db`"。SQLite 的实际大小可能因 WAL 文件和空闲页而膨胀。建议 Sprint 0 的存储清单中包含 `PRAGMA page_count`、`PRAGMA freelist_count` 和 WAL 文件大小，以区分实际数据大小和文件大小。

### 4. 冷 API 的 DuckDB 子进程生命周期

第 428 行选择 DuckDB CLI 子进程而非 CGO 绑定是正确的（避免 CGO 进入主 web 二进制文件），但子进程管理（启动、超时、僵尸进程清理、并发限制）需要明确设计。这不是 Sprint 0-6 的问题，但应在 Sprint 7 开始之前记录在案。

### 5. 最终验收标准已从 14 条扩展到 16 条

新增的标准 14（冷保留策略可见）和 15（告警条件可见）是务实且可验证的。标准 16（"无需手动步骤即可维持 180 天热数据"）是衡量完全自动化成功的关键标准。

---

## 更新后的评分

| 维度 | 第一轮 | 第二轮 | 变化说明 |
|---|---|---|---|
| 架构设计 | 9/10 | 9/10 | 恢复模式、URI 方案、启动恢复状态机加强了架构 |
| 安全性 | 9/10 | 9/10 | 新增最小已验证段阈值使安全性更加稳固 |
| 完整性 | 7/10 | 9/10 | 所有关键空白已填补：quotes.db 算法、空间模型门控、文件租约、索引、告警 |
| 可实施性 | 7/10 | 8/10 | 进入门控明确且可测试；空间模型和 Parquet 验证提前到 Sprint 0 |
| 可运维性 | 7/10 | 9/10 | 启动恢复状态机、告警条件、冷却期、跳过计数让运维变得可行 |
| 与现有代码的集成 | 7/10 | 8/10 | 锁顺序规范清晰，与 flock 模型兼容；文件租约仍为新基础设施但接口已定义 |

---

## 结论

该方案现已成熟，可以进入 Sprint 0 实施。上一轮审查中发现的所有 15 个问题和 4 个风险已得到妥善解决。方案的强度在于其门控结构：每个 sprint 都有明确的退出标准，这些标准必须为真才能继续推进，不安全的操作（修剪、批量迁移）有多层防护。

实施中的关键路径风险仍然是同盘空间模型。第 710 行的声明 —— "This baseline is intentionally conservative and must not be used for pruning decisions. Sprint 0 must replace it with measurements from representative DBs" —— 是正确的态度。如果 Sprint 0 测量结果显示空间模型在最坏情况下不可行，应在故障选项中提前准备增量对象存储卸载方案。

**建议：批准进入 Sprint 0。**

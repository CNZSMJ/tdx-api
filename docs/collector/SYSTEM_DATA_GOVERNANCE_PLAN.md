# System Data Governance Plan

**Status**: Proposed
**Repo**: `tdx-api`
**Scope**: Post-acceptance runtime governance for collector, metadata, static market reference data, and professional finance
**Last Updated**: 2026-04-19

## Purpose

这份文档定义 `tdx-api` 在 collector 已完成首轮验收之后，下一阶段应该如何把现有分散的采集、刷新、补偿、对账、降级，收束成一个统一的系统级数据治理模块。

它回答四个问题：

1. 现有系统里到底有哪些自动任务
2. 这些任务各自应该做什么，不应该做什么
3. 每个任务什么时候运行，开始条件和结束条件是什么
4. 如何分多个原子化 Sprint 落地，而不是一次性大改

## Non-Goals

- 不在本文件中定义 API 外部 contract 变更
- 不在本文件中讨论分布式调度、外部队列、跨服务拆分
- 不在本文件中改写底层 TDX 协议层
- 不把实时 `ticker` / `signal` 常驻循环视为治理任务

## Current Runtime Inventory

当前运行中的自动治理相关任务并不只有三个。

### Explicit collector jobs

| Current task | Trigger | Current behavior |
|---|---|---|
| `collector_startup_catchup` | service boot | 启动后执行一轮大 catch-up |
| `collector_daily_full_sync` | daily `18:00` | 刷新 collector 已覆盖的数据域 |
| `collector_daily_reconcile` | daily `19:00` | 做日期对账、修复、写报告 |

### Hidden or embedded scheduled jobs

| Current task | Trigger | Current behavior |
|---|---|---|
| `workday` auto update | daily `09:00:00` | 刷新交易日历 |
| `codes` auto update | daily `09:00:10` | 刷新股票/ETF/指数代码表 |
| `block` auto refresh | daily `09:00:10` | 刷新板块文件与缓存 |
| `professional finance` auto prefetch | daily `09:00:00` | 同步 `gpcw.txt` / ZIP / serving data |
| `professional finance` startup prefetch | service boot | 启动即做一次 prefetch |
| missed-window compensation | after startup catch-up | 补跑最近漏掉的 `18:00` / `19:00` 窗口 |

### Current problems

- 调度面分裂。部分任务在 `collector`，部分任务藏在 `codes/workday/block/profinance` 的对象构造里。
- 状态面分裂。`/api/collector/status` 只能看到 collector 主任务，看不到全部 `09:00` 任务。
- 启动语义不清。`startup catch-up` 现在更像“从第一只标的重新走一轮任务”，而不是“恢复未完成治理”。
- `18:00` 语义过重。名字叫 full sync，但更合理的职责应是“最近收盘窗口同步”。
- `19:00` 语义混杂。既审计又修复，但没有正式的 backlog / repair / degrade 任务模型。
- 存在重复刷新。`codes` 在当前进程里存在双重实例和双重定时风险。

## Design Goals

- 只有一套系统级治理调度器负责注册和触发治理任务
- 只有一套系统级状态面负责展示 run、健康度、gap、repair、degrade
- `startup` 负责恢复 backlog，不负责默认全市场重扫
- `18:00` 负责最近收盘窗口同步，不负责全历史全量刷新
- `19:00` 负责审计、分类、轻量修复，不承担第二个 full sync
- 历史长尾审计与回补必须独立出来，避免挤占日常窗口
- 每个治理任务都必须有明确输入、开始条件、结束条件和输出产物

## Persistence Layout and Isolation

系统级治理状态必须与业务数据物理隔离。

### Recommended layout

- Governance state DB:
  - `${TDX_DATA_DIR}/governance/system_governance.db`
- Governance lock file:
  - `${TDX_DATA_DIR}/governance/system_governance.lock`
- Governance reports:
  - `${TDX_DATA_DIR}/governance/reports/...`

如果 `TDX_DATA_DIR` 未设置，则回退到 repo-local 默认目录：

- `./data/database/governance/system_governance.db`
- `./data/database/governance/system_governance.lock`
- `./data/database/governance/reports/...`

### Isolation rules

- `system_governance.db` 只存系统级治理状态：
  - `GovernanceRun`
  - `GovernanceTask`
  - `DomainHealthSnapshot`
  - lock holder metadata
  - run evidence index
- 业务数据 SQLite 不存系统级治理历史，避免污染查询路径。
- 现有 `collector.db` 在迁移阶段可以继续承载 collector 本地 cursor / gap / domain-internal state，但新的系统级 run/task/state 底座不应继续堆在 `collector.db` 上。

## System Governance Lock Scope

系统治理锁必须是跨进程、可持久化的锁，不允许只依赖单进程内的 `Mutex`。

### Authoritative lock choice

采用 **本地文件系统独占锁** 作为权威治理锁，目标文件为：

- `${TDX_DATA_DIR}/governance/system_governance.lock`

原因：

- 能跨进程生效
- 进程崩溃后由 OS 自动释放
- 不依赖应用内存状态
- 比单纯的 SQLite 行状态更适合作为“当前唯一写型治理任务”的排他机制

### Lock metadata

为了可观测性，治理状态库还应记录 lock holder metadata，例如：

- `holder_pid`
- `holder_hostname`
- `holder_job_name`
- `holder_run_id`
- `acquired_at`
- `last_heartbeat_at`

注意：

- `system_governance.db` 中的 lock metadata 只用于观测和诊断
- 权威排他语义来自 OS 文件锁，而不是数据库里某一行的布尔字段
- 单进程内仍可使用内存锁作为优化，但它不是权威锁

## Unified Governance Model

### Core concepts

| Concept | Meaning |
|---|---|
| `GovernanceDomain` | 一个受治理的数据域，例如 `codes`、`kline`、`professional_finance` |
| `GovernanceJob` | 一个系统级治理任务类型，例如 `startup_recovery` |
| `GovernanceRun` | 某个治理任务的一次实际执行 |
| `GovernanceTask` | 审计、修复、降级后留下的待办工作项 |
| `Freshness` | 数据是否在预期窗口内被刷新 |
| `Coverage` | 目标窗口是否被完整覆盖 |
| `Gap` | 已知缺口，可分 `open / closed / degraded` |
| `Evidence` | 报告、cursor 摘要、watermark、错误样本、文件路径 |

### Unified run statuses

- `planned`
- `running`
- `passed`
- `partial`
- `failed`
- `interrupted`
- `skipped`

### Unified governance task statuses

- `open`
- `in_progress`
- `repaired`
- `degraded`
- `blocked`
- `unsupported`
- `closed`

### Trading-day vocabulary

- `T`: 最新已完成交易日
- `T-1`: 前一交易日
- `current open day`: 当前交易日，但尚未收盘

所有与 `18:00`、`19:00` 有关的任务，都使用交易日语义，不使用自然日语义。

## System-Level Governance Jobs

系统级治理模块正式定义五类任务。

### 1. `startup_recovery`

| Item | Definition |
|---|---|
| Purpose | 服务启动后恢复 backlog，把系统从中断或脏状态拉回可持续运行状态 |
| When | 每次服务启动后立即评估 |
| Start conditions | 进程启动，且满足任一条件：存在 `interrupted` run、存在漏跑窗口、存在 `open` gap、存在 `repair_pending` 任务、存在 stale watermark、存在缺失 coverage |
| End conditions | 所有 backlog 都被处理成 `repaired / degraded / skipped / queued` 之一；系统进入 `healthy` 或 `degraded-but-acknowledged` |
| Inputs | 最近 runs、missed windows、open/degraded gaps、pending governance tasks、domain freshness / coverage snapshot |
| Outputs | `startup_recovery` run record、补偿任务、repair/degrade 任务、恢复报告 |
| Explicitly not for | 默认全市场全历史重扫 |

### 2. `daily_open_refresh`

| Item | Definition |
|---|---|
| Purpose | 开盘前刷新静态或低频变化的参考数据，保证当天运行使用同一份最新参考面 |
| When | 每天 `09:00` 本地时间 |
| Start conditions | 到达 `09:00`；或 `startup_recovery` 发现当天 `09:00` 漏跑；通过 trading-calendar gate；且没有更高优先级写型任务在运行 |
| End conditions | 当天参考域刷新完成；失败项进入 backlog |
| Inputs | `codes/workday/block/professional_finance` 的 freshness、remote watermark、artifact 状态 |
| Outputs | `daily_open_refresh` run、最新 reference watermark、失败清单 |
| Explicitly not for | 历史成交、K 线、历史订单、收盘后对账 |

### 3. `daily_close_sync`

| Item | Definition |
|---|---|
| Purpose | 只同步最近收盘窗口的数据，不做全历史全量刷新 |
| When | 每天 `18:00` 本地时间 |
| Start conditions | 到达 `18:00`；或 `startup_recovery` 发现当天 `18:00` 漏跑；通过 trading-calendar gate；存在 `T`；且没有更高优先级写型任务在运行 |
| End conditions | `T` 和 `T-1` 的目标域同步完成；失败项被转成 repair task，不阻塞主任务无限重试 |
| Inputs | `T/T-1` 窗口、域级 cursor、coverage、backlog 中与最近窗口相关的 repair task |
| Outputs | `daily_close_sync` run、close watermark、repair task |
| Explicitly not for | 全历史重扫、深度审计 |

### 4. `daily_audit`

| Item | Definition |
|---|---|
| Purpose | 审计最近窗口的数据健康，做轻量修复，并把问题分类成正式治理待办 |
| When | 每天 `19:00` 本地时间 |
| Start conditions | 到达 `19:00`；或 `startup_recovery` 发现当天 `19:00` 漏跑；通过 trading-calendar gate；且 `daily_close_sync` 已完成或被判定为可审计 |
| End conditions | 审计报告生成；问题被分类为 `repaired / repair_pending / degraded / blocked / unsupported` |
| Inputs | `T/T-1` 数据、collector gap、cursor 摘要、repairable error signals |
| Outputs | `daily_audit` run、审计报告、repair task、degrade task、域级健康快照 |
| Explicitly not for | 第二个 full sync、历史全量回补 |

### 5. `deep_audit_backfill`

| Item | Definition |
|---|---|
| Purpose | 处理历史长尾、深度审计、批量回补、人工指定历史窗口治理 |
| When | 建议每周一次低峰时段，或人工触发 |
| Start conditions | 到达低峰窗口；且没有更高优先级治理任务；存在历史 backlog 或人工指定窗口 |
| End conditions | 指定历史窗口处理完成；发现问题全部落成治理任务或完成修复 |
| Inputs | 历史 gap、历史缺失 coverage、人工指定窗口、历史 watermark 差异 |
| Outputs | `deep_audit_backfill` run、历史审计报告、批量 repair/degrade 任务 |
| Explicitly not for | 日常开盘前准备、最近窗口同步 |

## Governance Priority

同一时刻只允许一个写型系统治理任务运行。

优先级从高到低：

1. `startup_recovery`
2. `daily_open_refresh`
3. `daily_close_sync`
4. `daily_audit`
5. `deep_audit_backfill`

如果窗口到达时高优先级任务仍在运行，窗口不会丢失，而是转成 `missed-window governance task`，由 `startup_recovery` 或后续空闲窗口补跑。

## Trading-Calendar Gate

所有日常定时治理任务都必须在获取系统治理锁之后，先做一次 trading-calendar gate。

### Gate rules

- `daily_open_refresh`
- `daily_close_sync`
- `daily_audit`

这三个任务都必须先判断：

1. 当前本地自然日是否对应一个有效交易日窗口
2. 是否存在需要处理的 `T` / `T-1`
3. 是否只是普通周末/节假日空窗

### Skip behavior

如果当前是非交易日，并且不存在需要补跑的历史窗口，则任务直接：

- 获取锁
- 记录一次 `skipped` run
- reason 标记为 `non_trading_day_window`
- 不再进入空转采集逻辑

### Exceptions

- `startup_recovery` 可以在任何自然日运行，因为它处理的是 backlog 和 missed windows
- `deep_audit_backfill` 可以在任何自然日运行，因为它处理的是历史长尾
- 手动触发的补偿或指定窗口回放，可以显式绕过默认 skip，但必须写明 target trading window

### Calendar source

trading-calendar gate 默认使用最近一次成功发布的 `workday` 快照进行判断，而不是依赖当次 `09:00` 刚刚刷新成功之后才能判断。

这样可以避免：

- 因为当天 `workday` 刷新失败，导致调度器自己失去交易日判断能力
- 在周末和节假日产生大量无意义的空转 runs

## Domain Assignment Matrix

| Domain | Startup Recovery | Daily Open Refresh | Daily Close Sync | Daily Audit | Deep Audit / Backfill |
|---|---|---|---|---|---|
| `codes` | 恢复异常或漏跑 | 主刷新 | 不参与 | freshness 审计 | 必要时全量重建 |
| `workday` | 恢复异常或漏跑 | 主刷新 | 不参与 | freshness / consistency 审计 | 必要时重建 |
| `block` | 恢复异常或漏跑 | 主刷新 | 不参与 | freshness 审计 | 必要时重建 |
| `professional_finance` artifacts / serving watermark | 恢复异常、补漏 | 主刷新 | 默认不参与 | freshness / visibility 审计 | 历史重扫、重建 serving |
| `kline` | 处理 backlog、gap、漏跑窗口 | 不参与 | 主同步 `T/T-1` | continuity / gap 审计，轻量 repair | 历史重扫与 backfill |
| `trade_history` | 处理 backlog | 不参与 | 主同步 `T/T-1` | coverage 审计，轻量 repair | 历史 backfill |
| `live_capture` close state | 处理 backlog | 不参与 | 主同步 `T/T-1` | completeness 审计，轻量 repair | 历史 backfill |
| `order_history` | 处理 backlog | 不参与 | 主同步 `T/T-1` | coverage 审计，轻量 repair | 历史 backfill |
| `finance` | 恢复 backlog，不做默认全市场重扫 | 不参与 | 增量同步最近变化 | freshness / completeness 审计 | 历史抽查或重建 |
| `f10` | 恢复 backlog，不做默认全市场重扫 | 不参与 | 增量同步最近变化 | freshness / completeness 审计 | 历史抽查或重建 |

## Domain-Specific Rules

### Metadata and static reference domains

- `codes`
- `workday`
- `block`
- `professional_finance` remote list / artifact watermark

这些域的主刷新窗口属于 `daily_open_refresh`。

原因：

- 它们是全系统共享参考面
- 它们变化频率低于交易明细数据
- 它们不应该再各自偷偷注册 `09:00` cron

### Open-critical fast retry policy

`daily_open_refresh` 里不是所有域都同等关键。

#### Open-critical domains

- `codes`
- `workday`

这两个域直接影响：

- 当天证券代码全集是否可用
- 当天交易日窗口判定是否可靠
- 开盘后 ticker / quote / collector 任务是否会连锁失败

#### Open-supporting domains

- `block`

`block` 很重要，但不是开盘前绝对阻塞项。失败后可以降级运行，不应像 `codes/workday` 一样卡死全部开盘前准备。

#### Non-open-critical domains

- `professional_finance` artifact refresh

它属于参考面刷新，但不应阻塞 09:30 前的市场运行链路。

#### Fast-retry semantics

对于 `codes` 和 `workday`：

- 如果 `09:00` 首次刷新因为临时网络或上游抖动失败，不应立刻结束并仅仅转入 backlog
- 应在 `09:00 - 09:30` 的 pre-open window 内执行短间隔 fast-retry
- 只有在 fast-retry 预算耗尽，或已经超过 pre-open window，才将失败正式沉淀为 backlog / high-priority governance task

推荐策略语义：

- fast-retry 属于同一个 `daily_open_refresh` run 的内部阶段
- retry 使用短退避，而不是等到下一日定时
- `codes/workday` 若在 pre-open window 内恢复成功，则该 run 仍可视为 `passed` 或 `partial-recovered`
- 若直到 window 结束仍失败，则该 run 至少为 `partial`，并生成高优先级 repair task

这条规则的目标不是追求“09:00 整点一定成功”，而是保证开盘前关键参考面具备更强鲁棒性。

### Market close domains

- `kline`
- `trade_history`
- `live_capture` close state
- `order_history`
- `finance`
- `f10`

这些域的最近窗口同步属于 `daily_close_sync`。

要求：

- `daily_close_sync` 只处理 `T/T-1`
- 不负责历史全量重扫
- 失败项转成 repair task

### Audit-only responsibilities

`daily_audit` 必须完成：

- 最近窗口 coverage 审计
- 最近窗口 continuity 审计
- `gap` 扫描与分类
- 轻量可修复问题的自动 repair
- 对不可修复问题执行 degrade
- 固化当日健康快照和报告

`daily_audit` 不得承担全历史全量刷新。

## Unified Start and Stop Rules

### Shared start rules

任一治理任务启动前都必须满足：

1. 获取系统治理锁
2. 读取最新域级状态快照
3. 解析是否存在更高优先级任务
4. 决定本次 run 的目标窗口和输入任务集

### Shared stop rules

任一治理任务结束时都必须写出：

- run status
- run details
- affected domain snapshots
- newly created governance tasks
- closed / degraded gaps
- report path or evidence summary

### Shared interrupt rules

以下情况允许任务中断：

- 手动 pause / stop
- 服务关闭或重启
- 运行超时
- 顶层 fatal error

中断后必须把本次 run 标成 `interrupted`，并把未完成工作重新投回 backlog。

## Unified Outputs

统一后的治理系统，任何任务都至少应该产出以下内容：

- `GovernanceRun`
- `DomainHealthSnapshot`
- `Cursor / Watermark Summary`
- `GovernanceTask` delta
- `Gap` delta
- `Evidence` location

用户和运维视角最终只看一套状态：

- 当前有哪些任务在跑
- 各域是否健康
- 哪些 backlog 尚未处理
- 哪些问题已降级确认
- 最近一个交易窗口的治理结果是什么

## Proposed Naming

统一后的任务命名建议：

- `startup_recovery`
- `daily_open_refresh`
- `daily_close_sync`
- `daily_audit`
- `deep_audit_backfill`

旧名字与新名字映射：

| Current name | Proposed name |
|---|---|
| `collector_startup_catchup` | `startup_recovery` |
| `collector_daily_full_sync` | `daily_close_sync` |
| `collector_daily_reconcile` | `daily_audit` |
| embedded `09:00` jobs | `daily_open_refresh` |

## Sprint Plan

下面的实施拆分要求每个 Sprint 都是原子、独立、可验证的功能增量，而不是跨多个 Sprint 才能工作的半成品。

### Sprint 1: Governance Control Plane

**Goal**

建立系统级治理的控制面和命名，但不改变现有运行行为。

**In**

- 建立独立的治理状态底座 `system_governance.db`
- 建立跨进程的持久化治理锁 `system_governance.lock`
- 新增 `GovernanceJob` / `GovernanceRun` / `GovernanceTask` / `DomainHealthSnapshot` 模型
- 为现有 `startup/18/19` 和 `09:00` 任务建立统一命名和状态映射
- 记录 lock holder metadata 和 run evidence index
- 扩展 `/api/collector/status` 的返回结构，显示全量治理任务清单和域级健康摘要

**Out**

- 不改具体调度触发方式
- 不改任务语义

**Atomic value**

即使后续 Sprint 还没做，系统也已经先获得统一状态面和统一术语。

**Exit gate**

- 能在一个状态接口里看到所有治理任务与域级健康摘要
- 跨进程治理锁具备排他语义且可被观测
- 不改变现有任务执行行为

### Sprint 2: Daily Open Refresh Unification

**Goal**

把 `codes/workday/block/profinance` 的内嵌 `09:00` cron 收敛为统一的 `daily_open_refresh`。

**In**

- 取消对象构造即自带 cron 的模式
- 由统一治理调度器注册 `09:00` 任务
- 保留原有刷新逻辑，但状态与触发统一
- 对 `codes/workday` 引入 pre-open fast-retry
- 对 `daily_open_refresh` 引入 trading-calendar gate

**Out**

- 不改 `18:00/19:00`
- 不改 startup 语义

**Atomic value**

解决隐藏定时器和 `codes` 双重调度问题，立即减少重复刷新和状态盲区。

**Exit gate**

- 当前进程内只剩一个 `09:00` 系统任务入口
- `codes/workday/block/profinance` 刷新都能被统一记录
- 非交易日 `09:00` 任务会显式 `skipped`
- `codes/workday` 在 pre-open window 内具备 fast-retry

### Sprint 3: Daily Close Sync Semantics Cutover

**Goal**

把 `18:00 full sync` 改造成 `daily_close_sync`，职责明确为同步最近窗口 `T/T-1`。

**In**

- 重命名任务语义
- 把 `18:00` 的默认输入改成 `T/T-1`
- 失败项进入 repair task，而不是无限兜底重扫
- 对 `daily_close_sync` 引入 trading-calendar gate

**Out**

- 不改 `startup`
- 不做历史全量回补

**Atomic value**

即使 `startup` 仍旧较重，日常 `18:00` 已经从“伪全量”变成“最近窗口同步”。

**Exit gate**

- `18:00` 只处理 `T/T-1`
- `18:00` 的失败项能留下正式 repair task
- 非交易日 `18:00` 任务会显式 `skipped`

### Sprint 4: Daily Audit Taskification

**Goal**

把 `19:00` 从“只写一份报告”升级为“报告 + repair/degrade/backlog”的正式审计任务。

**In**

- 为审计结果建立 `repair_pending / degraded / blocked / unsupported` 分类
- 审计报告与治理任务绑定
- 对轻量可修复问题做自动 repair
- 对 `daily_audit` 引入 trading-calendar gate

**Out**

- 不做历史深扫
- 不改 `startup`

**Atomic value**

系统第一次拥有正式 backlog，不再只是知道“有问题”，而是知道“下一步怎么办”。

**Exit gate**

- `19:00` 结束后一定产生报告和任务分类
- 不可修复问题可以被正式 degrade
- 非交易日 `19:00` 任务会显式 `skipped`

### Sprint 5: Startup Recovery Cutover

**Goal**

把当前的 `startup catch-up` 从“从头走一轮任务”改成“消费 backlog 和漏跑窗口”。

**In**

- `startup` 只处理 missed windows、interrupted runs、open/degraded gaps、pending repair tasks
- 启动后优先恢复未完成治理，不默认重扫全市场

**Out**

- 不实现 instrument 级 checkpoint 续跑
- 不做 `deep_audit_backfill`

**Atomic value**

重启语义被纠正，系统终于从“每次重启都从头扫”切换成“真正的恢复模式”。

**Exit gate**

- `startup` 默认不再全市场重扫
- 漏跑的 `09/18/19` 能由 `startup` 统一补偿

### Sprint 6: Repair Worker Generalization

**Goal**

把目前零散的 gap cleanup / reconcile / downgrade 能力统一成通用 repair worker。

**In**

- 通用 repair task executor
- 针对 `kline/trade/live/order` 的定向治理入口
- 通用 degrade / blocked / unsupported 结果写回

**Out**

- 不做历史批量深扫

**Atomic value**

审计产生的问题终于有统一执行器，不再依赖手工 API 和一次性脚本。

**Exit gate**

- repair task 可以被统一执行和重试
- degraded task 不会被下一轮错误 reopen

### Sprint 7: Fundamentals and Professional Finance Incrementalization

**Goal**

给 `finance`、`f10`、`professional_finance` 建立真正的增量治理规则。

**In**

- 基于 `updated_date`
- 基于 artifact watermark
- 基于 serving watermark / visible report date
- 避免启动和最近窗口任务里默认全市场重扫

**Out**

- 不改外部查询 contract

**Atomic value**

基本面域从“每次都扫一遍”变成“按变化治理”，直接降低运行成本。

**Exit gate**

- `finance/f10/profinance` 都能根据增量条件决定是否刷新
- `startup` 和 `18:00` 都不再默认全量扫这三类域

### Sprint 8: Deep Audit / Backfill

**Goal**

补上历史长尾治理能力，但不影响日常窗口。

**In**

- 独立的 `deep_audit_backfill` 任务
- 支持按历史窗口或 backlog 范围执行
- 低峰时段定时或人工触发

**Out**

- 不改变 `09/18/19` 核心职责

**Atomic value**

历史长尾终于有独立出路，不再挤占日常治理主线。

**Exit gate**

- 可以独立调度历史深扫
- 不会与 `09/18/19` 混成同一个任务语义

## Recommended Rollout Order

推荐严格按以下顺序推进：

1. Sprint 1
2. Sprint 2
3. Sprint 3
4. Sprint 4
5. Sprint 5
6. Sprint 6
7. Sprint 7
8. Sprint 8

理由：

- 先统一术语和状态面，再收敛 `09:00` 调度
- 先修正 `18:00/19:00` 语义，再修正 `startup`
- 先有 backlog 和 repair task，再做 repair worker
- 最后再处理历史深扫，避免过早把系统复杂度拉高

## Final Decision Summary

统一后的系统级数据治理模块，正式由五类任务组成：

- `startup_recovery`
- `daily_open_refresh`
- `daily_close_sync`
- `daily_audit`
- `deep_audit_backfill`

其中：

- `09:00` 负责静态参考面刷新
- `18:00` 负责最近收盘窗口同步
- `19:00` 负责审计、分类、轻量修复
- `startup` 负责恢复 backlog 和漏跑窗口
- `deep_audit_backfill` 负责历史长尾

这五类任务共同组成一个统一的系统级数据治理闭环，而不再是若干互相重叠、互相看不见的脚本和 cron。

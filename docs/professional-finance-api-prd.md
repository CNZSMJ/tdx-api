# Product Requirements Document: Professional Finance API

**Version**: 1.0  
**Date**: 2026-04-17  
**Author**: Sarah (Product Owner)  
**Quality Score**: 93/100

---

## Executive Summary

`tdx-api` 当前已经具备两条财务相关能力：一条是 `/api/finance` 对 `TDX GetFinanceInfo` 的原始透传，另一条是 `/api/financial-reports` 对 `gpcw.txt + gpcw*.zip` 专业财务远程文件的部分接入。但这两条能力在产品形态上仍然混杂：前者是 raw 单快照，后者是少量字段的历史序列，`/api/profile` 又把 raw 财务、专业财务和实时行情混合在一起生成常用估值字段。

这不符合专业投研场景的最佳实践。Bloomberg 与 Wind 的公开接口形态虽然实现细节不同，但都体现出稳定模式：区分单证券截面查询、跨证券截面查询、历史序列查询与字段字典查询；区分原始值、标准化值和派生值；显式表达报告期、披露日期、时点语义、字段单位和覆盖状态。基于这一模式，本设计稿提出一组面向 `gpcw` 专业财务数据的专门 API，使 `tdx-api` 具备更接近专业终端的数据产品形态。

---

## Problem Statement

**Current Situation**

- `/api/finance` 是单次 raw 财务快照，但字段命名和口径偏底层，难以直接用于研究系统。
- `/api/financial-reports` 已经接入了 `gpcw`，但当前只暴露极少字段，无法体现 `gpcw` 作为完整三大报表与衍生因子源的价值。
- `/api/profile` 在同一个响应里混合 raw 财务、专业财务、实时行情和估值计算，适合轻量快照，不适合做专业财务接口。
- 当前缺少统一的财务字段字典接口，调用方无法知道某个字段来自 raw finance 还是 `gpcw`，也无法知道字段单位、口径、报告期类型和时间语义。
- 当前缺少“多证券单报告期截面查询”能力，无法很好支持类似 Wind `WSS` / Bloomberg reference-style fundamentals 的用法。

**Proposed Solution**

新增一组面向专业财务数据的 API family，以 `gpcw` 为主数据源，围绕 `field catalog`、`snapshot`、`cross-section`、`history` 和 `coverage` 五类能力构建标准接口。该组接口不替代 `/api/profile` 的轻量快照职责，也不再把专业财务能力继续堆叠进 `/api/finance` 的 raw 透传语义中。

**Business Impact**

- 把 `gpcw` 从“少量字段补丁”升级成正式的专业财务数据产品。
- 为后续 MCP 的 `market/fundamentals` 能力提供更稳定、专业、可解释的上游。
- 为选股、横截面比较、财报历史趋势分析、估值分析和研究模板提供统一接口。

---

## Public References

本设计参考了以下公开资料：

- Bloomberg BLPAPI 官方文档与开发者指南：
  - [BLPAPI Documentation](https://bloomberg.github.io/blpapi-docs/)
  - [BLPAPI Core Developer Guide (PDF)](https://data.bloomberglp.com/professional/sites/4/blpapi-developers-guide-2.54.pdf)
- Wind 官方产品/API 页面：
  - [Wind Client API](https://www.wind.com.cn/mobile/ClientApi/zh.html)
  - [Wind 数据接口服务](https://www.wind.com.cn/portal/zh/WDS/sapi.html)
  - [Wind 金融终端](https://www.wind.com.cn/portal/zh/WFT/index.html)

基于这些公开资料，可以直接确认的模式有：

- Bloomberg 官方公开 API 将 `ReferenceDataRequest` 与 `HistoricalDataRequest` 明确分开。
- Wind 官方公开 API 强调以统一 Client API 接入全球数据，并面向投研、量化与系统集成场景提供不同形态的数据服务。

下面关于“Bloomberg/Wind 财务 API 最佳实践”的总结，是**基于这些公开接口形态与行业通用使用习惯的推断**，不是对其私有终端文档的逐字复刻。

---

## Design Principles

### 1. Split by query shape, not by internal file

调用方关心的是“我要查一只证券最新财报”“我要查多只证券同一报告期的横截面”“我要查一只证券多期历史”，而不是底层是否来自 `gpcw20251231.zip`。因此 API 应按查询形态分组，而不是按文件名暴露。

### 2. Separate source mapping and public contract

- `source mapping`: 系统内部保留 `gpcw source_field_id` 到公共字段的映射
- `public contract`: 对外只暴露一套稳定字段名
- `derived`: 由财务字段和实时价格计算出的估值与衍生指标

### 3. Time semantics must be explicit

专业投资接口必须避免未来函数。每次返回都要明确：

- `report_date`: 财报所属报告期
- `announce_date`: 公告日，如果暂时无法完整提供则显式缺失
- `as_of_date`: 调用者要求的观察时点
- `knowledge_cutoff`: 系统在该时点能知道的数据边界

### 4. Field-first model

先有字段目录，再有数据查询。字段目录是专业财务 API 的地基。

### 5. Cross-sectional access is first-class

单证券查询不够，必须支持“多证券 + 同一字段 + 同一报告期/时点”的专业横截面取数。

### 6. Missing coverage is a valid response

不允许默默补零，也不允许把“无数据”伪装成“0”。必须用显式的 `missing_fields`、`coverage`、`status` 表达。

### 7. Naming must be unambiguous

同一份设计里，不允许混用 `code` 来同时表示证券代码和字段代码。本文统一使用以下术语：

- `full_code`
  - 带交易所前缀的证券代码，例：`sh600519`、`sz000001`、`bj430047`
- `full_codes`
  - 多个带交易所前缀的证券代码列表
- `name`
  - 证券名称，专门用于证券实体，例：`贵州茅台`
- `field_code`
  - 公共财务字段名，例：`book_value_per_share`
- `field_codes`
  - 多个公共财务字段名列表
- `source_field_id`
  - 底层 `gpcw` 原始字段编号，例：`4`、`238`、`283`
- `field_name_cn`
  - 字段或元数据对象的中文名称，只用于字段字典/字段元数据
- `field_name_en`
  - 字段或元数据对象的英文名称，只用于字段字典/字段元数据

说明：

- **查询接口只接受 `field_code / field_codes`**
- `source_field_id` 只在字段字典与元数据中出现，不作为主要查询参数对外暴露
- **查询接口只接受 `full_code / full_codes`**
- 不接受 bare 6 位代码作为正式 contract，以避免市场歧义
- **证券实体统一用 `name`**
- **字段字典与字段元数据统一用 `field_name_cn / field_name_en`**

---

## Target Users

### Primary: 投研工程师 / 量化研究员

- **Goals**: 快速获取稳定、可批量、可回测的财务数据
- **Pain Points**: raw TDX 财务口径弱、字段混乱、历史序列不足
- **Technical Level**: Advanced

### Secondary: MCP / 内部服务调用方

- **Goals**: 为市场分析、估值、选股、研究问答提供可解释的专业财务上游
- **Pain Points**: 当前只能拼接 `/api/finance`、`/api/financial-reports`、`/api/profile`
- **Technical Level**: Advanced

---

## Proposed API Family

统一前缀：

- `/api/prof-finance/*`

### Response Shape Conventions

为避免歧义，本文对返回结构做统一约定：

- 顶层 `data` 是一个 JSON object
- `field_values` 是 `object<field_code, value>`
- `coverage` 是一个 JSON object
- `items` 是一个 array
- `list` 是一个 array

除非明确标注为 `items[]` 或 `list[]`，否则字段默认都是对象字段，不额外再包一层无意义对象。

### A. Field Catalog

#### `GET /api/prof-finance/fields`

**Purpose**

返回专业财务字段字典，供调用方先发现字段，再发起查询。

**Core params**

- `category=meta|disclosure|per_share|balance_sheet|income_statement|cash_flow_statement|solvency|operating_efficiency|growth|profitability|capital_structure|cash_flow_analysis|single_quarter|shareholder|institutional_holding|all`
- `query=...`

**Response fields**

- `field_code`
- `source_field_id`
- `field_name_cn`
- `field_name_en`
- `category`
- `statement`
- `period_semantics`
- `unit`
- `value_type`
- `source`
- `supported`

**Notes**

- `source_field_id` 对应 `gpcw` 的原始字段编号
- `field_code` 是唯一公开字段名，例如 `book_value_per_share`
- 字段目录接口天然同时承担“公共字段字典”和“底层字段映射说明”职责，不再额外引入 `view`
- 字段目录必须完整覆盖 `FINVALUE` / `FINONE` 的全部专业财务字段定义，而不是只覆盖首批常用字段

---

### B. Single-Security Snapshot

#### `GET /api/prof-finance/snapshot`

**Purpose**

查询单只证券在某个报告期或某个观察时点下的专业财务快照。  
这条接口对应 Bloomberg / Wind 习惯里的“单证券截面财务查询”。

**Core params**

- `full_code`
- `report_date`
- `as_of_date`
- `field_codes`
- `period_mode=latest_available|latest_report|exact`

**Parameter rules**

- `full_code` 必须带交易所前缀，例如 `sh600519`
- `field_codes` 只接受公共字段名
- 查询接口不再提供第二套字段视图，只返回公共字段

**Response shape**

- `full_code`
- `name`
- `report_date`
- `announce_date`
- `as_of_date`
- `knowledge_cutoff`
- `source`
- `field_values`
- `missing_fields`
- `coverage`

---

### C. Cross-Section Snapshot

#### `GET /api/prof-finance/cross-section`

**Purpose**

多证券、同报告期/同时点的横截面财务查询。  
这条接口是 Wind `WSS` / Bloomberg reference-style fundamentals 的核心形态。

**Core params**

- `full_codes=...`
- `report_date`
- `as_of_date`
- `field_codes=...`
- `period_mode=latest_available|latest_report|exact`

**Response shape**

- `report_date`
- `as_of_date`
- `knowledge_cutoff`
- `field_codes`
- `items[]`
  - `full_code`
  - `name`
  - `field_values`
  - `missing_fields`
  - `coverage`

---

### D. Historical Series

#### `GET /api/prof-finance/history`

**Purpose**

单证券多报告期历史序列查询。  
这条接口覆盖当前 `/api/financial-reports` 的更通用形态。

**Core params**

- `full_code`
- `field_codes=...`
- `as_of_date`
- `start_report_date`
- `end_report_date`
- `limit`
- `period=quarterly|annual|all`

**Response shape**

- `full_code`
- `name`
- `as_of_date`
- `knowledge_cutoff`
- `field_codes`
- `list[]`
  - `report_date`
  - `announce_date`
  - `field_values`
  - `missing_fields`
  - `source_report_file`

---

### E. Coverage / Availability

#### `GET /api/prof-finance/coverage`

**Purpose**

返回证券在专业财务链路上的覆盖状态，避免调用方把无覆盖误判成零值。

**Core params**

- `full_code`
- `report_date`

**Response shape**

- `full_code`
- `name`
- `latest_report_date`
- `knowledge_cutoff`
- `available_reports`
- `available_field_codes`
- `missing_fields`
- `status`

---

## Public Query Contract

所有专业财务查询接口统一遵守以下 contract：

- 请求只接受 `field_codes`
- 响应只返回 `field_values`
- `field_values` 的 key 始终是 `field_code`
- 底层 `gpcw source_field_id` 只通过 `/api/prof-finance/fields` 暴露
- 所有查询响应都必须显式返回 `knowledge_cutoff`

也就是说：

- 查询接口负责“取值”
- 字段目录接口负责“解释字段来自哪里、单位是什么、底层映射是什么”

---

## Full Field Coverage Requirement

本设计不再采用 `P0/P1/P2/P3` 这种“先只做一部分字段”的范围定义。  
对专业财务 API 而言，**首版字段宇宙就是完整字段宇宙**。

### Scope rule

- 覆盖 `gpcw` 的全部专业财务字段定义
- 以 `FINVALUE` / `FINONE` 作为同一套 `source_field_id` 体系的两种访问语义
- 不把 `GPJYVALUE`、`BKJYVALUE`、`SCJYVALUE`、`GPONEDAT` 混入专业财务字段范围

### Current full-field baseline

当前公开可确认的专业财务字段基线为：

- `403` 个 `source_field_id` 定义
- appendix 以 `18` 个 source section 组织
- 对外字段目录把这些字段归一到 `15` 个 public categories
- `FINVALUE` 与 `FINONE` 共用同一套字段编号

字段分组范围如下：

- 元数据：`0`
- 每股指标：`1-7`
- 资产负债表：`8-73`
- 利润表：`74-97`
- 现金流量表：`98-158`
- 偿债能力分析：`159-171`
- 经营效率分析：`172-182`
- 发展能力分析：`183-192`
- 获利能力分析：`193-209`
- 资本结构分析：`210-218`
- 现金流量分析：`219-229`
- 单季度财务指标：`230-237`
- 股本股东：`238-245`
- 机构持股：`246-263`
- 新增指标：`264-322`
- 资产负债表新增指标：`401-439`
- 利润表新增指标：`501-521`
- 现金流量表新增指标：`561-580`

对外字段目录使用如下 public categories：

- `meta`
- `disclosure`
- `per_share`
- `balance_sheet`
- `income_statement`
- `cash_flow_statement`
- `solvency`
- `operating_efficiency`
- `growth`
- `profitability`
- `capital_structure`
- `cash_flow_analysis`
- `single_quarter`
- `shareholder`
- `institutional_holding`

### Normative appendix

完整字段清单见：

- [professional-finance-field-catalog-baseline.md](/Users/huangjiahao/workspace/industry-investment-suite/repos/tdx-api/docs/professional-finance-field-catalog-baseline.md)

这份附录是本设计的规范性组成部分，用来定义：

- 每一个已覆盖的 `source_field_id`
- 其中文含义
- 所属逻辑分组
- Professional Finance API 必须至少能够在字段目录层暴露这些定义

### Public field strategy

对外仍然只暴露一套稳定的 `field_code` 体系，但这不等于只覆盖少数字段。

- `/api/prof-finance/fields` 必须能列出全部已注册字段，而不只是当前可查询字段
- 每个字段目录项都必须有稳定的 `field_code`，用于字段目录、映射追踪和覆盖状态表达
- `supported` 只表示该字段当前是否进入查询接口 contract，不改变 `field_code` 作为字段目录稳定标识的语义
- 每个字段目录项都必须能追溯到一个或多个 `source_field_id`
- 对于同一经济含义存在多个 `source_field_id` 的情况，字段目录必须显式说明 `period_semantics`
  - 例如报告期值、单季度值、TTM 值、快报值、预告值
- 对于暂不进入查询接口的字段，也必须在字段目录中保留稳定 `field_code` 并明确标注 `supported=false`，而不是在设计层静默遗漏

### Examples of same-concept multi-source fields

以下类型说明为什么“完整覆盖所有字段”不能简化成只挑少量常用字段：

- 营业收入同时存在：
  - `74` 报告期营业收入
  - `230` 单季度营业收入
  - `283` 最近一年营业收入
  - `312` 单季度营业总收入
  - `319` 营业总收入 TTM
- 净利润同时存在：
  - `95` 净利润
  - `96` 归母净利润
  - `232` 单季度归母净利润
  - `276` 近一年净利润
  - `287` 业绩快报归母净利润
- 净资产收益率同时存在：
  - `6` 净资产收益率
  - `197` 净资产收益率
  - `281` 加权净资产收益率
  - `292` 业绩快报摊薄净资产收益率
  - `293` 业绩快报加权净资产收益率

因此，本设计的完整覆盖要求是：

- 不遗漏任何一个 `source_field_id`
- 不模糊不同口径的时间语义
- 不把“未映射”误写成“无此字段”

---

## Interface Semantics

### Report date vs as-of date

- `report_date` 表示财报所属期间
- `as_of_date` 表示调用方希望系统站在什么时间点看世界

若 `as_of_date=2026-04-17`，则只能返回在该日期之前已经可得的报告。  
这条规则是专业投研接口必须具备的要求，用于避免未来函数。

### Exact vs latest

- `period_mode=exact`
  - 只返回指定 `report_date`
- `period_mode=latest_report`
  - 返回最新报告期
- `period_mode=latest_available`
  - 返回当前可得的最新可用报告

### Missing handling

禁止以下行为：

- 用 `0` 代表缺失值
- 对字段缺失静默省略但不说明
- 对公告期外不可得数据做未来穿透

必须返回：

- `missing_fields`
- `coverage`
- `status`

---

## Recommended Response Examples

### 1. Snapshot

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "full_code": "sh600519",
    "name": "贵州茅台",
    "report_date": "20251231",
    "as_of_date": "20260417",
    "knowledge_cutoff": "20260417",
    "source": "tdx_professional_finance",
    "field_values": {
      "book_value_per_share": 198.42,
      "total_shares": 1256197800,
      "float_a_shares": 1256197800,
      "net_profit_ttm": 89214300000,
      "revenue_ttm_yuan": 186532000000,
      "weighted_roe": 31.2
    },
    "missing_fields": [],
    "coverage": {
      "available": true,
      "source_report_file": "gpcw20251231.zip"
    }
  }
}
```

### 2. Cross-section

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "report_date": "20251231",
    "knowledge_cutoff": "20260417",
    "field_codes": ["net_profit_ttm", "weighted_roe"],
    "items": [
      {
        "full_code": "sh600519",
        "name": "贵州茅台",
        "field_values": {
          "net_profit_ttm": 89214300000,
          "weighted_roe": 31.2
        },
        "missing_fields": []
      },
      {
        "full_code": "sz000001",
        "name": "平安银行",
        "field_values": {
          "net_profit_ttm": 42632998912,
          "weighted_roe": 9.15
        },
        "missing_fields": []
      }
    ]
  }
}
```

---

## Relationship with Existing APIs

### Keep

- `/api/finance`
  - 定位：raw TDX 财务快照
- `/api/profile`
  - 定位：轻量快照，不是专业财务主接口

### Replace / absorb

- `/api/financial-reports`
  - 建议被 `/api/prof-finance/history` 吸收
  - 可在迁移期保留，但不应继续作为最终专业财务主接口扩展

---

## Implementation Plan

### Phase 1

- 建立专业财务字段注册层
- 完整收录 `FINVALUE / FINONE` 全部 `403` 个 `source_field_id`
- 为每个字段补齐：
  - `field_code`
  - `source_field_id`
  - `field_name_cn`
  - `field_name_en`
  - `category`
  - `statement`
  - `period_semantics`
  - `unit`
  - `value_type`
  - `source`
  - `supported`
- 明确同一经济含义下不同时间语义字段的公共命名
- 明确 `report_date`、`announce_date`、`as_of_date`、`knowledge_cutoff` 的统一时间语义
- 落地 `/api/prof-finance/fields`

### Phase 2

- 建立专业财务统一查询层
- 基于统一字段注册层实现：
  - 单证券快照查询
  - 单证券历史序列查询
  - 多证券横截面查询
- 所有查询接口统一遵守：
  - 请求只接受 `full_code` / `full_codes`、`field_codes`
  - 响应只返回 `field_values`
  - 响应显式返回 `knowledge_cutoff`
  - 按 `as_of_date` 控制可见性
  - 不返回未来可见的数据
  - 不用 `0` 表示缺失
  - 显式返回 `missing_fields`
- 落地：
  - `/api/prof-finance/history`
  - `/api/prof-finance/snapshot`
  - `/api/prof-finance/cross-section`

### Phase 3

- 实现专业财务覆盖状态查询
- 明确字段级、报告期级、证券级可用性
- 返回最新报告覆盖、可用报告列表、可用字段列表和缺失字段
- 响应显式返回 `knowledge_cutoff`
- 落地 `/api/prof-finance/coverage`

### Phase 4

- 让 `/api/profile` 复用统一专业财务查询层
- 保证专业财务字段来源、命名和时间语义与 `/api/prof-finance/*` 保持一致
- 完成字段注册测试、查询层测试、handler 测试和回归测试
- 更新 API 文档与示例

## Completion Standard

- `/api/prof-finance/fields` 可列出完整 `403` 个专业财务字段定义
- `/api/prof-finance/history`、`/api/prof-finance/snapshot`、`/api/prof-finance/cross-section`、`/api/prof-finance/coverage` 全部可用
- 所有查询接口严格使用 `full_code` / `full_codes`、`field_codes`、`field_values`
- 所有查询接口具备明确时间语义，不产生未来函数
- 所有缺失值与覆盖状态显式表达
- `/api/profile` 与统一专业财务层保持一致
- 测试通过，文档完成更新

---

## Acceptance Criteria

### Story 1: 字段发现

**As a** 投研工程师  
**I want to** 先查询专业财务字段目录  
**So that** 我能稳定知道字段名、含义、单位和口径

**Acceptance Criteria**

- [ ] `/api/prof-finance/fields` 可列出全部 `403` 个专业财务 `source_field_id` 定义
- [ ] 字段目录支持按 `category` 过滤，支持按 `query` 搜索
- [ ] 每个字段目录项必须返回 `field_code`、`source_field_id`、`field_name_cn`、`field_name_en`、`category`、`statement`、`period_semantics`、`unit`、`value_type`、`source`、`supported`
- [ ] 所有 `source_field_id` 在字段目录中唯一且可追溯，不允许静默遗漏
- [ ] 尚未进入公开查询 contract 的字段仍必须在字段目录中出现，并显式标记 `supported=false`

### Story 2: 单证券快照

**As a** 研究系统调用方  
**I want to** 查询一只股票最新可用专业财务快照  
**So that** 我能直接做个股分析

**Acceptance Criteria**

- [ ] `/api/prof-finance/snapshot` 的请求只接受 `full_code`、`report_date`、`as_of_date`、`field_codes`、`period_mode`
- [ ] 响应必须返回 `full_code`、`name`、`report_date`、`announce_date`、`as_of_date`、`knowledge_cutoff`、`source`、`field_values`、`missing_fields`、`coverage`
- [ ] `field_values` 的 key 必须全部来自字段目录中的公开 `field_code`
- [ ] 查询结果必须受 `as_of_date` 约束，不得返回在 `knowledge_cutoff` 之后才可见的数据
- [ ] 缺失值不得用 `0` 代替，字段缺失必须通过 `missing_fields` 与 `coverage` 显式表达

### Story 3: 横截面比较

**As a** 量化研究员  
**I want to** 一次查询多只股票同一报告期的多个财务字段  
**So that** 我能做横截面比较和选股

**Acceptance Criteria**

- [ ] `/api/prof-finance/cross-section` 的请求只接受 `full_codes`、`report_date`、`as_of_date`、`field_codes`、`period_mode`
- [ ] 响应必须返回 `report_date`、`as_of_date`、`knowledge_cutoff`、`field_codes`、`items`
- [ ] `items[]` 中每个证券项必须返回 `full_code`、`name`、`field_values`、`missing_fields`、`coverage`
- [ ] 横截面接口与字段目录接口必须使用同一套 `field_code` 命名，不允许出现只在某个接口存在的私有字段名
- [ ] 无覆盖证券或字段缺失不得被静默丢弃，必须在对应证券项中显式表达

### Story 4: 历史报告序列

**As a** 基本面研究员  
**I want to** 查询单只证券多期财务历史  
**So that** 我能做趋势分析

**Acceptance Criteria**

- [ ] `/api/prof-finance/history` 的请求只接受 `full_code`、`field_codes`、`as_of_date`、`start_report_date`、`end_report_date`、`limit`、`period`
- [ ] 响应必须返回 `full_code`、`name`、`as_of_date`、`knowledge_cutoff`、`field_codes`、`list`
- [ ] `list[]` 中每期必须返回 `report_date`、`announce_date`、`field_values`、`missing_fields`、`source_report_file`
- [ ] 历史列表必须按 `report_date` 倒序返回
- [ ] 历史查询必须受 `as_of_date` 约束，不得返回在 `knowledge_cutoff` 之后才可见的报告值
- [ ] 历史接口必须覆盖全部已公开 `field_code`，而不是只覆盖当前已接入的少数字段

### Story 5: 覆盖状态查询

**As a** 研究系统调用方
**I want to** 明确知道某只证券在专业财务链路上的可用性和缺失原因
**So that** 我能区分无覆盖、字段缺失和时点不可见

**Acceptance Criteria**

- [ ] `/api/prof-finance/coverage` 的请求只接受 `full_code`、`report_date`
- [ ] 响应必须返回 `full_code`、`name`、`latest_report_date`、`available_reports`、`available_field_codes`、`missing_fields`、`status`、`knowledge_cutoff`
- [ ] `status` 必须能够区分有覆盖、无覆盖、字段缺失或报告期不可用等状态
- [ ] 覆盖状态不得用 `0`、空对象或静默缺字段来表达
- [ ] 对于不可用结果，响应必须能让调用方直接判断不可用原因，而不是依赖额外推断

---

## Risks

- `gpcw` 字段体系版本可能演进，字段映射需要版本校验
- 公告日期与“可得时点”目前并未完整接入，需要单独补强
- 某些 raw finance 与 professional finance 字段口径不完全一致，不能简单混写

---

## Recommendation

不要继续把 `gpcw` 能力零散地补到 `/api/finance` 和 `/api/profile`。  
正确做法是把它提升成一组独立的 `professional finance API family`，由字段目录驱动，以横截面、快照、历史序列为核心能力，再由 `/api/profile` 等轻量接口按需消费其结果。

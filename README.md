# 📈 TDX通达信股票数据查询系统

> 基于通达信协议的股票数据获取库 + Web可视化界面 + RESTful API

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go)](https://golang.org)
[![Docker](https://img.shields.io/badge/Docker-支持-2496ED?style=flat&logo=docker)](https://www.docker.com)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

**感谢源作者 [injoyai](https://github.com/injoyai/tdx)，请支持原作者！**

---

## ✨ 功能特性

| 分类 | 功能 |
|-----|------|
| **📊 核心功能** | 实时行情（五档盘口）、K线数据（10种周期）、分时数据、股票搜索、批量查询 |
| **🌐 Web界面** | 现代化UI、ECharts图表、智能搜索、实时刷新 |
| **🔌 RESTful API** | 32个接口、完整文档、多语言示例、高性能 |
| **🐳 Docker部署** | 开箱即用、国内镜像加速、跨平台支持 |

---

## 🚀 快速开始

### 方式一：Docker部署（推荐）⭐

```bash
# 克隆项目
git clone https://github.com/oficcejo/tdx-api.git
cd tdx-api

# 可选：复制部署变量模板
cp .env.example .env

# 首次在本机上构建最终镜像
./scripts/build_local_image.sh

# 启动服务
docker compose up -d

# 访问 http://localhost:8080
```

**一键启动脚本：**
- Windows: 双击 `docker-start.bat`
- Linux/Mac: `chmod +x docker-start.sh && ./docker-start.sh`

### 方式二：源码运行

```bash
# 前置要求: Go 1.22+

# 1. 下载依赖
go mod download

# 2. 进入web目录并运行
cd web
go run .

# 3. 访问 http://localhost:8080
```

> ⚠️ **注意**: 必须使用 `go run .` 编译所有Go文件，不能使用 `go run server.go`

---

## � API接口列表

### 核心接口

| 接口 | 说明 | 示例 |
|-----|------|------|
| `/api/quote` | 五档行情 | `?code=000001` |
| `/api/kline` | K线数据 | `?code=000001&type=day` |
| `/api/minute` | 分时数据 | `?code=000001` |
| `/api/trade` | 分时成交 | `?code=000001` |
| `/api/search` | 跨资产证券搜索 | `?keyword=平安&asset_type=all` |
| `/api/security/status` | 证券可交易状态 | `?code=000001` |
| `/api/profile` | 证券轻量快照（基础属性 + 当前行情 + 常用估值字段） | `?code=000001` |
| `/api/stock-info` | 综合信息 | `?code=000001` |

### 扩展接口

| 接口 | 说明 |
|-----|------|
| `/api/codes` | 获取股票代码列表 |
| `/api/batch-quote` | 批量获取行情 |
| `/api/kline-history` | 历史K线数据 |
| `/api/kline-all` | 完整K线数据 |
| `/api/kline-all/tdx` | TDX源K线数据 |
| `/api/kline-all/ths` | 同花顺源K线数据（含前复权） |
| `/api/index` | 指数数据 |
| `/api/index/all` | 全部指数数据 |
| `/api/market-stats` | 全市场宽度统计 |
| `/api/market/screen` | 全市场排行 / 涨跌停池 |
| `/api/market/signal` | K 线扫描异动 |
| `/api/market-count` | 市场数量统计 |
| `/api/stock-codes` | 股票代码 |
| `/api/etf-codes` | ETF代码 |
| `/api/index-codes` | 指数代码 |
| `/api/etf` | ETF列表 |
| `/api/finance` | 财务数据 |
| `/api/financial-reports` | 多期财报摘要 |
| `/api/f10/categories` | F10目录 |
| `/api/f10/content` | F10正文 |
| `/api/trade-history` | 历史成交 |
| `/api/order-history` | 历史委托分布 |
| `/api/trade-history/full` | 完整历史成交 |
| `/api/minute-trade-all` | 全部分时成交 |
| `/api/workday` | 交易日查询 |
| `/api/workday/range` | 交易日范围 |
| `/api/income` | 收益数据 |
| `/api/tasks/pull-kline` | 创建K线拉取任务 |
| `/api/tasks/pull-trade` | 创建成交拉取任务 |
| `/api/tasks` | 任务列表 |
| `/api/server-status` | 服务器状态 |
| `/api/health` | 健康检查 |
| `/api/collector/status` | 数据采集器运行状态 |
| `/api/collector/reconcile` | 查看/触发数据对账 |

**完整API文档**: [API_接口文档.md](API_接口文档.md)

### 指数池配置

服务默认会从交易所代码表自动识别全量指数，也支持自定义覆盖：

- 配置文件：`data/database/index_codes.json`
- 环境变量：`TDX_INDEX_CODES=sh000001,sh000300,sz399006`

`index_codes.json` 支持两种格式：

```json
["sh000001", "sh000300", "sz399006"]
```

```json
[
  { "code": "sh000001", "name": "上证指数" },
  { "code": "sh000300", "name": "沪深300" }
]
```

指数代码必须带交易所前缀，例如 `sh000001`、`sz399001`。

---

## � 使用示例

### API调用

```bash
# 获取实时行情
curl "http://localhost:8080/api/quote?code=000001"

# 获取日K线
curl "http://localhost:8080/api/kline?code=000001&type=day"

# 搜索股票
curl "http://localhost:8080/api/search?keyword=平安"

# 获取财务数据
curl "http://localhost:8080/api/finance?code=000001"

# 获取F10目录
curl "http://localhost:8080/api/f10/categories?code=000001"

# 获取指数代码
curl "http://localhost:8080/api/index-codes"

# 创建混合证券K线任务
curl -X POST "http://localhost:8080/api/tasks/pull-kline" \
  -H "Content-Type: application/json" \
  -d '{
    "asset_types": ["stock", "etf", "index"],
    "index_codes": ["sh000001", "sh000300", "sz399006"],
    "tables": ["day"],
    "limit": 1
  }'

# 创建股票/ETF成交采集任务
curl -X POST "http://localhost:8080/api/tasks/pull-trade" \
  -H "Content-Type: application/json" \
  -d '{
    "asset_types": ["stock", "etf"],
    "limit": 2,
    "start_year": 2025,
    "end_year": 2026
  }'

# 获取历史委托分布
curl "http://localhost:8080/api/order-history?code=000001&date=2026-03-31"

# 健康检查
curl "http://localhost:8080/api/health"
```

### Go库使用

```go
import "github.com/injoyai/tdx"

// 连接服务器
c, _ := tdx.DialDefault(tdx.WithDebug(false))

// 获取行情
quotes, _ := c.GetQuote("000001", "600519")

// 获取日K线
kline, _ := c.GetKlineDayAll("000001")
```

---

## � Docker配置说明

### 当前推荐流程

- **构建**：用 [`scripts/build_local_image.sh`](scripts/build_local_image.sh) 生成最终应用镜像
- **部署**：用 [`docker-compose.yml`](docker-compose.yml) 直接启动该镜像
- **数据**：宿主机目录挂载到容器，不会被打进镜像

### 常用命令

```bash
./scripts/build_local_image.sh      # 本机构建最终镜像
docker compose up -d                # 启动服务
docker compose logs -f              # 查看日志
docker compose stop                 # 停止服务
docker compose restart              # 重启服务
docker compose down                 # 停止并删除容器
./scripts/save_image.sh             # 导出镜像，便于迁移到VPS
```

`.env.example` 中提供了两个常用变量：

- `TDX_STOCK_WEB_IMAGE`：部署时使用的镜像名
- `TDX_DATA_DIR`：宿主机数据目录，默认 `./data/database`

**唯一 Docker 文档入口**: [DOCKER_DEPLOY.md](DOCKER_DEPLOY.md)

---

## 📊 支持的数据类型

| 数据类型 | 方法 | 说明 |
|---------|------|------|
| 五档行情 | `GetQuote` | 实时买卖五档、最新价、成交量 |
| 1/5/15/30/60分钟K线 | `GetKlineXXXAll` | 分钟级K线数据 |
| 日/周/月K线 | `GetKlineDayAll` 等 | 中长期K线数据 |
| 分时数据 | `GetMinute` | 当日每分钟价格 |
| 分时成交 | `GetTrade` | 逐笔成交记录 |
| 股票列表 | `GetCodeAll` | 全市场代码 |

---

## 📁 项目结构

```
tdx-api/
├── client.go              # TDX客户端核心
├── protocol/              # 通达信协议实现
├── web/                   # Web应用
│   ├── server.go          # 主服务器
│   ├── server_api_extended.go  # 扩展API
│   ├── tasks.go           # 任务管理
│   └── static/            # 前端文件
├── extend/                # 扩展功能
├── Dockerfile             # Docker镜像（国内源）
├── docker-compose.yml     # Docker编排
└── docs/                  # 文档
```

---

## � 相关资源

| 资源 | 链接 |
|-----|------|
| 原项目 | [injoyai/tdx](https://github.com/injoyai/tdx) |
| API文档 | [API_接口文档.md](API_接口文档.md) |
| Docker部署 | [DOCKER_DEPLOY.md](DOCKER_DEPLOY.md) |
| Python示例 | [API_使用示例.py](API_使用示例.py) |

### 通达信服务器

系统自动连接最快的服务器：

| IP | 地区 |
|----|------|
| 124.71.187.122 | 上海(华为) |
| 122.51.120.217 | 上海(腾讯) |
| 121.36.54.217 | 北京(华为) |
| 124.71.85.110 | 广州(华为) |

---

## ⚠️ 免责声明

1. 本项目仅供学习和研究使用
2. 数据来源于通达信公共服务器，可能存在延迟
3. 不构成任何投资建议，投资有风险

---

## 📄 许可证

MIT License - 详见 [LICENSE](LICENSE)

---

**如果这个项目对您有帮助，请点个 Star ⭐ 支持一下！**

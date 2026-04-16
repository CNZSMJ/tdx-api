# Docker 部署指南

更新时间：2026-04-10

## 1. 先讲清两个角色

- **构建机**：负责把源码构建成最终 Docker 镜像的机器。
- **部署机**：负责把最终 Docker 镜像跑起来的机器。

它们可以是同一台机器。

对当前仓库，推荐流程是：

1. 用 `Dockerfile` 直接构建最终应用镜像。
2. 用 `docker-compose.yml` 只做部署，不在 compose 里触发构建。
3. 把数据库目录挂载到宿主机，不打进镜像。

这样现在你可以在本机同时构建和部署；以后迁移到 VPS 时，只需要把“构建”留在本地，把“部署”放到 VPS。

## 2. 这套方案解决了什么

- 不再依赖历史遗留的 `runtime-base` 本地基础镜像。
- 不再使用 `docker commit` 作为正式构建流程。
- 不会把 `data/database` 烘进镜像。
- 部署机只需要最终镜像，不需要 Go 环境。
- 本机和 VPS 使用同一份 `docker-compose.yml`。

## 3. 仓库里的关键文件

- `Dockerfile`：标准多阶段构建，产出最终应用镜像。
- `docker-compose.yml`：只负责部署镜像和挂载数据目录。
- `.env.example`：镜像名和宿主机数据目录变量模板。
- `scripts/build_local_image.sh`：本机或构建机用来生成最终镜像。
- `scripts/save_image.sh`：把最终镜像导出成 tar，便于传到 VPS。

## 4. 单机使用：本机既构建又部署

### 4.1 前置要求

- Docker 已安装并能正常运行。
- 推荐 Docker 自带 `buildx`；脚本会优先使用它。

### 4.2 首次构建镜像

```bash
cp .env.example .env
./scripts/build_local_image.sh
```

默认会构建：

```bash
tdx-api-stock-web:invest-grade-fixed
```

如果你想显式指定架构，例如本机是 Apple Silicon，但未来准备部署到 Linux/amd64 的 VPS：

```bash
TARGET_ARCH=amd64 ./scripts/build_local_image.sh
```

如果你希望强制刷新基础镜像：

```bash
PULL_BASE_IMAGES=1 ./scripts/build_local_image.sh
```

### 4.3 启动服务

```bash
docker compose up -d
```

### 4.4 查看状态

```bash
docker compose ps
docker compose logs -f
curl -sS http://localhost:8080/api/health
```

### 4.5 停止服务

```bash
docker compose down
```

## 5. 迁移到 VPS：本地构建，VPS 只部署

这是后续最推荐的方式。

### 5.1 在本机构建最终镜像

如果 VPS 是常见的 Linux/amd64：

```bash
TARGET_ARCH=amd64 ./scripts/build_local_image.sh
```

### 5.2 导出镜像

```bash
IMAGE=tdx-api-stock-web:invest-grade-fixed ./scripts/save_image.sh
```

默认会输出到：

```bash
dist/images/tdx-api-stock-web__invest-grade-fixed.tar
```

### 5.3 把镜像 tar 传到 VPS

示例：

```bash
scp dist/images/tdx-api-stock-web__invest-grade-fixed.tar user@your-vps:/tmp/
```

### 5.4 在 VPS 上加载镜像

```bash
docker load -i /tmp/tdx-api-stock-web__invest-grade-fixed.tar
```

### 5.5 准备部署目录

VPS 上至少需要这些文件：

- `docker-compose.yml`
- `.env` 或 `.env.example`

推荐将数据目录放在仓库外，例如：

```bash
mkdir -p /srv/tdx-api/database
```

然后在 `.env` 中设置：

```bash
TDX_STOCK_WEB_IMAGE=tdx-api-stock-web:invest-grade-fixed
TDX_DATA_DIR=/srv/tdx-api/database
```

### 5.6 启动 VPS 服务

```bash
docker compose up -d
```

## 6. compose 现在的职责

当前 `docker-compose.yml` 只有两件事：

1. 启动指定镜像。
2. 把宿主机数据库目录挂到 `/app/data/database`。

它不会再默认帮你构建镜像。

这正是为了避免：

- 部署机磁盘被 build cache 吃满；
- 构建和部署耦合在一起；
- 一台网络不稳定的机器每次重启服务都去重新 build。

## 7. 常用环境变量

### `TDX_STOCK_WEB_IMAGE`

部署时使用的镜像名，默认：

```bash
tdx-api-stock-web:invest-grade-fixed
```

### `TDX_DATA_DIR`

宿主机数据目录，默认：

```bash
./data/database
```

在单机上，这意味着数据保存在仓库下。  
在 VPS 上，更推荐改成绝对路径，例如 `/srv/tdx-api/database`。

## 8. 镜像为什么会保持轻量

当前正式构建流程中，镜像只包含：

- 编译后的 `stock-web`
- 前端静态资源 `web/static`
- 最小运行时依赖

镜像不会包含：

- `data/database`
- 本地 SQLite 文件
- 临时归档包
- 仓库里的 `dist/`

这依赖两件事共同保证：

1. `.dockerignore` 已排除运行时数据和大文件。
2. `docker-compose.yml` 使用宿主机挂载而不是把数据打进镜像。

## 9. 为什么不再保留“本地 runtime-base”方案

之前的方案本质是为了绕开某台机器上不稳定的远端拉镜像链路。它适合救火，不适合作为长期正式流程。

长期问题有三类：

- 很容易把历史文件层和数据库层带进基础镜像。
- 一旦基础镜像被污染，后续所有派生镜像都会跟着膨胀。
- 部署机必须理解并维护一张“特殊本地基础镜像”，迁移和交付都不干净。

所以现在的正式方案改成：

- **构建阶段**：标准 Docker 多阶段构建。
- **部署阶段**：只运行最终镜像。

## 10. 什么时候还需要基础镜像

只有在你以后明确建立“统一构建环境”时，才值得单独维护一个小型 runtime base，例如：

- 固定基础运行环境版本；
- 复用企业内镜像仓库；
- 多个服务共享同一运行时层。

即便如此，它也应该只属于“构建链路内部”，而不是“部署机必须自己准备的一张本地特殊镜像”。

## 11. 常见问题

### 问题 1：`docker compose up -d` 报找不到镜像

说明你还没在当前机器上准备最终镜像。

本机构建：

```bash
./scripts/build_local_image.sh
```

或者从其他机器导入：

```bash
docker load -i your-image.tar
```

### 问题 2：我在 Apple Silicon 本机构建，目标是 x86 VPS

直接指定：

```bash
TARGET_ARCH=amd64 ./scripts/build_local_image.sh
```

如果本机 Docker 缺少跨架构支持，请先确认 `docker buildx version` 可用。

### 问题 3：为什么不建议在 VPS 上直接 `docker build`

因为 VPS 更适合承担“稳定运行”职责，而不是承担“源码编译 + 拉基础镜像 + 保留 build cache”职责。把构建留在本地或 CI，能让 VPS 更轻、更稳定、更容易迁移。

## 12. 当前推荐命令清单

本机构建镜像：

```bash
./scripts/build_local_image.sh
```

本机启动服务：

```bash
docker compose up -d
```

导出镜像给 VPS：

```bash
./scripts/save_image.sh
```

VPS 导入镜像：

```bash
docker load -i dist/images/tdx-api-stock-web__invest-grade-fixed.tar
```

查看服务日志：

```bash
docker compose logs -f
```

## 13. 快速参考

启动服务：

```bash
docker compose up -d
```

停止服务：

```bash
docker compose stop
```

重启服务：

```bash
docker compose restart
```

查看状态：

```bash
docker compose ps
docker stats tdx-stock-web
```

查看日志：

```bash
docker compose logs -f
docker compose logs --tail=100
docker logs -f tdx-stock-web
```

进入容器：

```bash
docker exec -it tdx-stock-web sh
```

导出镜像：

```bash
./scripts/save_image.sh
```

导入镜像：

```bash
docker load -i your-image.tar
```

## 14. 日常排查

确认容器健康：

```bash
docker compose ps
curl -sS http://localhost:8080/api/health
```

确认服务接口：

```bash
curl -sS "http://localhost:8080/api/profile?code=sh600000"
```

检查端口占用：

```bash
# macOS / Linux
lsof -i :8080

# Linux 备选
netstat -tulpn | grep :8080
```

查看 Docker 总占用：

```bash
docker system df
```

清理构建缓存：

```bash
docker builder prune -af
```

说明：这条命令不会影响正在运行的容器，但会让下次构建重新生成缓存。

## 15. 为什么项目现在只保留这一份 Docker 文档

仓库之前同时存在：

- 详细部署说明；
- 快速参考卡；
- 部署完成说明；
- 一份排障复盘文档。

它们来自不同时期，内容已经出现明显分叉，例如：

- 有的文档还在写 `docker-compose build` / `docker-compose up -d --build`；
- 有的文档假设 compose 会自动构建镜像；
- 有的文档描述的是旧的本地 `runtime-base` 方案。

当前仓库的正式方案已经统一成：

1. `Dockerfile` 负责构建最终镜像。
2. `docker-compose.yml` 只负责部署最终镜像。
3. `data/database` 始终走宿主机挂载。
4. 本地和 VPS 共享同一套部署思路。

所以仓库里只保留这一份 `DOCKER_DEPLOY.md` 作为唯一 Docker 文档入口，避免后续再出现“文档之间互相矛盾”的情况。

## 16. 历史背景摘要

之前这台机器曾出现过 Docker 相关故障，核心不是某条命令写错，而是旧链路默认依赖：

- 远端基础镜像总能稳定拉取；
- `docker build` 总能顺利走完；
- 本地基础镜像不会被历史数据污染。

这些前提一旦失效，就会出现：

- 构建卡住；
- 镜像越来越大；
- 数据目录被错误烘进镜像；
- 本机磁盘被 Docker 镜像层和缓存迅速吃满。

当前方案就是为了解决这些问题，确保：

- 运行镜像保持轻量；
- 部署机只负责运行，不负责长期堆积构建历史；
- 以后迁移到 VPS 时不需要重新设计部署方式。

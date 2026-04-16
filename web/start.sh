#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "${ROOT_DIR}/.." && pwd)"
PID_FILE="${ROOT_DIR}/.stock-web.pid"
LOG_FILE="${ROOT_DIR}/.stock-web.log"
DEFAULT_PORT="${TDX_WEB_PORT:-8080}"
ENV_FILE="${TDX_WEB_ENV_FILE:-${PROJECT_DIR}/.env}"
DEFAULT_BIN="${ROOT_DIR}/stock-web"

usage() {
  cat <<'EOF'
Usage:
  ./start.sh start
  ./start.sh stop
  ./start.sh restart
  ./start.sh status
  ./start.sh run
  ./start.sh logs

Commands:
  start    Start stock-web in background and write PID to .stock-web.pid
  stop     Stop the managed background process
  restart  Restart the managed background process
  status   Show whether the managed process is running
  run      Run stock-web in foreground
  logs     Tail the current service log

Environment:
  TDX_WEB_ENV_FILE  Optional env file to source before start/run
EOF
}

print_banner() {
  echo "========================================"
  echo "  股票数据查询Web系统"
  echo "========================================"
}

read_pid_file() {
  if [[ -f "${PID_FILE}" ]]; then
    tr -d '[:space:]' < "${PID_FILE}"
  fi
}

is_running_pid() {
  local pid="$1"
  [[ -n "${pid}" ]] || return 1
  kill -0 "${pid}" 2>/dev/null
}

command_for_pid() {
  local pid="$1"
  ps -p "${pid}" -o command= 2>/dev/null | sed 's/^[[:space:]]*//'
}

find_port_pid() {
  lsof -ti "tcp:${DEFAULT_PORT}" 2>/dev/null | head -n 1 || true
}

cleanup_stale_pid_file() {
  local pid
  pid="$(read_pid_file)"
  if [[ -n "${pid}" ]] && ! is_running_pid "${pid}"; then
    rm -f "${PID_FILE}"
  fi
}

load_runtime_env() {
  if [[ -f "${ENV_FILE}" ]]; then
    set -a
    # shellcheck disable=SC1090
    source "${ENV_FILE}"
    set +a
  fi

  export TZ="${TZ:-Asia/Shanghai}"
}

launch_background_process() {
  local -a cmd
  local pid=""

  if [[ -x "${DEFAULT_BIN}" ]]; then
    cmd=("${DEFAULT_BIN}")
  else
    cmd=(go run .)
  fi

  if command -v python3 >/dev/null 2>&1; then
    pid="$(
      python3 - "${LOG_FILE}" "${cmd[@]}" <<'PY'
import subprocess
import sys

log_path = sys.argv[1]
cmd = sys.argv[2:]

with open(log_path, "ab", buffering=0) as log_file:
    proc = subprocess.Popen(
        cmd,
        stdin=subprocess.DEVNULL,
        stdout=log_file,
        stderr=log_file,
        close_fds=True,
        start_new_session=True,
    )

print(proc.pid)
PY
    )"
    if [[ -n "${pid}" ]]; then
      printf '%s\n' "${pid}"
      return 0
    fi
  fi

  if command -v setsid >/dev/null 2>&1; then
    nohup setsid "${cmd[@]}" < /dev/null >> "${LOG_FILE}" 2>&1 &
    printf '%s\n' "$!"
    return 0
  fi

  nohup "${cmd[@]}" < /dev/null >> "${LOG_FILE}" 2>&1 &
  printf '%s\n' "$!"
}

run_foreground_process() {
  if [[ -x "${DEFAULT_BIN}" ]]; then
    exec "${DEFAULT_BIN}"
  fi

  exec go run .
}

start_service() {
  cleanup_stale_pid_file

  local pid
  pid="$(read_pid_file)"
  if [[ -n "${pid}" ]] && is_running_pid "${pid}"; then
    echo "服务已在运行: PID ${pid}"
    return 0
  fi

  local port_pid
  port_pid="$(find_port_pid)"
  if [[ -n "${port_pid}" ]]; then
    echo "端口 ${DEFAULT_PORT} 已被占用: PID ${port_pid}"
    echo "命令: $(command_for_pid "${port_pid}")"
    return 1
  fi

  print_banner
  echo "正在后台启动服务器..."
  echo "日志文件: ${LOG_FILE}"

  pid="$(
    cd "${ROOT_DIR}"
    load_runtime_env
    launch_background_process
  )"

  if [[ -z "${pid}" ]]; then
    echo "启动失败，未获取到后台进程 PID" >&2
    return 1
  fi

  printf '%s\n' "${pid}" > "${PID_FILE}"

  sleep 1

  if [[ -n "${pid}" ]] && is_running_pid "${pid}"; then
    echo "启动成功: PID ${pid}"
    return 0
  fi

  echo "启动失败，请检查日志: ${LOG_FILE}" >&2
  rm -f "${PID_FILE}"
  return 1
}

stop_service() {
  cleanup_stale_pid_file

  local pid
  pid="$(read_pid_file)"
  if [[ -z "${pid}" ]]; then
    local port_pid
    port_pid="$(find_port_pid)"
    if [[ -z "${port_pid}" ]]; then
      echo "服务未运行"
      return 0
    fi
    echo "未找到 PID 文件，按端口 ${DEFAULT_PORT} 停止进程: PID ${port_pid}"
    echo "命令: $(command_for_pid "${port_pid}")"
    kill -TERM "${port_pid}" 2>/dev/null || true
    sleep 1
    if is_running_pid "${port_pid}"; then
      echo "进程未退出，发送 SIGKILL"
      kill -KILL "${port_pid}" 2>/dev/null || true
    fi
    echo "已停止"
    return 0
  fi

  if ! is_running_pid "${pid}"; then
    rm -f "${PID_FILE}"
    echo "服务未运行"
    return 0
  fi

  echo "正在停止服务: PID ${pid}"
  kill -TERM "${pid}" 2>/dev/null || true

  local i
  for i in {1..10}; do
    if ! is_running_pid "${pid}"; then
      rm -f "${PID_FILE}"
      echo "已停止"
      return 0
    fi
    sleep 1
  done

  echo "服务未在预期时间内退出，发送 SIGKILL"
  kill -KILL "${pid}" 2>/dev/null || true
  rm -f "${PID_FILE}"
  echo "已强制停止"
}

status_service() {
  cleanup_stale_pid_file

  local pid
  pid="$(read_pid_file)"
  if [[ -n "${pid}" ]] && is_running_pid "${pid}"; then
    echo "运行中: PID ${pid}"
    echo "命令: $(command_for_pid "${pid}")"
    return 0
  fi

  local port_pid
  port_pid="$(find_port_pid)"
  if [[ -n "${port_pid}" ]]; then
    echo "未由 start.sh 管理，但端口 ${DEFAULT_PORT} 正被占用: PID ${port_pid}"
    echo "命令: $(command_for_pid "${port_pid}")"
    return 1
  fi

  echo "未运行"
}

run_foreground() {
  print_banner
  echo "正在前台启动服务器..."
  cd "${ROOT_DIR}"
  load_runtime_env
  run_foreground_process
}

tail_logs() {
  touch "${LOG_FILE}"
  tail -f "${LOG_FILE}"
}

main() {
  local action="${1:-start}"

  case "${action}" in
    start)
      start_service
      ;;
    stop)
      stop_service
      ;;
    restart)
      stop_service
      start_service
      ;;
    status)
      status_service
      ;;
    run)
      run_foreground
      ;;
    logs)
      tail_logs
      ;;
    -h|--help|help)
      usage
      ;;
    *)
      echo "未知命令: ${action}" >&2
      usage >&2
      exit 1
      ;;
  esac
}

main "${@}"

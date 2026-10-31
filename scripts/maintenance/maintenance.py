#!/usr/bin/env python3
"""AxonHub 的通用宿主机维护入口。配置和执行记录应放在 Git 忽略目录。

仅 status/verify 为只读。export/restore/expire-exports 由运维主动执行，
不安装定时任务、不猜测凭据、不删除 Docker 卷、不覆盖正在运行的数据库。
"""

import argparse
import contextlib
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import shutil
import subprocess
import sys
import tempfile


GIB = 1024 ** 3
IDENTIFIER = re.compile(r"[A-Za-z_][A-Za-z0-9_]{0,62}\Z")


def load_config(path):
    config = json.loads(Path(path).read_text(encoding="utf-8"))
    if not isinstance(config, dict):
        raise ValueError("配置必须是 JSON 对象")
    return config


def run(argv, *, stdin=None, stdout=None, timeout=60):
    # 元数据输出落临时文件，最多读 8 MiB；导出直接写目标文件，不在内存聚合。
    # stderr 可能包含数据库内容或连接信息，不回显或保存到公共日志。
    with tempfile.TemporaryFile() as captured:
        with subprocess.Popen(argv, stdin=stdin, stdout=stdout if stdout is not None else captured,
                              stderr=subprocess.DEVNULL, start_new_session=os.name == "posix") as process:
            try:
                process.wait(timeout=timeout)
            except subprocess.TimeoutExpired:
                if os.name == "posix":
                    os.killpg(process.pid, signal.SIGKILL)
                else:
                    process.kill()
                process.wait()
                raise RuntimeError(f"{Path(argv[0]).name} 超时；本机执行进程已停止") from None
            if process.returncode:
                raise RuntimeError(f"{Path(argv[0]).name} 失败（退出码 {process.returncode}），请在本机检查服务日志")
        if stdout is not None:
            return b""
        captured.seek(0)
        output = captured.read(8 * 1024 * 1024 + 1)
        if len(output) > 8 * 1024 * 1024:
            raise RuntimeError("维护命令的元数据输出超过 8 MiB")
        return output


def require_local_execution(settings):
    # docker/ssh 客户端退出不保证远端作业退出，也不继承本机 cgroup 限额。
    if settings.get("exec_prefix"):
        raise ValueError("写入维护必须在实际执行环境中运行本脚本，不能通过 exec_prefix 启动远端或容器内作业；数据库可用原生客户端直连")


def postgres_command(config, executable, arguments):
    pg = config["postgres"]
    prefix = list(pg.get("exec_prefix", []))
    if not all(isinstance(item, str) for item in prefix):
        raise ValueError("exec_prefix 必须为命令参数数组")
    return prefix + [executable, "--username", pg.get("user", "postgres")] + arguments


def sql(config, statement, database=None):
    database = database or config["postgres"]["database"]
    return run(postgres_command(config, "psql", ["--no-psqlrc", "--no-align", "--tuples-only",
               "--set", "ON_ERROR_STOP=1", "--dbname", database, "--command", statement])).decode().strip()


def capacity(config):
    result = []
    for configured in config["monitor_paths"]:
        path = Path(configured).resolve(strict=True)
        usage = shutil.disk_usage(path)
        automatic = max(2 * GIB, usage.total * 0.15)
        reserve = config.get("reserve_gib", 0) * GIB or automatic
        percent = (usage.total - usage.free) / usage.total * 100
        level = "ok"
        if percent >= config.get("warning_percent", 70) or usage.free < reserve:
            level = "warning"
        if percent >= config.get("critical_percent", 85) or usage.free < reserve / 2:
            level = "critical"
        entry = dict(path=str(path), total_bytes=usage.total, free_bytes=usage.free,
                     used_percent=round(percent, 2), reserve_bytes=int(reserve), level=level)
        if hasattr(os, "statvfs"):
            stat = os.statvfs(path)
            if stat.f_files:
                entry["inode_used_percent"] = round((stat.f_files - stat.f_favail) / stat.f_files * 100, 2)
                if entry["inode_used_percent"] >= config.get("critical_percent", 85):
                    entry["level"] = "critical"
        result.append(entry)
    return result


def status(config):
    result = {"sampled_at": dt.datetime.now(dt.timezone.utc).isoformat(), "filesystems": capacity(config)}
    if config.get("postgres"):
        result["database_bytes"] = int(sql(config, "SELECT pg_database_size(current_database());"))
        # 只读目录统计，不读取请求正文、连接串或环境变量。
        result["database_tables"] = json.loads(sql(config, """
          SELECT COALESCE(json_agg(x),'[]'::json) FROM (
            SELECT relname AS name, pg_total_relation_size(relid) AS bytes,
                   n_live_tup, n_dead_tup, last_autovacuum
            FROM pg_stat_user_tables WHERE schemaname=current_schema()
            ORDER BY pg_total_relation_size(relid) DESC LIMIT 15
          ) x;"""))
        result["wal_archiving"] = json.loads(sql(config, """
          SELECT json_build_object('archived_count', archived_count, 'failed_count', failed_count,
            'last_archived_time', last_archived_time, 'last_failed_time', last_failed_time,
            'error_pending', last_failed_time IS NOT NULL AND
               (last_archived_time IS NULL OR last_failed_time > last_archived_time)) FROM pg_stat_archiver;"""))
    return result


def require_capacity(config, destination, extra_bytes=0):
    usage = shutil.disk_usage(destination)
    reserve = config.get("reserve_gib", 0) * GIB or max(2 * GIB, usage.total * 0.15)
    if usage.free < reserve + extra_bytes:
        raise RuntimeError("剩余空间不足以同时容纳本次操作与应急预留；请求服务未被修改")


@contextlib.contextmanager
def maintenance_lock(config):
    directory = Path(config["state_directory"]).resolve()
    directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    file = (directory / "maintenance.lock").open("a+b")
    try:
        if os.name == "nt":
            import msvcrt
            file.seek(0)
            if not file.read(1):
                file.write(b"0"); file.flush()
            file.seek(0)
            msvcrt.locking(file.fileno(), msvcrt.LK_NBLCK, 1)
        else:
            import fcntl
            fcntl.flock(file.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        yield
    except BlockingIOError as error:
        raise RuntimeError("已有宿主机维护任务执行中") from error
    finally:
        file.close()  # OS 锁随进程退出释放；不删除其他执行者使用的锁文件。


def digest(path):
    value = hashlib.sha256()
    with path.open("rb") as file:
        for block in iter(lambda: file.read(1024 * 1024), b""):
            value.update(block)
    return value.hexdigest()


def export_database(config):
    require_local_execution(config["postgres"])
    root = Path(config["export_directory"]).resolve(strict=True)
    size = int(sql(config, "SELECT pg_database_size(current_database());"))
    require_capacity(config, root, size * 1.2)
    name = "axonhub-export-" + dt.datetime.now(dt.timezone.utc).strftime("%Y%m%dT%H%M%S.%fZ") + ".dump"
    target = root / name
    fd, temporary = tempfile.mkstemp(prefix=".axonhub-export-", suffix=".partial", dir=root)
    temporary = Path(temporary)
    try:
        with os.fdopen(fd, "wb") as output:
            run(postgres_command(config, "pg_dump", ["--format=custom", "--compress=6", "--no-owner", "--no-acl",
                "--lock-wait-timeout=3s", "--dbname", config["postgres"]["database"]]), stdout=output,
                timeout=config.get("operation_timeout_seconds", 3600))
            output.flush(); os.fsync(output.fileno())
        checksum = digest(temporary)
        # Destination is unique and the process lock serializes local exports.
        if target.exists():
            raise RuntimeError("导出目标已存在")
        temporary.rename(target)
        receipt = {"format": "postgres-custom", "source_database_bytes": size,
                   "created_at": dt.datetime.now(dt.timezone.utc).isoformat(),
                   "file": name, "bytes": target.stat().st_size, "sha256": checksum}
        receipt_path = target.with_suffix(".json")
        with receipt_path.open("x", encoding="utf-8") as file:
            json.dump(receipt, file, ensure_ascii=False, indent=2)
            file.flush(); os.fsync(file.fileno())
        return receipt
    finally:
        # Only this invocation's mkstemp file, never an enumerated user directory.
        if temporary.exists():
            temporary.unlink()


def verify_export(config, receipt_path):
    root = Path(config["export_directory"]).resolve(strict=True)
    receipt_path = Path(receipt_path)
    if receipt_path.is_symlink() or receipt_path.resolve().parent != root:
        raise ValueError("校验清单必须直接位于已配置的导出目录内")
    receipt = json.loads(receipt_path.read_text(encoding="utf-8"))
    name = receipt["file"]
    if Path(name).name != name or not re.fullmatch(r"axonhub-export-[0-9T.Z]+\.dump", name):
        raise ValueError("导出文件名不符合受管格式")
    archive = root / name
    if archive.is_symlink() or archive.resolve().parent != root or not archive.is_file():
        raise ValueError("导出文件不是受管目录中的普通文件")
    if archive.stat().st_size != receipt["bytes"] or digest(archive) != receipt["sha256"]:
        raise ValueError("导出文件校验失败")
    with archive.open("rb") as file:
        if file.read(5) != b"PGDMP":
            raise ValueError("不是 PostgreSQL custom 格式导出")
    return archive, receipt


def expire_exports(config, apply=False):
    root = Path(config["export_directory"]).resolve(strict=True)
    days = config.get("export_retention_days", 7)
    minimum = config.get("export_min_keep", 2)
    if not isinstance(days, int) or days < 1 or not isinstance(minimum, int) or minimum < 1:
        raise ValueError("导出保留天数和最少保留份数必须大于 0")
    files = []
    # 只处理工具生成、成对且结构一致的普通文件，不递归、不跟随链接。
    for path in root.glob("axonhub-export-*.json"):
        if path.is_symlink() or not path.is_file():
            continue
        receipt = json.loads(path.read_text(encoding="utf-8"))
        name = receipt.get("file", "")
        if not re.fullmatch(r"axonhub-export-[0-9T.Z]+\.dump", name) or path.name != Path(name).with_suffix(".json").name:
            continue
        archive = root / name
        if archive.is_symlink() or not archive.is_file() or archive.stat().st_size != receipt.get("bytes"):
            continue
        created = dt.datetime.fromisoformat(receipt["created_at"])
        if created.tzinfo is None:
            raise ValueError("导出清单缺少时区")
        files.append((created, path, archive))
    files.sort(reverse=True)
    cutoff = dt.datetime.now(dt.timezone.utc) - dt.timedelta(days=days)
    selected = [(path, archive) for created, path, archive in files[minimum:] if created < cutoff]
    names = [path.name for pair in selected for path in pair]
    result = {"apply": apply, "files": names, "bytes": sum(path.stat().st_size for pair in selected for path in pair)}
    if apply:
        for receipt, archive in selected:
            archive.unlink()
            receipt.unlink()
    return result


def restore_export(config, receipt_path, database):
    if not IDENTIFIER.fullmatch(database) or database == config["postgres"]["database"]:
        raise ValueError("恢复目标必须是独立的新数据库名称，不能与在线库相同")
    archive, receipt = verify_export(config, receipt_path)
    pg = config["postgres"]
    require_local_execution(pg)
    # 压缩文件大小不能代表展开后的数据、索引和 WAL；需要实际目标卷和容量依据。
    monitored = pg.get("restore_monitor_path")
    if not monitored:
        raise ValueError("恢复前必须配置实际目标数据库卷的 restore_monitor_path；远程库应在目标主机执行")
    source_bytes = receipt.get("source_database_bytes", 0)
    override = config.get("restore_required_gib", 0) * GIB
    if source_bytes <= 0 and override <= 0:
        raise ValueError("清单没有源库容量，请显式配置 restore_required_gib，不能按压缩包大小推算")
    require_capacity(config, Path(monitored).resolve(strict=True), max(source_bytes * 2, override))
    if sql(config, f"SELECT COUNT(*) FROM pg_database WHERE datname='{database}';", "postgres") != "0":
        raise ValueError("恢复目标已存在；本工具不覆盖或删除数据库")
    sql(config, f'CREATE DATABASE "{database}";', "postgres")
    # Single transaction leaves the isolated destination empty on restore error.
    with archive.open("rb") as source:
        command = postgres_command(config, "pg_restore", ["--exit-on-error", "--single-transaction", "--no-owner", "--no-acl", "--dbname", database])
        try:
            run(command, stdin=source, stdout=subprocess.DEVNULL,
                timeout=config.get("operation_timeout_seconds", 3600))
        except RuntimeError as error:
            raise RuntimeError("隔离恢复未完成；目标库保留待检查，在线库未被覆盖。" + str(error)) from error
    return {"restored_database": database, "success": True, "online_database_unchanged": True}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", required=True, help="本机私有 JSON 配置")
    parser.add_argument("operation", choices=["status", "expire-exports", "export", "verify", "restore"])
    parser.add_argument("--receipt")
    parser.add_argument("--database", help="恢复时必须指定尚不存在的独立数据库")
    parser.add_argument("--apply", action="store_true", help="实际删除 expire-exports 预览命中的过期受管导出")
    args = parser.parse_args()
    config = load_config(args.config)
    if args.operation == "status":
        output = status(config)
    elif args.operation == "verify":
        if not args.receipt:
            raise ValueError("校验需要 --receipt")
        _, output = verify_export(config, args.receipt)
        output = {"verified": True, "bytes": output["bytes"], "restore_tested": False}
    else:
        with maintenance_lock(config):
            if args.operation == "export":
                output = export_database(config)
            elif args.operation == "expire-exports":
                output = expire_exports(config, args.apply)
            else:
                if not args.receipt or not args.database:
                    raise ValueError("恢复需要 --receipt 和 --database")
                output = restore_export(config, args.receipt, args.database)
    print(json.dumps(output, ensure_ascii=False, indent=2))
    if args.operation == "status" and (any(fs["level"] != "ok" for fs in output["filesystems"])
            or output.get("wal_archiving", {}).get("error_pending")):
        return 2
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (ValueError, KeyError, OSError, RuntimeError, subprocess.TimeoutExpired) as error:
        print(f"维护失败：{error}", file=sys.stderr)
        sys.exit(1)

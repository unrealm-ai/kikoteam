"""Explicit Docker setup assistance; never changes groups or accepts licenses."""
import os
import hashlib
from pathlib import Path
import platform
import plistlib
import shutil
import subprocess
import sys
import tempfile
import threading
import time

MAC_DOC = "https://docs.docker.com/desktop/setup/install/mac-install/"
ENGINE_DOC = "https://docs.docker.com/engine/install/"


def run(args, label="执行命令", timeout=1200, **kwargs):
    """Keep real command output and show elapsed time, without echoing arguments."""
    started = time.monotonic()
    stopped = threading.Event()
    print(f"[开始] {label}", file=sys.stderr, flush=True)
    def heartbeat():
        while not stopped.wait(5):
            print(f"[进行中] {label} · 已耗时 {int(time.monotonic() - started)} 秒", file=sys.stderr, flush=True)
    thread = threading.Thread(target=heartbeat, daemon=True)
    thread.start()
    try:
        result = subprocess.run(args, check=True, timeout=timeout, **kwargs)
        print(f"[完成] {label} · 耗时 {int(time.monotonic() - started)} 秒", file=sys.stderr, flush=True)
        return result
    finally:
        stopped.set()
        thread.join()


def confirm(message):
    print(message, file=sys.stderr)
    if not sys.stdin.isatty():
        raise ValueError("非交互环境不安装或启动 Docker。请先在终端完成安装，再重试。")
    try:
        answer = input("输入 yes 同意，其他输入取消：").strip().lower()
    except EOFError:
        answer = ""
    if answer != "yes":
        raise ValueError("已取消，未执行此安装/启动操作。")


def linux_distribution():
    values = {}
    for line in Path("/etc/os-release").read_text().splitlines():
        key, sep, value = line.partition("=")
        if sep:
            values[key] = value.strip('"')
    distro, codename = values.get("ID", ""), values.get("VERSION_CODENAME", "")
    supported = {"ubuntu": {"jammy", "noble", "resolute", "stonking"}, "debian": {"bullseye", "bookworm", "trixie"}}
    if distro not in supported or codename not in supported[distro]:
        raise ValueError("当前发行版不支持自动安装，请按官方文档安装 Engine 和 Compose 插件：" + ENGINE_DOC)
    return distro, codename


def install_linux():
    distro, codename = linux_distribution()
    # Do not remove competing runtimes, containers, or existing installations.
    for package in ("docker.io", "docker-compose", "docker-compose-v2", "docker-doc", "podman-docker", "containerd", "runc"):
        result = subprocess.run(["dpkg-query", "-W", "-f=${Status}", package], capture_output=True, text=True, timeout=10)
        if result.returncode == 0 and "install ok installed" in result.stdout:
            raise ValueError(f"发现已有 {package}，不会自动卸载。请先按官方文档处理兼容性：{ENGINE_DOC}{distro}/")
    existing_sources = ("/etc/apt/sources.list.d/docker.list", "/etc/apt/sources.list.d/docker.sources", "/etc/apt/sources.list.d/kiko-docker.sources", "/etc/apt/keyrings/kiko-docker.asc")
    if any(Path(path).exists() for path in existing_sources):
        raise ValueError("已有 Docker 软件源/安装状态，不自动覆盖；请按官方文档完成或修复现有安装。")
    prefix = [] if os.geteuid() == 0 else ["sudo"]
    if prefix and not shutil.which("sudo"):
        raise ValueError("安装需要管理员权限；未找到 sudo，请由管理员按官方文档安装。")
    confirm(f"将使用 {distro} 官方 Docker apt 源安装 Engine、CLI、containerd、Buildx、Compose。\n"
            "会新增 Docker 软件源及签名密钥，执行 apt update/install；需要管理员权限，服务可能自动启动并配置容器网络。\n"
            "不卸载旧包、不修改 docker 组、不放宽 socket 权限、不修改用户防火墙规则。容器端口可能绕过部分防火墙规则。\n"
            f"请先阅读 https://docs.docker.com/engine/install/{distro}/ 。是否继续？")
    if prefix:
        run(["sudo", "-v"], label="等待管理员授权（请在终端输入密码）", timeout=120)
    run(prefix + ["apt-get", "update"], label="刷新系统软件源")
    run(prefix + ["apt-get", "install", "-y", "ca-certificates", "curl"], label="安装 HTTPS 下载依赖")
    with tempfile.TemporaryDirectory(prefix="kiko-docker-") as directory:
        key = Path(directory) / "docker.asc"
        source = Path(directory) / "docker.sources"
        run(["curl", "--fail", "--location", "--proto", "=https", "--proto-redir", "=https", "--connect-timeout", "20", "--max-time", "180", "-o", str(key), f"https://download.docker.com/linux/{distro}/gpg"], label="下载 Docker 官方签名密钥", timeout=190)
        arch = subprocess.run(["dpkg", "--print-architecture"], check=True, capture_output=True, text=True, timeout=10).stdout.strip()
        if arch not in ("amd64", "arm64", "armhf", "s390x", "ppc64el"):
            raise ValueError("不支持的包架构；停止安装。")
        source.write_text(f"Types: deb\nURIs: https://download.docker.com/linux/{distro}\nSuites: {codename}\nComponents: stable\nArchitectures: {arch}\nSigned-By: /etc/apt/keyrings/kiko-docker.asc\n")
        if Path("/etc/apt/sources.list.d/kiko-docker.sources").exists() or Path("/etc/apt/keyrings/kiko-docker.asc").exists():
            raise ValueError("发现上次安装配置，不覆盖。请按官方文档检查已有源后重试。")
        run(prefix + ["install", "-d", "-m", "0755", "/etc/apt/keyrings"], label="准备软件源密钥目录")
        run(prefix + ["install", "-m", "0644", str(key), "/etc/apt/keyrings/kiko-docker.asc"], label="安装官方签名密钥")
        run(prefix + ["install", "-m", "0644", str(source), "/etc/apt/sources.list.d/kiko-docker.sources"], label="配置 Docker 软件源")
    run(prefix + ["apt-get", "update"], label="刷新 Docker 软件源")
    run(prefix + ["apt-get", "install", "-y", "docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin"], label="安装 Docker 与 Compose")


def docker_path():
    found = shutil.which("docker")
    if found:
        return found
    if platform.system() == "Darwin":
        candidate = Path("/Applications/Docker.app/Contents/Resources/bin/docker")
        if candidate.is_file():
            return str(candidate)
    return None


def check_mac_version(minimum="14.0"):
    current = platform.mac_ver()[0]
    def numbers(value):
        try:
            return tuple(int(part) for part in value.split("."))
        except ValueError:
            raise ValueError("无法识别 macOS 版本，请按官方文档检查兼容性：" + MAC_DOC)
    if not current or numbers(current) < numbers(minimum):
        raise ValueError(f"当前 macOS {current or '未知'} 不满足 Docker Desktop 最低要求 {minimum}。不会下载旧版或升级系统；请升级到受支持系统或使用其他已获授权的运行方案。参考 " + MAC_DOC)


def install_mac():
    check_mac_version("13.3")
    arch = {"arm64": "arm64", "x86_64": "amd64"}.get(platform.machine())
    if arch is None:
        raise ValueError("未知 Mac 架构，请按官方文档安装：" + MAC_DOC)
    legacy = int(platform.mac_ver()[0].split(".")[0]) == 13
    version_path = "/207573" if legacy else ""
    warning = "macOS 13 选择历史兼容版 4.48.0。该系统支持已终止，旧版本不包含后续修复；仅在接受此限制后继续。\n" if legacy else "选择当前官方版本。\n"
    confirm(warning + "将下载并打开官方 Docker Desktop 安装镜像。\n"
            "请自行拖拽到 Applications、启动 Docker.app，并处理许可协议与系统权限；不会代为接受许可或付费。\n"
            "Docker Desktop 部分商业/政府用途需要订阅，请先核对适用条款。\n" + MAC_DOC + "\n是否下载并打开？")
    required = 4 * 1024 ** 3
    if shutil.disk_usage(tempfile.gettempdir()).free < required or shutil.disk_usage("/Applications").free < required:
        raise ValueError("磁盘可用空间不足 4 GiB，停止下载/安装。请先释放空间；后续镜像构建和虚拟机还需额外空间。不会自动清理用户文件。")
    directory = Path(tempfile.mkdtemp(prefix="kiko-docker-"))
    installer = directory / "Docker.dmg"
    run(["curl", "--fail", "--location", "--proto", "=https", "--proto-redir", "=https", "--connect-timeout", "20", "--max-time", "1200", "--progress-bar", "-o", str(installer), f"https://desktop.docker.com/mac/main/{arch}{version_path}/Docker.dmg"], label="下载 Docker Desktop", timeout=1210)
    if legacy:
        expected = {"amd64": "fac73a1edc91e6bce5a449e83e3d0b537f19df74c5f51af4705e479cf0d32515", "arm64": "79b10d41c8ed5edde7e0db3e01f604349a1e1bcd35dd487ed13afaca98b2beb6"}[arch]
        digest = hashlib.sha256()
        with installer.open("rb") as stream:
            for block in iter(lambda: stream.read(1024 * 1024), b""):
                digest.update(block)
        if digest.hexdigest() != expected:
            raise ValueError("安装镜像与 Docker 官方 SHA256 不匹配，未打开。")
        print("[完成] 官方 SHA256 校验", file=sys.stderr)
    run(["open", str(installer)], label="打开 Docker 安装镜像", timeout=20)
    print("请完成安装并启动 Docker.app；许可和权限由你确认。安装镜像保留于 " + str(installer), file=sys.stderr)
    raise ValueError("安装引导已打开。Docker 就绪后重新运行 ./start.sh；尚未创建项目配置或启动服务。")


def ensure_docker(allow_setup):
    docker = docker_path()
    if not docker:
        if not allow_setup:
            raise ValueError("未找到 Docker；请运行 ./start.sh 获取安装引导。")
        if platform.system() == "Darwin":
            install_mac()
        elif platform.system() == "Linux":
            install_linux()
        else:
            raise ValueError("仅支持 macOS/Linux：" + ENGINE_DOC)
        docker = docker_path()
        if not docker:
            raise ValueError("安装后仍未找到 Docker，请检查安装结果。")
    if platform.system() == "Darwin" and "/Docker.app/" in str(Path(docker).resolve()):
        info = Path("/Applications/Docker.app/Contents/Info.plist")
        if info.exists():
            with info.open("rb") as stream:
                minimum = plistlib.load(stream).get("LSMinimumSystemVersion", "14.0")
            try:
                check_mac_version(minimum)
            except ValueError:
                if allow_setup:
                    print("已安装 Docker 与系统不兼容；安装辅助不会覆盖现有 app 或数据。", file=sys.stderr)
                    install_mac()
                raise
    try:
        run([docker, "compose", "version"], label="检查 Compose 插件", timeout=20, stdout=subprocess.DEVNULL)
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
        raise ValueError("Docker CLI 已安装，但 Compose 插件不可用。请按 https://docs.docker.com/compose/install/ 安装/修复插件；不会覆盖现有 Docker。")
    try:
        run([docker, "info"], label="检查 Docker 引擎连接", timeout=20, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
        if not allow_setup or platform.system() != "Darwin" or not Path("/Applications/Docker.app").is_dir():
            raise ValueError("Docker 已安装，但引擎不可达或当前用户无权限。检查 docker context、daemon 和权限；不会自动 sudo、加入 docker 组或修改 socket。参考 " + ENGINE_DOC + "linux-postinstall/")
        confirm("Docker 引擎尚未就绪。是否打开 Docker Desktop，并等待最多 180 秒？许可/权限仍由你处理。")
        run(["open", "-a", "Docker"], label="启动 Docker Desktop", timeout=20)
        started = time.monotonic()
        while time.monotonic() - started < 180:
            try:
                run([docker, "info"], label="等待 Docker Desktop 就绪", timeout=10, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                return docker
            except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
                print(f"[等待] Docker Desktop · 已耗时 {int(time.monotonic() - started)} 秒，请检查许可/权限窗口", file=sys.stderr, flush=True)
                time.sleep(5)
        raise ValueError("Docker Desktop 180 秒内未就绪，请完成窗口中的设置后重试。")
    return docker

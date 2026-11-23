# Portable Docker setup and command supervision for start.sh. Source this file.
KIKO_DOCKER_MAC_DOC=https://docs.docker.com/desktop/setup/install/mac-install/
KIKO_DOCKER_ENGINE_DOC=https://docs.docker.com/engine/install/
KIKO_STEP_PID=
KIKO_STEP_STARTING=0
KIKO_STEP_PREVIOUS_BG=
KIKO_DOCKER_SETUP_TMP=

die() {
    printf '%s\n' "$*" >&2
    exit 1
}

# Freeze the tracked command before collecting its descendants. Avoid sending
# signals to the caller's whole process group (which may include other commands).
# ps PID/PPID and awk are standard on supported Linux/macOS hosts.
stop_step_tree() {
    kiko_stop_pid=$1
    kill -STOP "$kiko_stop_pid" 2>/dev/null || return 0
    kiko_stop_seen= kiko_stop_pass=0
    while [ "$kiko_stop_pass" -lt 5 ]; do
        kiko_stop_pass=$((kiko_stop_pass + 1))
        kiko_stop_pids=$(ps -A -o pid= -o ppid= 2>/dev/null |
            awk -v root="$kiko_stop_pid" '
                { parent[$1] = $2 }
                END {
                    for (pid in parent) {
                        p = pid
                        while (p in parent && p != parent[p] && p > 1) {
                            if (parent[p] == root) { print pid; break }
                            p = parent[p]
                        }
                    }
                }')
        [ "$kiko_stop_pids" != "$kiko_stop_seen" ] || break
        kiko_stop_seen=$kiko_stop_pids
        for kiko_stop_child in $kiko_stop_pids; do
            kill -STOP "$kiko_stop_child" 2>/dev/null || :
        done
    done
    for kiko_stop_child in $kiko_stop_seen; do
        kill -KILL "$kiko_stop_child" 2>/dev/null || :
    done
    kill -KILL "$kiko_stop_pid" 2>/dev/null || :
}

step_latest_background() {
    # bash's POSIX sh mode can treat even ${!:-} as unbound before the first job.
    case $- in
        *u*) set +u; KIKO_STEP_LATEST_BG=$!; set -u ;;
        *) KIKO_STEP_LATEST_BG=$! ;;
    esac
}

cancel_running_step() {
    # A trap may run at the asynchronous launch boundary, before the normal
    # assignment. Only adopt the newly started job, never an earlier $! value.
    if [ "$KIKO_STEP_STARTING" = 1 ] && [ -z "$KIKO_STEP_PID" ]; then
        step_latest_background
        if [ -n "$KIKO_STEP_LATEST_BG" ] && [ "$KIKO_STEP_LATEST_BG" != "$KIKO_STEP_PREVIOUS_BG" ]; then
            KIKO_STEP_PID=$KIKO_STEP_LATEST_BG
        fi
    fi
    KIKO_STEP_STARTING=0
    if [ -n "${KIKO_STEP_PID:-}" ]; then
        stop_step_tree "$KIKO_STEP_PID"
        wait "$KIKO_STEP_PID" 2>/dev/null || :
        KIKO_STEP_PID=
    fi
}

# Reserve FD 9 during this call to keep tty stdin for explicitly authorized
# interactive tools; POSIX asynchronous lists otherwise default to /dev/null.
# Stdout is exclusively the command's stdout; diagnostics are always stderr.
run_step() {
    kiko_step_label=$1 kiko_step_timeout=$2
    shift 2
    printf '[开始] %s\n' "$kiko_step_label" >&2
    step_latest_background
    KIKO_STEP_PREVIOUS_BG=$KIKO_STEP_LATEST_BG
    KIKO_STEP_STARTING=1
    "$@" <&9 9<&- &
    KIKO_STEP_PID=$!
    KIKO_STEP_STARTING=0
    kiko_step_elapsed=0
    while kill -0 "$KIKO_STEP_PID" 2>/dev/null; do
        if [ "$kiko_step_elapsed" -ge "$kiko_step_timeout" ]; then
            printf '[超时] %s · 已耗时 %s 秒\n' "$kiko_step_label" "$kiko_step_elapsed" >&2
            cancel_running_step
            return 124
        fi
        sleep 1
        kiko_step_elapsed=$((kiko_step_elapsed + 1))
        if [ $((kiko_step_elapsed % 5)) -eq 0 ] && kill -0 "$KIKO_STEP_PID" 2>/dev/null; then
            printf '[进行中] %s · 已耗时 %s 秒\n' "$kiko_step_label" "$kiko_step_elapsed" >&2
        fi
    done
    if wait "$KIKO_STEP_PID"; then
        kiko_step_status=0
    else
        kiko_step_status=$?
    fi
    KIKO_STEP_PID=
    if [ "$kiko_step_status" -eq 0 ]; then
        printf '[完成] %s · 耗时 %s 秒\n' "$kiko_step_label" "$kiko_step_elapsed" >&2
    fi
    return "$kiko_step_status"
} 9<&0

cleanup_docker_setup() {
    if [ -n "${KIKO_DOCKER_SETUP_TMP:-}" ]; then
        rm -rf "$KIKO_DOCKER_SETUP_TMP"
        KIKO_DOCKER_SETUP_TMP=
    fi
}

docker_confirm() {
    printf '%s\n' "$1" >&2
    [ -t 0 ] || die '非交互环境不安装或启动 Docker。请先在终端完成安装，再重试。'
    printf '输入 yes 同意，其他输入取消：' >&2
    IFS= read -r kiko_docker_answer || kiko_docker_answer=
    kiko_docker_answer=$(printf '%s\n' "$kiko_docker_answer" |
        LC_ALL=C awk '{ sub(/^[[:space:]]+/, ""); sub(/[[:space:]]+$/, ""); print tolower($0) }')
    [ "$kiko_docker_answer" = yes ] || die '已取消，未执行此安装/启动操作。'
}

docker_linux_release() { cat /etc/os-release; }
docker_mac_app_path() { printf '%s\n' /Applications/Docker.app; }

docker_linux_distribution() {
    kiko_docker_release=$(docker_linux_release) || die "无法读取发行版信息：$KIKO_DOCKER_ENGINE_DOC"
    kiko_docker_distribution=$(printf '%s\n' "$kiko_docker_release" | LC_ALL=C awk '
        /^ID=/ { sub(/^ID=/, ""); gsub(/^"|"$/, ""); distro = $0 }
        /^VERSION_CODENAME=/ { sub(/^VERSION_CODENAME=/, ""); gsub(/^"|"$/, ""); codename = $0 }
        END { print distro ":" codename }')
    case $kiko_docker_distribution in
        ubuntu:jammy|ubuntu:noble|ubuntu:resolute|ubuntu:stonking|debian:bullseye|debian:bookworm|debian:trixie) ;;
        *) die "当前发行版不支持自动安装，请按官方文档安装 Engine 和 Compose 插件：$KIKO_DOCKER_ENGINE_DOC" ;;
    esac
    KIKO_DOCKER_DISTRO=${kiko_docker_distribution%%:*}
    KIKO_DOCKER_CODENAME=${kiko_docker_distribution#*:}
}

docker_apt_paths_clear() {
    for kiko_docker_existing in /etc/apt/sources.list.d/docker.list /etc/apt/sources.list.d/docker.sources \
        /etc/apt/sources.list.d/kiko-docker.sources /etc/apt/keyrings/kiko-docker.asc; do
        if [ -e "$kiko_docker_existing" ] || [ -L "$kiko_docker_existing" ]; then
            return 1
        fi
    done
}

docker_as_root() {
    if [ "$KIKO_DOCKER_SUDO" = 1 ]; then sudo "$@"; else "$@"; fi
}

docker_install_linux() {
    docker_linux_distribution
    for kiko_docker_package in docker.io docker-compose docker-compose-v2 docker-doc podman-docker containerd runc; do
        if kiko_docker_status=$(run_step '检查冲突安装' 10 dpkg-query -W '-f=${Status}' "$kiko_docker_package" 2>/dev/null); then
            case $kiko_docker_status in
                *'install ok installed'*) die "发现已有 ${kiko_docker_package}，不会自动卸载。请先按官方文档处理兼容性：$KIKO_DOCKER_ENGINE_DOC$KIKO_DOCKER_DISTRO/" ;;
            esac
        else
            kiko_docker_query_status=$?
            [ "$kiko_docker_query_status" -eq 1 ] || die "无法检查已有软件包，停止安装。请参考 $KIKO_DOCKER_ENGINE_DOC"
        fi
    done
    docker_apt_paths_clear || die '已有 Docker 软件源/安装状态，不自动覆盖；请按官方文档完成或修复现有安装。'
    KIKO_DOCKER_SUDO=0
    if [ "$(id -u)" != 0 ]; then
        command -v sudo >/dev/null 2>&1 || die '安装需要管理员权限；未找到 sudo，请由管理员按官方文档安装。'
        KIKO_DOCKER_SUDO=1
    fi
    docker_confirm "将使用 $KIKO_DOCKER_DISTRO 官方 Docker apt 源安装 Engine、CLI、containerd、Buildx、Compose。
会新增 Docker 软件源及签名密钥，执行 apt update/install；需要管理员权限，服务可能自动启动并配置容器网络。
不卸载旧包、不修改 docker 组、不放宽 socket 权限、不修改用户防火墙规则。容器端口可能绕过部分防火墙规则。
请先阅读 $KIKO_DOCKER_ENGINE_DOC$KIKO_DOCKER_DISTRO/ 。是否继续？"
    if [ "$KIKO_DOCKER_SUDO" = 1 ]; then
        run_step '等待管理员授权（请在终端输入密码）' 120 sudo -v || return $?
    fi
    run_step '刷新系统软件源' 1200 docker_as_root apt-get update || return $?
    run_step '安装 HTTPS 下载依赖' 1200 docker_as_root apt-get install -y ca-certificates curl || return $?
    KIKO_DOCKER_SETUP_TMP=$(mktemp -d "${TMPDIR:-/tmp}/kiko-docker-XXXXXXXX") || return $?
    kiko_docker_key=$KIKO_DOCKER_SETUP_TMP/docker.asc
    kiko_docker_source=$KIKO_DOCKER_SETUP_TMP/docker.sources
    run_step '下载 Docker 官方签名密钥' 190 curl --fail --location --proto '=https' --proto-redir '=https' \
        --connect-timeout 20 --max-time 180 -o "$kiko_docker_key" "https://download.docker.com/linux/$KIKO_DOCKER_DISTRO/gpg" || return $?
    kiko_docker_arch=$(run_step '确认软件包架构' 10 dpkg --print-architecture) || return $?
    case $kiko_docker_arch in amd64|arm64|armhf|s390x|ppc64el) ;; *) die '不支持的包架构；停止安装。' ;; esac
    {
        printf '%s\n' 'Types: deb' "URIs: https://download.docker.com/linux/$KIKO_DOCKER_DISTRO" \
            "Suites: $KIKO_DOCKER_CODENAME" 'Components: stable' "Architectures: $kiko_docker_arch" \
            'Signed-By: /etc/apt/keyrings/kiko-docker.asc'
    } > "$kiko_docker_source" || return $?
    docker_apt_paths_clear || die '发现上次安装配置，不覆盖。请按官方文档检查已有源后重试。'
    run_step '准备软件源密钥目录' 1200 docker_as_root install -d -m 0755 /etc/apt/keyrings || return $?
    run_step '安装官方签名密钥' 1200 docker_as_root install -m 0644 "$kiko_docker_key" /etc/apt/keyrings/kiko-docker.asc || return $?
    run_step '配置 Docker 软件源' 1200 docker_as_root install -m 0644 "$kiko_docker_source" /etc/apt/sources.list.d/kiko-docker.sources || return $?
    cleanup_docker_setup
    run_step '刷新 Docker 软件源' 1200 docker_as_root apt-get update || return $?
    run_step '安装 Docker 与 Compose' 1200 docker_as_root apt-get install -y \
        docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
}

docker_mac_version_supported() {
    LC_ALL=C awk -v current="$1" -v minimum="$2" '
        BEGIN {
            numeric = "^[0-9]+([.][0-9]+)*$"
            if (current !~ numeric || minimum !~ numeric) exit 2
            nc = split(current, c, "."); nm = split(minimum, m, ".")
            for (i = 1; i <= nc || i <= nm; i++) {
                if ((c[i] + 0) > (m[i] + 0)) exit 0
                if ((c[i] + 0) < (m[i] + 0)) exit 1
            }
            exit 0
        }'
}

docker_check_macos_version() {
    kiko_docker_minimum=$1
    KIKO_MAC_VERSION=$(sw_vers -productVersion 2>/dev/null) || KIKO_MAC_VERSION=
    if docker_mac_version_supported "$KIKO_MAC_VERSION" "$kiko_docker_minimum"; then
        return 0
    else
        kiko_docker_version_status=$?
    fi
    if [ "$kiko_docker_version_status" -eq 2 ] && [ -n "$KIKO_MAC_VERSION" ]; then
        printf '无法识别 macOS 版本，请按官方文档检查兼容性：%s\n' "$KIKO_DOCKER_MAC_DOC" >&2
    else
        printf '当前 macOS %s 不满足 Docker Desktop 最低要求 %s。不会下载旧版或升级系统；请升级到受支持系统或使用其他已获授权的运行方案。参考 %s\n' \
            "${KIKO_MAC_VERSION:-未知}" "$kiko_docker_minimum" "$KIKO_DOCKER_MAC_DOC" >&2
    fi
    return 1
}

docker_disk_has_space() {
    kiko_docker_space=$(df -Pk "$1") || return $?
    printf '%s\n' "$kiko_docker_space" | LC_ALL=C awk 'END { exit !(NR > 1 && $4 ~ /^[0-9]+$/ && $4 >= 4194304) }'
}

docker_file_digest() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1"
    else
        shasum -a 256 "$1"
    fi
}

docker_install_mac() {
    docker_check_macos_version 13.3 || return $?
    case $(uname -m) in
        arm64) kiko_docker_arch=arm64 ;;
        x86_64) kiko_docker_arch=amd64 ;;
        *) die "未知 Mac 架构，请按官方文档安装：$KIKO_DOCKER_MAC_DOC" ;;
    esac
    kiko_docker_version_path=
    kiko_docker_warning='选择当前官方版本。'
    if [ "${KIKO_MAC_VERSION%%.*}" = 13 ]; then
        kiko_docker_version_path=/207573
        kiko_docker_warning='macOS 13 选择历史兼容版 4.48.0。该系统支持已终止，旧版本不包含后续修复；仅在接受此限制后继续。'
    fi
    docker_confirm "$kiko_docker_warning
将下载并打开官方 Docker Desktop 安装镜像。
请自行拖拽到 Applications、启动 Docker.app，并处理许可协议与系统权限；不会代为接受许可或付费。
Docker Desktop 部分商业/政府用途需要订阅，请先核对适用条款。
$KIKO_DOCKER_MAC_DOC
是否下载并打开？"
    docker_disk_has_space "${TMPDIR:-/tmp}" && docker_disk_has_space /Applications ||
        die '磁盘可用空间不足 4 GiB，停止下载/安装。请先释放空间；后续镜像构建和虚拟机还需额外空间。不会自动清理用户文件。'
    kiko_docker_mac_tmp=$(mktemp -d "${TMPDIR:-/tmp}/kiko-docker-XXXXXXXX") || return $?
    kiko_docker_installer=$kiko_docker_mac_tmp/Docker.dmg
    run_step '下载 Docker Desktop' 1210 curl --fail --location --proto '=https' --proto-redir '=https' \
        --connect-timeout 20 --max-time 1200 --progress-bar -o "$kiko_docker_installer" \
        "https://desktop.docker.com/mac/main/$kiko_docker_arch$kiko_docker_version_path/Docker.dmg" || return $?
    if [ -n "$kiko_docker_version_path" ]; then
        case $kiko_docker_arch in
            amd64) kiko_docker_expected=fac73a1edc91e6bce5a449e83e3d0b537f19df74c5f51af4705e479cf0d32515 ;;
            arm64) kiko_docker_expected=79b10d41c8ed5edde7e0db3e01f604349a1e1bcd35dd487ed13afaca98b2beb6 ;;
        esac
        kiko_docker_digest=$(run_step '校验 Docker 安装镜像' 180 docker_file_digest "$kiko_docker_installer") || return $?
        [ "${kiko_docker_digest%% *}" = "$kiko_docker_expected" ] || die '安装镜像与 Docker 官方 SHA256 不匹配，未打开。'
        printf '[完成] 官方 SHA256 校验\n' >&2
    fi
    run_step '打开 Docker 安装镜像' 20 open "$kiko_docker_installer" || return $?
    printf '请完成安装并启动 Docker.app；许可和权限由你确认。安装镜像保留于 %s\n' "$kiko_docker_installer" >&2
    die '安装引导已打开。Docker 就绪后重新运行 ./start.sh；尚未创建项目配置或启动服务。'
}

docker_path() {
    DOCKER=$(command -v docker 2>/dev/null) || DOCKER=
    if [ -z "$DOCKER" ] && [ "$KIKO_DOCKER_OS" = Darwin ]; then
        kiko_docker_candidate=$(docker_mac_app_path)/Contents/Resources/bin/docker
        if [ -x "$kiko_docker_candidate" ]; then DOCKER=$kiko_docker_candidate; fi
    fi
    [ -n "$DOCKER" ]
}

# BSD readlink does not offer GNU's canonicalization option.
docker_resolve_cli_path() {
    kiko_docker_resolved=$1 kiko_docker_hops=0
    while [ -L "$kiko_docker_resolved" ]; do
        [ "$kiko_docker_hops" -lt 24 ] || return 1
        kiko_docker_next=$(readlink "$kiko_docker_resolved") || return $?
        case $kiko_docker_next in
            /*) kiko_docker_resolved=$kiko_docker_next ;;
            *) kiko_docker_resolved=$(dirname "$kiko_docker_resolved")/$kiko_docker_next ;;
        esac
        kiko_docker_hops=$((kiko_docker_hops + 1))
    done
    kiko_docker_dir=$(CDPATH= cd -P "$(dirname "$kiko_docker_resolved")" && pwd -P) || return $?
    printf '%s/%s\n' "$kiko_docker_dir" "${kiko_docker_resolved##*/}"
}

docker_mac_app_minimum() {
    /usr/libexec/PlistBuddy -c 'Print :LSMinimumSystemVersion' "$1" 2>/dev/null
}

docker_info_quiet() { "$DOCKER" info >/dev/null 2>&1; }
docker_clock() { date +%s; }

ensure_docker() {
    kiko_docker_allow_setup=$1
    KIKO_DOCKER_OS=$(uname -s) || die '无法识别操作系统。'
    if ! docker_path; then
        [ "$kiko_docker_allow_setup" = 1 ] || die '未找到 Docker；请运行 ./start.sh 获取安装引导。'
        case $KIKO_DOCKER_OS in
            Darwin) docker_install_mac >&2 || return $? ;;
            Linux) docker_install_linux >&2 || return $? ;;
            *) die "仅支持 macOS/Linux：$KIKO_DOCKER_ENGINE_DOC" ;;
        esac
        docker_path || die '安装后仍未找到 Docker，请检查安装结果。'
    fi
    if [ "$KIKO_DOCKER_OS" = Darwin ]; then
        kiko_docker_cli=$(docker_resolve_cli_path "$DOCKER") || die '无法解析已安装的 Docker 路径。'
        case $kiko_docker_cli in
            */Docker.app/*)
                kiko_docker_app=${kiko_docker_cli%/Docker.app/*}/Docker.app
                if [ -f "$kiko_docker_app/Contents/Info.plist" ]; then
                    kiko_docker_min=$(docker_mac_app_minimum "$kiko_docker_app/Contents/Info.plist") || kiko_docker_min=14.0
                    if ! docker_check_macos_version "$kiko_docker_min"; then
                        if [ "$kiko_docker_allow_setup" = 1 ]; then
                            printf '已安装 Docker 与系统不兼容；安装辅助不会覆盖现有 app 或数据。\n' >&2
                            docker_install_mac >&2 || return $?
                        fi
                        return 1
                    fi
                fi
                ;;
        esac
    fi
    run_step '检查 Compose 插件' 20 "$DOCKER" compose version >/dev/null ||
        die 'Docker CLI 已安装，但 Compose 插件不可用。请按 https://docs.docker.com/compose/install/ 安装/修复插件；不会覆盖现有 Docker。'
    if run_step '检查 Docker 引擎连接' 20 docker_info_quiet; then return 0; fi
    if [ "$kiko_docker_allow_setup" != 1 ] || [ "$KIKO_DOCKER_OS" != Darwin ] || [ ! -d "$(docker_mac_app_path)" ]; then
        die "Docker 已安装，但引擎不可达或当前用户无权限。检查 docker context、daemon 和权限；不会自动 sudo、加入 docker 组或修改 socket。参考 ${KIKO_DOCKER_ENGINE_DOC}linux-postinstall/"
    fi
    docker_confirm 'Docker 引擎尚未就绪。是否打开 Docker Desktop，并等待最多 180 秒？许可/权限仍由你处理。'
    run_step '启动 Docker Desktop' 20 open -a Docker >/dev/null || return $?
    kiko_docker_started=$(docker_clock) || return $?
    kiko_docker_elapsed=0
    while [ "$kiko_docker_elapsed" -lt 180 ]; do
        kiko_docker_remaining=$((180 - kiko_docker_elapsed))
        if [ "$kiko_docker_remaining" -gt 10 ]; then kiko_docker_remaining=10; fi
        if run_step '等待 Docker Desktop 就绪' "$kiko_docker_remaining" docker_info_quiet; then return 0; fi
        kiko_docker_now=$(docker_clock) || return $?
        kiko_docker_elapsed=$((kiko_docker_now - kiko_docker_started))
        printf '[等待] Docker Desktop · 已耗时 %s 秒，请检查许可/权限窗口\n' "$kiko_docker_elapsed" >&2
        kiko_docker_remaining=$((180 - kiko_docker_elapsed))
        if [ "$kiko_docker_remaining" -gt 5 ]; then kiko_docker_remaining=5; fi
        if [ "$kiko_docker_remaining" -gt 0 ]; then sleep "$kiko_docker_remaining"; fi
        kiko_docker_now=$(docker_clock) || return $?
        kiko_docker_elapsed=$((kiko_docker_now - kiko_docker_started))
    done
    die 'Docker Desktop 180 秒内未就绪，请完成窗口中的设置后重试。'
}

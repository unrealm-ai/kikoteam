#!/bin/sh
# Local-only Compose launcher. Never replaces configuration or removes data.
set -eu
umask 077
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
cd "$root"
. "$root/tools/docker-support.sh"

state=$root/.kiko-local
lock=$state/start.lock.d
config=$root/.env
marker=$state/config.sha256
runtime=$state/runtime.env
volume=kikoteam-local-postgres-data
pending=
scratch=
owns_lock=no
command=up
command_seen=no
update=no

usage() {
    printf '%s\n' '用法：./start.sh [init|up|restart|down|status|logs] [--update]' \
        '默认执行 up，使用当前本地源码。restart 或 --update 先快进更新再启动。'
}

cleanup() {
    cleanup_docker_setup
    if [ -n "$pending" ]; then rm -f "$pending"; fi
    if [ -n "$scratch" ]; then rm -rf "$scratch"; fi
    if [ "$owns_lock" = yes ]; then
        rm -f "$lock/pid"
        rmdir "$lock" || :
    fi
}

interrupted() {
    trap '' HUP INT TERM
    cancel_running_step
    printf '%s\n' '已中止等待；已完成的安装步骤或容器可能保留，请检查后重试。' >&2
    exit "$1"
}

trap cleanup 0
trap 'interrupted 129' HUP
trap 'interrupted 130' INT
trap 'interrupted 143' TERM

exists() { [ -e "$1" ] || [ -L "$1" ]; }

# BSD (macOS) and GNU/BusyBox (Linux) stat have different option syntax.
owner_only() {
    metadata=$(stat -f '%Lp %u' "$1" 2>/dev/null) ||
        metadata=$(stat -c '%a %u' "$1" 2>/dev/null) || return 1
    mode=${metadata%% *}
    owner=${metadata#* }
    case $mode in 0|[0-7]00) ;; *) return 1 ;; esac
    [ "$owner" = "$(id -u)" ]
}

prepare_state() {
    if ! exists "$state"; then mkdir -m 700 "$state" 2>/dev/null || :; fi
    [ ! -L "$state" ] && [ -d "$state" ] && owner_only "$state" ||
        die '.kiko-local 必须是仅所有者可访问的真实目录（chmod 700 .kiko-local）。'
}

acquire_lock() {
    prepare_state
    if [ "${KIKO_LOCAL_UPDATE_LOCK_PID:-}" = "$$" ] && [ "$command" = up ] &&
        [ "$update" = no ] && [ ! -L "$lock" ] && [ -d "$lock" ] && owner_only "$lock" &&
        [ ! -L "$lock/pid" ] && [ -f "$lock/pid" ] && owner_only "$lock/pid" &&
        [ "$(cat "$lock/pid")" = "$$" ]; then
        owns_lock=yes
        unset KIKO_LOCAL_UPDATE_LOCK_PID
        return
    fi
    unset KIKO_LOCAL_UPDATE_LOCK_PID
    if ! mkdir -m 700 "$lock" 2>/dev/null; then
        die '本地启动器被锁定。请等待其他操作完成。如确认启动器及其子进程均已退出，再手动移除 .kiko-local/start.lock.d；不要删除数据库卷或配置。'
    fi
    owns_lock=yes
    printf '%s\n' "$$" > "$lock/pid"
}

# Parse only data. The intermediate file carries one literal KEY=value per line and
# remains owner-only. It is never sourced or expanded as shell code.
parse_env() {
    env_path=$1
    [ ! -L "$env_path" ] && [ -f "$env_path" ] && owner_only "$env_path" ||
        die "$2 must be a regular owner-only file (chmod 600)."
    LC_ALL=C tr -d '\000' < "$env_path" | cmp -s "$env_path" - ||
        die 'invalid byte in dotenv file'
    LC_ALL=C awk '
        BEGIN { sq=sprintf("%c",39); dq=sprintf("%c",34); bs=sprintf("%c",92) }
        function fail(msg) { print msg > "/dev/stderr"; exit 1 }
        { sub(/^[[:space:]]+/, ""); sub(/[[:space:]]+$/, "")
          if ($0 == "" || substr($0,1,1) == "#") next
          sep=index($0,"=")
          key=substr($0,1,sep-1)
          value=substr($0,sep+1)
          if (!sep || key !~ /^[A-Z][A-Z0-9_]*$/ || key in seen) fail("invalid or duplicate .env key")
          seen[key]=1
          if (length(value)>=2 && substr(value,1,1)==sq && substr(value,length(value),1)==sq) {
            value=substr(value,2,length(value)-2)
            if (index(value,sq) || index(value,bs)) fail("embedded quotes and escapes are unsupported in .env values")
          } else if (index(value,dq) || index(value,sq) || index(value,"$") || index(value,"#") ||
                     index(value,bs) || value ~ /[[:space:]]/) {
            fail("quote literal values with single quotes; interpolation is unsupported")
          }
          print key "=" value
        }' "$env_path"
}

load_config() {
    parse_env "$config" '.env' > "$scratch/config" || exit 1
    db= db_user= db_password= auth= origin= storage=
    port=18080
    verify_email=false
    mail=false
    implicit_tls=false
    while IFS= read -r item; do
        value=${item#*=}
        case ${item%%=*} in
            POSTGRES_DB) db=$value ;;
            POSTGRES_USER) db_user=$value ;;
            POSTGRES_PASSWORD) db_password=$value ;;
            KIKO_AUTH_SECRET) auth=$value ;;
            KIKO_LOCAL_PORT) port=$value ;;
            KIKO_PUBLIC_ORIGIN) origin=$value ;;
            KIKO_STORAGE_LOCAL_ROOT) storage=$value ;;
            KIKO_REGISTRATION_VERIFICATION_REQUIRED) verify_email=$value ;;
            KIKO_MAIL_ENABLED) mail=$value ;;
            SMTP_IMPLICIT_TLS) implicit_tls=$value ;;
        esac
    done < "$scratch/config"
    for key in POSTGRES_DB POSTGRES_USER; do
        case $key in POSTGRES_DB) value=$db ;; *) value=$db_user ;; esac
        case $value in
            ''|[!abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ_]*|*[!abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_]*)
                die "$key must be a simple SQL identifier" ;;
        esac
    done
    case $db_password in
        *[!abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-]*)
            die 'POSTGRES_PASSWORD must contain 24+ URL-safe letters/digits/_/-' ;;
    esac
    [ "${#db_password}" -ge 24 ] || die 'POSTGRES_PASSWORD must contain 24+ URL-safe letters/digits/_/-'
    [ "$(printf '%s' "$auth" | LC_ALL=C wc -c)" -ge 32 ] || die 'KIKO_AUTH_SECRET must be 32+ bytes'
    case $port in ''|*[!0123456789]*) die 'KIKO_LOCAL_PORT must be 1024..65535' ;; esac
    # Strip leading zeroes only for the numeric bound, not the literal origin comparison.
    numeric_port=$port
    while [ "${numeric_port#0}" != "$numeric_port" ]; do numeric_port=${numeric_port#0}; done
    [ -n "$numeric_port" ] && [ "${#numeric_port}" -le 5 ] &&
        [ "$numeric_port" -ge 1024 ] && [ "$numeric_port" -le 65535 ] ||
        die 'KIKO_LOCAL_PORT must be 1024..65535'
    case $origin in
        "http://localhost:$port"|"http://127.0.0.1:$port") ;;
        *) die 'local launcher only accepts localhost/127.0.0.1 origin matching its port' ;;
    esac
    check_bool KIKO_REGISTRATION_VERIFICATION_REQUIRED "$verify_email"
    check_bool KIKO_MAIL_ENABLED "$mail"
    check_bool SMTP_IMPLICIT_TLS "$implicit_tls"
    [ "$verify_email" != true ] || [ "$mail" = true ] || die 'email verification requires mail enabled'
}

check_bool() {
    case $2 in true|false) ;; *) die "$1 must be true or false" ;; esac
}

random_hex() {
    result=$(LC_ALL=C od -An -v -N "$1" -tx1 /dev/urandom | tr -d '[:space:]')
    [ "${#result}" -eq "$(($1 * 2))" ] || return 1
    printf '%s' "$result"
}

fingerprint_bytes() {
    printf '%s\000%s\000%s\000%s' "$db" "$db_user" "$db_password" "$auth"
}

fingerprint_config() {
    if command -v sha256sum >/dev/null 2>&1; then
        digest=$(fingerprint_bytes | sha256sum) || die '无法计算本地配置指纹。'
    elif command -v openssl >/dev/null 2>&1; then
        digest=$(fingerprint_bytes | openssl dgst -sha256 -r) || die '无法计算本地配置指纹。'
    elif [ -x /usr/bin/shasum ]; then
        digest=$(fingerprint_bytes | /usr/bin/shasum -a 256) || die '无法计算本地配置指纹。'
    else
        die '找不到 SHA256 工具（sha256sum、openssl 或 shasum）。'
    fi
    digest=${digest%% *}
    case $digest in *[!0123456789abcdef]*) die '无法计算本地配置指纹。' ;; esac
    [ "${#digest}" -eq 64 ] || die '无法计算本地配置指纹。'
}

# Each file is completed on the same filesystem before its destination appears.
publish_file() {
    [ ! -e "$1" ] && [ ! -L "$1" ] &&
        ln "$pending" "$1" || die '本地配置已存在或无法安全写入，原文件未覆盖。'
    rm -f "$pending"
    pending=
}

initialize_config() {
    run_step '检查已有数据卷' 20 "$DOCKER" volume ls --format '{{.Name}}' > "$scratch/volumes" ||
        die '无法检查已有 Docker 数据卷。'
    has_volume=no
    if grep -Fqx -- "$volume" "$scratch/volumes"; then has_volume=yes; fi
    if ! exists "$config"; then
        if exists "$root/backend/.env" || exists "$root/backend/backend.local.json" ||
            exists "$root/backend.local.json" || [ -n "${DATABASE_URL:-}${EVENT_HUB_DATABASE_URL:-}${KIKO_CONFIG:-}${KIKO_AUTH_SECRET:-}" ]; then
            die 'existing deployment configuration detected; migrate explicitly, never generate replacement secrets'
        fi
        if exists "$marker" || exists "$runtime" || [ "$has_volume" = yes ]; then
            die 'existing local database/state detected without .env; restore .env from backup'
        fi
        pending=$(mktemp "$state/.env-pending.XXXXXXXX") || die '无法准备本地配置。'
        new_password=$(random_hex 24) && new_auth=$(random_hex 32) || die '无法生成安全的本地凭据。'
        while IFS= read -r line || [ -n "$line" ]; do
            case $line in
                POSTGRES_PASSWORD=) line="POSTGRES_PASSWORD=$new_password" ;;
                KIKO_AUTH_SECRET=) line="KIKO_AUTH_SECRET=$new_auth" ;;
            esac
            printf '%s\n' "$line"
        done < "$root/.env.example" > "$pending"
        publish_file "$config"
        unset new_password new_auth
    fi
    load_config
    fingerprint_config
    if exists "$marker"; then
        [ ! -L "$marker" ] && [ -f "$marker" ] && owner_only "$marker" ||
            die '.kiko-local/config.sha256 must be a regular owner-only file.'
        saved_digest=$(LC_ALL=C awk '{sub(/^[[:space:]]+/,"");sub(/[[:space:]]+$/,"");if(length)print}' "$marker")
        [ "$saved_digest" = "$digest" ] ||
            die 'database identity/credentials changed; restore configuration or perform an explicit migration'
    else
        [ "$has_volume" = no ] || die 'unrecognized existing Compose database; restore its .kiko-local state or migrate explicitly'
        pending=$(mktemp "$state/.config-sha.XXXXXXXX") || die '无法准备配置指纹。'
        printf '%s\n' "$digest" > "$pending"
        publish_file "$marker"
    fi
}

initialize_runtime() {
    if ! exists "$runtime"; then
        new_hub=$(random_hex 32) && new_engine=$(random_hex 32) || die '无法生成安全的运行令牌。'
        pending=$(mktemp "$state/.runtime-env.XXXXXXXX") || die '无法准备运行凭据。'
        printf 'KIKO_HUB_TOKEN=%s\nKIKO_ENGINE_TOKEN=%s\n' "$new_hub" "$new_engine" > "$pending"
        publish_file "$runtime"
        unset new_hub new_engine
    fi
    parse_env "$runtime" '.kiko-local/runtime.env' > "$scratch/runtime" || exit 1
    hub_token= engine_token=
    while IFS= read -r item; do
        case ${item%%=*} in
            KIKO_HUB_TOKEN) hub_token=${item#*=} ;;
            KIKO_ENGINE_TOKEN) engine_token=${item#*=} ;;
        esac
    done < "$scratch/runtime"
    [ "$(printf '%s' "$hub_token" | LC_ALL=C wc -c)" -ge 32 ] &&
        [ "$(printf '%s' "$engine_token" | LC_ALL=C wc -c)" -ge 32 ] &&
        [ "$hub_token" != "$engine_token" ] ||
        die '.kiko-local/runtime.env must contain two distinct internal tokens of at least 32 bytes each'
}

lookup_home() {
    if command -v getent >/dev/null 2>&1; then
        getent passwd "$2" | LC_ALL=C awk -F : 'NR==1 {print $6}'
    elif command -v dscacheutil >/dev/null 2>&1; then
        dscacheutil -q user -a "$1" "$2" | LC_ALL=C awk '/^dir: / {sub(/^dir: /,"");print;exit}'
    fi
}

prepare_storage() {
    storage=$(printf '%s\n' "$storage" | LC_ALL=C awk '{sub(/^[[:space:]]+/,"");sub(/[[:space:]]+$/,"");print}')
    [ -n "$storage" ] || storage='~/.kiko'
    case $storage in
        '~'|'~/'*)
            home=${HOME:-}
            if [ -z "$home" ]; then home=$(lookup_home uid "$(id -u)"); fi
            case $home in /*) ;; *) die '无法解析当前用户的家目录。' ;; esac
            case $storage in '~') storage=$home ;; *) storage=$home/${storage#\~/} ;; esac
            ;;
        '~'*)
            named_home=${storage%%/*}
            username=${named_home#\~}
            case $username in
                ''|-*|*[!abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_.-]*)
                    die 'KIKO_STORAGE_LOCAL_ROOT must be absolute or start with ~/' ;;
            esac
            home=$(lookup_home name "$username")
            case $home in /*) ;; *) die '无法解析资产路径指定账户的家目录。' ;; esac
            storage=$home${storage#"$named_home"}
            ;;
    esac
    case $storage in /*) ;; *) die 'KIKO_STORAGE_LOCAL_ROOT must be absolute or start with ~/' ;; esac
    # Remove redundant slashes/dots (without resolving '..' or symlinks). In
    # particular, a trailing slash must not hide a symlink from the check.
    storage=$(printf '%s\n' "$storage" | LC_ALL=C awk -F / '{
        prefix=substr($0,1,2)=="//" && substr($0,3,1)!="/" ? "//" : "/"
        path=""; for(i=1;i<=NF;i++) if ($i!="" && $i!=".") path=path (path=="" ? "" : "/") $i
        print prefix path
    }')
    [ ! -L "$storage" ] || die 'KIKO_STORAGE_LOCAL_ROOT must not be a symlink'
    if [ "$command" = up ]; then
        mkdir -p "$storage" && [ -d "$storage" ] && [ -w "$storage" ] && [ -x "$storage" ] ||
            die 'KIKO_STORAGE_LOCAL_ROOT must be a writable directory'
    fi
    local_uid=$(id -u)
    local_gid=$(id -g)
}

# Env -u is shared by macOS and Linux. Only the child loses inherited values for
# keys supplied by dotenv; setting/unsetting these in the shell could overwrite
# its own PATH, readonly variables, or supervisor state.
compose_exec() {
    set -- "$DOCKER" compose --project-name kikoteam-local --project-directory "$root" \
        --env-file "$config" -f "$root/compose.yaml" "$@"
    while IFS= read -r item; do
        key=${item%%=*}
        case $key in
            KIKO_LOCAL_STORAGE_HOST_ROOT|KIKO_LOCAL_UID|KIKO_LOCAL_GID|KIKO_HUB_TOKEN|KIKO_ENGINE_TOKEN) ;;
            *) set -- -u "$key" "$@" ;;
        esac
    done < "$scratch/config"
    KIKO_LOCAL_STORAGE_HOST_ROOT=$storage
    KIKO_LOCAL_UID=$local_uid
    KIKO_LOCAL_GID=$local_gid
    export KIKO_LOCAL_STORAGE_HOST_ROOT KIKO_LOCAL_UID KIKO_LOCAL_GID
    unset KIKO_HUB_TOKEN KIKO_ENGINE_TOKEN
    if [ "$command" = up ]; then
        KIKO_HUB_TOKEN=$hub_token
        KIKO_ENGINE_TOKEN=$engine_token
        export KIKO_HUB_TOKEN KIKO_ENGINE_TOKEN
    fi
    exec env "$@"
}

compose_step() {
    label=$1
    budget=$2
    shift 2
    run_step "$label" "$budget" compose_exec "$@" ||
        die 'Local startup failed. Check Docker Compose availability, ports and service logs; no data was deleted.'
}

start_services() {
    revision='源码归档'
    if exists "$root/.git"; then
        run_step '确认构建版本' 10 git rev-parse --short HEAD > "$scratch/revision" ||
            die '无法确认 Git 构建版本，当前服务未被停止。'
        revision=$(cat "$scratch/revision")
        run_step '检查工作区版本' 10 git status --porcelain > "$scratch/git-status" ||
            die '无法检查 Git 工作区，当前服务未被停止。'
        if [ -s "$scratch/git-status" ]; then revision="${revision}（含本地改动）"; fi
    fi
    compose_step '构建 Web、Backend、Event Hub 和 Engine' 1800 build backend event-hub agent-engine
    compose_step '等待旧 Engine 停稳' 110 stop --timeout 90 agent-engine
    compose_step '等待旧 Event Hub 退出' 45 stop --timeout 25 event-hub
    compose_step '启动 Backend、Event Hub 并等待迁移' 360 up -d --no-build --wait --wait-timeout 180 backend event-hub
    compose_step '同步官方资源和员工模板' 90 run --pull never --rm --no-deps template-catalog
    compose_step '启动 Engine 并等待注册' 210 up -d --no-build --wait --wait-timeout 180 --no-deps agent-engine
    printf '已就绪 %s · Git %s\n' "$origin" "$revision"
}

argument_error() {
    usage >&2
    printf '%s\n' "$1" >&2
    exit 2
}

options=yes
for argument do
    case $options:$argument in
        yes:-h|yes:--help) usage; exit 0 ;;
        yes:--update) update=yes; continue ;;
        yes:--) options=no; continue ;;
    esac
    case $argument in
        init|up|restart|down|status|logs)
            [ "$command_seen" = no ] || argument_error '只接受一个启动命令。'
            command=$argument
            command_seen=yes ;;
        *) argument_error '无效的启动命令或选项。' ;;
    esac
done

case $command in
    up|restart) ;;
    *) [ "$update" = no ] || argument_error '--update 仅适用于 up 和 restart' ;;
esac

case $command in
    init|up|down|restart) acquire_lock ;;
esac

if [ "$update" = yes ] || [ "$command" = restart ]; then
    if exists "$root/.git"; then
        run_step '拉取当前分支' 120 git pull --ff-only --no-rebase ||
            die '拉取代码失败，当前服务未被停止。可运行 ./start.sh 构建当前工作区。'
        # Retain the lock across exec. The new launcher checks its PID and the
        # owner-only lock record before adopting it.
        KIKO_LOCAL_UPDATE_LOCK_PID=$$
        export KIKO_LOCAL_UPDATE_LOCK_PID
        exec "$root/start.sh" up
    elif [ "$update" = yes ]; then
        die '--update 需要 Git 工作区。可运行 ./start.sh 构建当前源码。'
    fi
    command=up
fi

case $command in
    init|up) ensure_docker 1 ;;
    *) ensure_docker 0 ;;
esac

case $command in
    init|up|down) scratch_base=$state ;;
    *) scratch_base=/tmp ;;
esac
scratch=$(mktemp -d "$scratch_base/.kiko-launch.XXXXXXXX") || die '无法准备启动器临时目录。'
case $command in
    init|up) initialize_config; initialize_runtime ;;
    *) load_config ;;
esac

if [ "$command" = init ]; then
    printf '%s\n' '本地配置已就绪。'
    exit 0
fi

prepare_storage
case $command in
    up) start_services ;;
    down) compose_step 'Compose down' 180 --profile maintenance down ;;
    status) compose_step 'Compose status' 180 ps ;;
    logs) compose_step 'Compose logs' 180 logs --tail 100 ;;
esac

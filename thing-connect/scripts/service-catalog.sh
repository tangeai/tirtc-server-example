#!/usr/bin/env bash

# This file is sourced by build/install/deployment scripts. The TSV catalog is
# the single service inventory; this loader owns its shell representation and
# rejects malformed or ambiguous additions before any build or process action.
load_service_catalog() {
    local catalog_file="$1" name port kind required uses_mqtt static_dir display_name
    local admin_count=0
    [ -f "$catalog_file" ] || {
        echo "[ERROR] 服务清单不存在: $catalog_file" >&2
        return 1
    }

    ALL_SERVICES=()
    BUSINESS_SERVICES=()
    REQUIRED_SERVICES=()
    OPTIONAL_SERVICES=()
    MQTT_SERVICES=()
    ADMIN_SERVICE=""
    declare -gA SERVICE_PORT=()
    declare -gA SERVICE_KIND=()
    declare -gA SERVICE_REQUIRED=()
    declare -gA SERVICE_USES_MQTT=()
    declare -gA SERVICE_STATIC_DIR=()
    declare -gA SERVICE_DISPLAY_NAME=()

    while IFS=$'\t' read -r name port kind required uses_mqtt static_dir display_name; do
        [ -n "$name" ] || continue
        [[ "$name" == \#* ]] && continue
        [[ "$name" =~ ^[a-z][a-z0-9-]*-server$ ]] || {
            echo "[ERROR] 服务清单名称无效: $name" >&2
            return 1
        }
        [[ "$port" =~ ^[1-9][0-9]*$ ]] && [ "$port" -le 65535 ] || {
            echo "[ERROR] $name HTTP 端口无效: $port" >&2
            return 1
        }
        [ "$kind" = "admin" ] || [ "$kind" = "business" ] || {
            echo "[ERROR] $name 类型无效: $kind" >&2
            return 1
        }
        [[ "$required" =~ ^(true|false)$ ]] && [[ "$uses_mqtt" =~ ^(true|false)$ ]] || {
            echo "[ERROR] $name 布尔属性无效" >&2
            return 1
        }
        [ -n "$display_name" ] || {
            echo "[ERROR] $name 缺少显示名称" >&2
            return 1
        }
        if [ "$static_dir" != "-" ]; then
            [[ "$static_dir" != /* && "$static_dir" != "." && "$static_dir" != ".." && "$static_dir" != ../* && "$static_dir" != */../* && "$static_dir" != */.. ]] || {
                echo "[ERROR] $name 静态资源目录必须是仓库内相对路径: $static_dir" >&2
                return 1
            }
        fi
        [ -z "${SERVICE_PORT[$name]+present}" ] || {
            echo "[ERROR] 服务清单名称重复: $name" >&2
            return 1
        }
        local known
        for known in "${ALL_SERVICES[@]}"; do
            [ "${SERVICE_PORT[$known]}" != "$port" ] || {
                echo "[ERROR] 服务清单端口重复: $port" >&2
                return 1
            }
        done

        ALL_SERVICES+=("$name")
        SERVICE_PORT["$name"]="$port"
        SERVICE_KIND["$name"]="$kind"
        SERVICE_REQUIRED["$name"]="$required"
        SERVICE_USES_MQTT["$name"]="$uses_mqtt"
        SERVICE_STATIC_DIR["$name"]="$static_dir"
        SERVICE_DISPLAY_NAME["$name"]="$display_name"
        if [ "$required" = "true" ]; then
            REQUIRED_SERVICES+=("$name")
        fi
        if [ "$kind" = "admin" ]; then
            [ "$name" = "admin-server" ] || {
                echo "[ERROR] Admin 服务必须命名为 admin-server" >&2
                return 1
            }
            admin_count=$((admin_count + 1))
            ADMIN_SERVICE="$name"
            [ "$required" = "true" ] || {
                echo "[ERROR] Admin 服务必须标记为必装" >&2
                return 1
            }
        else
            BUSINESS_SERVICES+=("$name")
            if [ "$required" = "false" ]; then
                OPTIONAL_SERVICES+=("$name")
            fi
        fi
        [ "$uses_mqtt" != "true" ] || MQTT_SERVICES+=("$name")
    done <"$catalog_file"

    [ "$admin_count" -eq 1 ] || {
        echo "[ERROR] 服务清单必须且只能包含一个 Admin 服务" >&2
        return 1
    }
    [ "${#BUSINESS_SERVICES[@]}" -gt 0 ] || {
        echo "[ERROR] 服务清单至少需要一个业务服务" >&2
        return 1
    }
}

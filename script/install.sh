#!/bin/bash

red='\033[0;31m'
green='\033[0;32m'
yellow='\033[0;33m'
plain='\033[0m'

cur_dir=$(pwd)

# check root
[[ $EUID -ne 0 ]] && echo -e "${red}错误：${plain} 必须使用root用户运行此脚本！\n" && exit 1

# check os
if [[ -f /etc/redhat-release ]]; then
    release="centos"
elif cat /etc/issue | grep -Eqi "alpine"; then
    release="alpine"
elif cat /etc/issue | grep -Eqi "debian"; then
    release="debian"
elif cat /etc/issue | grep -Eqi "ubuntu"; then
    release="ubuntu"
elif cat /etc/issue | grep -Eqi "centos|red hat|redhat|rocky|alma|oracle linux"; then
    release="centos"
elif cat /proc/version | grep -Eqi "debian"; then
    release="debian"
elif cat /proc/version | grep -Eqi "ubuntu"; then
    release="ubuntu"
elif cat /proc/version | grep -Eqi "centos|red hat|redhat|rocky|alma|oracle linux"; then
    release="centos"
elif cat /proc/version | grep -Eqi "arch"; then
    release="arch"
else
    echo -e "${red}未检测到系统版本，请联系脚本作者！${plain}\n" && exit 1
fi

########################
# 参数解析
########################
VERSION_ARG=""
API_HOST_ARG=""
NODE_ID_ARG=""
API_KEY_ARG=""

parse_args() {
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --api-host)
                API_HOST_ARG="$2"; shift 2 ;;
            --node-id)
                NODE_ID_ARG="$2"; shift 2 ;;
            --api-key)
                API_KEY_ARG="$2"; shift 2 ;;
            -h|--help)
                echo "用法: $0 [版本号] [--api-host URL] [--node-id ID] [--api-key KEY]"
                exit 0 ;;
            --*)
                echo "未知参数: $1"; exit 1 ;;
            *)
                # 兼容第一个位置参数作为版本号
                if [[ -z "$VERSION_ARG" ]]; then
                    VERSION_ARG="$1"; shift
                else
                    shift
                fi ;;
        esac
    done
}

arch=$(uname -m)

if [[ $arch == "x86_64" || $arch == "x64" || $arch == "amd64" ]]; then
    arch="64"
elif [[ $arch == "aarch64" || $arch == "arm64" ]]; then
    arch="arm64-v8a"
elif [[ $arch == "s390x" ]]; then
    arch="s390x"
else
    arch="64"
    echo -e "${red}检测架构失败，使用默认架构: ${arch}${plain}"
fi

if [ "$(getconf WORD_BIT)" != '32' ] && [ "$(getconf LONG_BIT)" != '64' ] ; then
    echo "本软件不支持 32 位系统(x86)，请使用 64 位系统(x86_64)，如果检测有误，请联系作者"
    exit 2
fi

# os version
if [[ -f /etc/os-release ]]; then
    os_version=$(awk -F'[= ."]' '/VERSION_ID/{print $3}' /etc/os-release)
fi
if [[ -z "$os_version" && -f /etc/lsb-release ]]; then
    os_version=$(awk -F'[= ."]+' '/DISTRIB_RELEASE/{print $2}' /etc/lsb-release)
fi

if [[ x"${release}" == x"centos" ]]; then
    if [[ ${os_version} -le 6 ]]; then
        echo -e "${red}请使用 CentOS 7 或更高版本的系统！${plain}\n" && exit 1
    fi
    if [[ ${os_version} -eq 7 ]]; then
        echo -e "${red}注意： CentOS 7 无法使用hysteria1/2协议！${plain}\n"
    fi
elif [[ x"${release}" == x"ubuntu" ]]; then
    if [[ ${os_version} -lt 16 ]]; then
        echo -e "${red}请使用 Ubuntu 16 或更高版本的系统！${plain}\n" && exit 1
    fi
elif [[ x"${release}" == x"debian" ]]; then
    if [[ ${os_version} -lt 8 ]]; then
        echo -e "${red}请使用 Debian 8 或更高版本的系统！${plain}\n" && exit 1
    fi
fi

install_base() {
    # 优化版本：批量检查和安装包，减少系统调用
    need_install_apt() {
        local packages=("$@")
        local missing=()
        
        # 批量检查已安装的包
        local installed_list=$(dpkg-query -W -f='${Package}\n' 2>/dev/null | sort)
        
        for p in "${packages[@]}"; do
            if ! echo "$installed_list" | grep -q "^${p}$"; then
                missing+=("$p")
            fi
        done
        
        if [[ ${#missing[@]} -gt 0 ]]; then
            echo "安装缺失的包: ${missing[*]}"
            apt-get update -y >/dev/null 2>&1
            DEBIAN_FRONTEND=noninteractive apt-get install -y "${missing[@]}" >/dev/null 2>&1
        fi
    }

    need_install_yum() {
        local packages=("$@")
        local missing=()
        
        # 批量检查已安装的包
        local installed_list=$(rpm -qa --qf '%{NAME}\n' 2>/dev/null | sort)
        
        for p in "${packages[@]}"; do
            if ! echo "$installed_list" | grep -q "^${p}$"; then
                missing+=("$p")
            fi
        done
        
        if [[ ${#missing[@]} -gt 0 ]]; then
            echo "安装缺失的包: ${missing[*]}"
            yum install -y "${missing[@]}" >/dev/null 2>&1
        fi
    }

    need_install_apk() {
        local packages=("$@")
        local missing=()
        
        # 批量检查已安装的包
        local installed_list=$(apk info 2>/dev/null | sort)
        
        for p in "${packages[@]}"; do
            if ! echo "$installed_list" | grep -q "^${p}$"; then
                missing+=("$p")
            fi
        done
        
        if [[ ${#missing[@]} -gt 0 ]]; then
            echo "安装缺失的包: ${missing[*]}"
            apk add --no-cache "${missing[@]}" >/dev/null 2>&1
        fi
    }

    # 一次性安装所有必需的包
    if [[ x"${release}" == x"centos" ]]; then
        # 检查并安装 epel-release
        if ! rpm -q epel-release >/dev/null 2>&1; then
            echo "安装 EPEL 源..."
            yum install -y epel-release >/dev/null 2>&1
        fi
        need_install_yum wget curl unzip tar cronie socat ca-certificates pv
        update-ca-trust force-enable >/dev/null 2>&1 || true
    elif [[ x"${release}" == x"alpine" ]]; then
        need_install_apk wget curl unzip tar socat ca-certificates pv
        update-ca-certificates >/dev/null 2>&1 || true
    elif [[ x"${release}" == x"debian" ]]; then
        need_install_apt wget curl unzip tar cron socat ca-certificates pv
        update-ca-certificates >/dev/null 2>&1 || true
    elif [[ x"${release}" == x"ubuntu" ]]; then
        need_install_apt wget curl unzip tar cron socat ca-certificates pv
        update-ca-certificates >/dev/null 2>&1 || true
    elif [[ x"${release}" == x"arch" ]]; then
        echo "更新包数据库..."
        pacman -Sy --noconfirm >/dev/null 2>&1
        # --needed 会跳过已安装的包，非常高效
        echo "安装必需的包..."
        pacman -S --noconfirm --needed wget curl unzip tar cronie socat ca-certificates pv >/dev/null 2>&1
    fi
}

apply_v2node_network_tuning() {
    echo -e "${green}Applying V2node network tuning...${plain}"

    local sysctl_file="/etc/sysctl.d/99-v2node-speed.conf"
    mkdir -p /etc/sysctl.d
    cat > "$sysctl_file" <<'EOF'
# V2node speed tuning. Managed by V2node install script.
net.core.default_qdisc = fq
net.core.netdev_max_backlog = 250000
net.core.rmem_max = 67108864
net.core.wmem_max = 67108864
net.core.rmem_default = 8388608
net.core.wmem_default = 8388608
net.core.optmem_max = 65536
net.core.rps_sock_flow_entries = 32768
net.ipv4.tcp_congestion_control = bbr
net.ipv4.tcp_fastopen = 3
net.ipv4.tcp_slow_start_after_idle = 0
net.ipv4.tcp_mtu_probing = 1
net.ipv4.udp_rmem_min = 16384
net.ipv4.udp_wmem_min = 16384
net.ipv4.ip_local_port_range = 10000 65000
net.netfilter.nf_conntrack_max = 1048576
EOF

    touch /etc/sysctl.conf
    sed -i '/^# BEGIN V2node speed tuning$/,/^# END V2node speed tuning$/d' /etc/sysctl.conf
    {
        echo ""
        echo "# BEGIN V2node speed tuning"
        cat "$sysctl_file"
        echo "# END V2node speed tuning"
    } >> /etc/sysctl.conf

    sysctl -p "$sysctl_file" >/dev/null 2>&1 || true
    while IFS= read -r line; do
        case "$line" in
            ""|\#*) continue ;;
        esac
        sysctl -w "$line" >/dev/null 2>&1 || true
    done < "$sysctl_file"

    mkdir -p /usr/local/sbin
    cat > /usr/local/sbin/v2node-net-queues.sh <<'EOF'
#!/bin/sh
set -eu

iface="${1:-$(ip route show default 2>/dev/null | awk '/default/ {print $5; exit}')}"
[ -n "$iface" ] || exit 0
[ -d "/sys/class/net/$iface" ] || exit 0

cpu_count="$(nproc 2>/dev/null || echo 1)"
case "$cpu_count" in
    ''|*[!0-9]*) cpu_count=1 ;;
esac

if [ "$cpu_count" -ge 32 ]; then
    mask="ffffffff"
else
    mask="$(printf '%x' "$(( (1 << cpu_count) - 1 ))")"
fi

echo 32768 > /proc/sys/net/core/rps_sock_flow_entries 2>/dev/null || true

for queue in /sys/class/net/"$iface"/queues/rx-*; do
    [ -d "$queue" ] || continue
    echo "$mask" > "$queue/rps_cpus" 2>/dev/null || true
    echo 8192 > "$queue/rps_flow_cnt" 2>/dev/null || true
done

ip link set dev "$iface" txqueuelen 5000 2>/dev/null || true
EOF
    chmod +x /usr/local/sbin/v2node-net-queues.sh
    /usr/local/sbin/v2node-net-queues.sh >/dev/null 2>&1 || true

    if command -v systemctl >/dev/null 2>&1; then
        cat > /etc/systemd/system/v2node-net-queues.service <<'EOF'
[Unit]
Description=V2node network queue tuning
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/sbin/v2node-net-queues.sh
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF
        systemctl daemon-reload >/dev/null 2>&1 || true
        systemctl enable --now v2node-net-queues.service >/dev/null 2>&1 || true
    fi

    if ! command -v irqbalance >/dev/null 2>&1; then
        if command -v apt-get >/dev/null 2>&1; then
            DEBIAN_FRONTEND=noninteractive apt-get install -y irqbalance >/dev/null 2>&1 || true
        elif command -v yum >/dev/null 2>&1; then
            yum install -y irqbalance >/dev/null 2>&1 || true
        elif command -v apk >/dev/null 2>&1; then
            apk add --no-cache irqbalance >/dev/null 2>&1 || true
        elif command -v pacman >/dev/null 2>&1; then
            pacman -S --noconfirm --needed irqbalance >/dev/null 2>&1 || true
        fi
    fi
    if command -v systemctl >/dev/null 2>&1; then
        systemctl enable --now irqbalance >/dev/null 2>&1 || true
    elif command -v rc-update >/dev/null 2>&1; then
        rc-update add irqbalance default >/dev/null 2>&1 || true
        service irqbalance start >/dev/null 2>&1 || true
    fi

    echo -e "${green}V2node network tuning applied.${plain}"
}

apply_v2node_port_hopping() {
    echo -e "${green}Applying V2node HY2 port hopping...${plain}"

    if ! command -v iptables >/dev/null 2>&1; then
        if command -v apt-get >/dev/null 2>&1; then
            DEBIAN_FRONTEND=noninteractive apt-get install -y iptables >/dev/null 2>&1 || true
        elif command -v yum >/dev/null 2>&1; then
            yum install -y iptables >/dev/null 2>&1 || true
        elif command -v apk >/dev/null 2>&1; then
            apk add --no-cache iptables >/dev/null 2>&1 || true
        elif command -v pacman >/dev/null 2>&1; then
            pacman -S --noconfirm --needed iptables >/dev/null 2>&1 || true
        fi
    fi

    if ! command -v iptables >/dev/null 2>&1; then
        echo -e "${yellow}iptables not found, skip V2node HY2 port hopping.${plain}"
        return 0
    fi

    mkdir -p /usr/local/sbin
    cat > /usr/local/sbin/v2node-hy2-porthop.sh <<'EOF'
#!/bin/sh
set -eu

CHAIN="V2NODE_HY2_HOP"

while iptables -t nat -D PREROUTING -p udp --dport 51820:51920 -j REDIRECT --to-ports 51806 2>/dev/null; do
    :
done

iptables -t nat -N "$CHAIN" 2>/dev/null || iptables -t nat -F "$CHAIN"
while iptables -t nat -C PREROUTING -p udp -j "$CHAIN" 2>/dev/null; do
    iptables -t nat -D PREROUTING -p udp -j "$CHAIN" 2>/dev/null || break
done
iptables -t nat -I PREROUTING 1 -p udp -j "$CHAIN"

iptables -t nat -A "$CHAIN" -p udp --dport 55001:60000 -m comment --comment "v2node-gm-51801" -j REDIRECT --to-ports 51801
iptables -t nat -A "$CHAIN" -p udp --dport 50001:55000 -m comment --comment "v2node-nnm-51802" -j REDIRECT --to-ports 51802
iptables -t nat -A "$CHAIN" -p udp --dport 45001:50000 -m comment --comment "v2node-ovo-51803" -j REDIRECT --to-ports 51803
iptables -t nat -A "$CHAIN" -p udp --dport 40001:45000 -m comment --comment "v2node-yiyuan-51804" -j REDIRECT --to-ports 51804
iptables -t nat -A "$CHAIN" -p udp --dport 35001:40000 -m comment --comment "v2node-clash-51805" -j REDIRECT --to-ports 51805
iptables -t nat -A "$CHAIN" -p udp --dport 30001:35000 -m comment --comment "v2node-pianyi-51806" -j REDIRECT --to-ports 51806
EOF
    chmod +x /usr/local/sbin/v2node-hy2-porthop.sh

    if command -v systemctl >/dev/null 2>&1; then
        cat > /etc/systemd/system/v2node-hy2-porthop.service <<'EOF'
[Unit]
Description=V2node HY2 UDP port hopping redirect
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/sbin/v2node-hy2-porthop.sh
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF
        systemctl daemon-reload >/dev/null 2>&1 || true
        systemctl enable --now v2node-hy2-porthop.service >/dev/null 2>&1 || true
    else
        /usr/local/sbin/v2node-hy2-porthop.sh >/dev/null 2>&1 || true
        if command -v crontab >/dev/null 2>&1; then
            (crontab -l 2>/dev/null | grep -v '/usr/local/sbin/v2node-hy2-porthop.sh' || true; echo '@reboot /usr/local/sbin/v2node-hy2-porthop.sh >/dev/null 2>&1') | crontab -
        fi
    fi

    /usr/local/sbin/v2node-hy2-porthop.sh >/dev/null 2>&1 || true
    echo -e "${green}V2node HY2 port hopping applied.${plain}"
}

sync_v2node_masq_pages() {
    echo -e "${green}Syncing V2node masquerade pages...${plain}"

    local masq_root="/etc/v2node/masq"
    mkdir -p "$masq_root"

    cat > /tmp/v2node-masq-pages.list <<'EOF'
gm|https://xn--54qr1i.xn--oor32f63hs9js55d.com/
nnm|https://xn--i2r10aa.xn--oor32f63hs9js55d.com/
ovo|https://ovo.xn--oor32f63hs9js55d.com/
yiyuan|https://xn--4gq62f52gdss.xn--oor32f63hs9js55d.com/
clash|https://clash.xn--oor32f63hs9js55d.com/
pianyi|https://xn--wtq35pfyd55o.xn--oor32f63hs9js55d.com/
EOF

    local synced=0
    local failed=0
    while IFS='|' read -r site url; do
        [[ -z "$site" || -z "$url" ]] && continue
        mkdir -p "$masq_root/$site"
        local tmp_file="$masq_root/$site/index.html.tmp"
        local out_file="$masq_root/$site/index.html"
        if curl -fsSL --connect-timeout 8 --max-time 20 "$url" -o "$tmp_file" && [[ -s "$tmp_file" ]]; then
            mv "$tmp_file" "$out_file"
            chmod 0644 "$out_file" >/dev/null 2>&1 || true
            synced=$((synced + 1))
        else
            rm -f "$tmp_file"
            failed=$((failed + 1))
            echo -e "${yellow}Masquerade page sync failed: ${site}${plain}"
        fi
    done < /tmp/v2node-masq-pages.list
    rm -f /tmp/v2node-masq-pages.list

    echo -e "${green}V2node masquerade pages synced: ${synced} success, ${failed} failed.${plain}"
    return 0
}

# 0: running, 1: not running, 2: not installed
check_status() {
    if [[ ! -f /usr/local/v2node/v2node ]]; then
        return 2
    fi
    if [[ x"${release}" == x"alpine" ]]; then
        temp=$(service v2node status | awk '{print $3}')
        if [[ x"${temp}" == x"started" ]]; then
            return 0
        else
            return 1
        fi
    else
        temp=$(systemctl status v2node | grep Active | awk '{print $3}' | cut -d "(" -f2 | cut -d ")" -f1)
        if [[ x"${temp}" == x"running" ]]; then
            return 0
        else
            return 1
        fi
    fi
}

generate_v2node_config() {
        local api_host="$1"
        local node_id="$2"
        local api_key="$3"

        mkdir -p /etc/v2node >/dev/null 2>&1
        cat > /etc/v2node/config.json <<EOF
{
    "Log": {
        "Level": "warning",
        "Output": "",
        "Access": "none"
    },
    "Nodes": [
        {
            "ApiHost": "${api_host}",
            "NodeID": ${node_id},
            "ApiKey": "${api_key}",
            "Timeout": 15
        }
    ]
}
EOF
        echo -e "${green}V2node 配置文件生成完成,正在重新启动服务${plain}"
        if [[ x"${release}" == x"alpine" ]]; then
            service v2node restart
        else
            systemctl restart v2node
        fi
        sleep 2
        check_status
        echo -e ""
        if [[ $? == 0 ]]; then
            echo -e "${green}v2node 重启成功${plain}"
        else
            echo -e "${red}v2node 可能启动失败，请使用 v2node log 查看日志信息${plain}"
        fi
}

install_v2node() {
    local version_param="$1"
    if [[ -e /usr/local/v2node/ ]]; then
        rm -rf /usr/local/v2node/
    fi

    mkdir /usr/local/v2node/ -p
    cd /usr/local/v2node/

    if  [[ -z "$version_param" ]] ; then
        if [[ "$arch" == "64" ]]; then
            last_version="officialhy2"
            url="https://github.com/OxO-51888/V2node-HY2/releases/latest/download/v2node-linux-64.zip"
            sha_url="${url}.sha256"
            echo -e "${green}安装自用版：v2node official-hy2，开始下载...${plain}"
            curl -fLs "$url" | pv -s 32M -W -N "下载进度" > /usr/local/v2node/v2node-linux.zip
            if [[ $? -ne 0 ]]; then
                echo -e "${red}?? v2node official-hy2 ??,??? GitHub Release ??${plain}"
                exit 1
            fi
            if command -v sha256sum >/dev/null 2>&1; then
                curl -fLs "$sha_url" -o /usr/local/v2node/v2node-linux.zip.sha256 >/dev/null 2>&1 && \
                sed -i 's/  .*/  v2node-linux.zip/' /usr/local/v2node/v2node-linux.zip.sha256 && \
                (cd /usr/local/v2node && sha256sum -c v2node-linux.zip.sha256 >/dev/null 2>&1) || {
                    echo -e "${red}v2node official-hy2 校验失败${plain}"
                    exit 1
                }
            fi
        else
            last_version=$(curl -Ls "https://api.github.com/repos/OxO-51888/V2node-HY2/releases/latest" | grep '"tag_name":' | cut -d '"' -f 4)
            if [[ ! -n "$last_version" ]]; then
                echo -e "${red}检测 v2node 版本失败，可能是超出 Github API 限制，请稍后再试，或手动指定 v2node 版本安装${plain}"
                exit 1
            fi
            echo -e "${yellow}当前架构 ${arch} 暂无自用 official-hy2 包，回退安装官方版本：${last_version}${plain}"
            url="https://github.com/OxO-51888/V2node-HY2/releases/download/${last_version}/v2node-linux-${arch}.zip"
            curl -fLs "$url" | pv -s 30M -W -N "下载进度" > /usr/local/v2node/v2node-linux.zip
            if [[ $? -ne 0 ]]; then
                echo -e "${red}下载 v2node 失败，请确保你的服务器能够下载 Github 的文件${plain}"
                exit 1
            fi
        fi
    else
        last_version=$version_param
        url="https://github.com/OxO-51888/V2node-HY2/releases/download/${last_version}/v2node-linux-${arch}.zip"
        curl -fLs "$url" | pv -s 30M -W -N "下载进度" > /usr/local/v2node/v2node-linux.zip
        if [[ $? -ne 0 ]]; then
            echo -e "${red}下载 v2node $1 失败，请确保此版本存在${plain}"
            exit 1
        fi
    fi

    unzip v2node-linux.zip
    rm v2node-linux.zip -f
    chmod +x v2node
    mkdir /etc/v2node/ -p
    cp geoip.dat /etc/v2node/
    cp geosite.dat /etc/v2node/
    sync_v2node_masq_pages
    if [[ x"${release}" == x"alpine" ]]; then
        rm /etc/init.d/v2node -f
        cat <<EOF > /etc/init.d/v2node
#!/sbin/openrc-run

name="v2node"
description="v2node"

command="/usr/local/v2node/v2node"
command_args="server"
command_user="root"

pidfile="/run/v2node.pid"
command_background="yes"

depend() {
        need net
}
EOF
        chmod +x /etc/init.d/v2node
        rc-update add v2node default
        echo -e "${green}v2node ${last_version}${plain} 安装完成，已设置开机自启"
    else
        rm /etc/systemd/system/v2node.service -f
        cat <<EOF > /etc/systemd/system/v2node.service
[Unit]
Description=v2node Service
After=network.target nss-lookup.target
Wants=network.target

[Service]
User=root
Group=root
Type=simple
LimitAS=infinity
LimitRSS=infinity
LimitCORE=infinity
LimitNOFILE=999999
WorkingDirectory=/usr/local/v2node/
ExecStart=/usr/local/v2node/v2node server
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF
        systemctl daemon-reload
        systemctl stop v2node
        systemctl enable v2node
        echo -e "${green}v2node ${last_version}${plain} 安装完成，已设置开机自启"
    fi

    apply_v2node_network_tuning
    apply_v2node_port_hopping

    if [[ ! -f /etc/v2node/config.json ]]; then
        # 如果通过 CLI 传入了完整参数，则直接生成配置并跳过交互
        if [[ -n "$API_HOST_ARG" && -n "$NODE_ID_ARG" && -n "$API_KEY_ARG" ]]; then
            generate_v2node_config "$API_HOST_ARG" "$NODE_ID_ARG" "$API_KEY_ARG"
            echo -e "${green}已根据参数生成 /etc/v2node/config.json${plain}"
            first_install=false
        else
            cp config.json /etc/v2node/
            first_install=true
        fi
    else
        if [[ x"${release}" == x"alpine" ]]; then
            service v2node start
        else
            systemctl start v2node
        fi
        sleep 2
        check_status
        echo -e ""
        if [[ $? == 0 ]]; then
            echo -e "${green}v2node 重启成功${plain}"
        else
            echo -e "${red}v2node 可能启动失败，请使用 v2node log 查看日志信息${plain}"
        fi
        first_install=false
    fi


    curl -o /usr/bin/v2node -Ls https://raw.githubusercontent.com/OxO-51888/V2node-HY2/main/script/v2node.sh
    chmod +x /usr/bin/v2node

    cd $cur_dir
    rm -f install.sh
    echo "------------------------------------------"
    echo -e "管理脚本使用方法: "
    echo "------------------------------------------"
    echo "v2node              - 显示管理菜单 (功能更多)"
    echo "v2node start        - 启动 v2node"
    echo "v2node stop         - 停止 v2node"
    echo "v2node restart      - 重启 v2node"
    echo "v2node status       - 查看 v2node 状态"
    echo "v2node enable       - 设置 v2node 开机自启"
    echo "v2node disable      - 取消 v2node 开机自启"
    echo "v2node log          - 查看 v2node 日志"
    echo "v2node generate     - 生成 v2node 配置文件"
    echo "v2node tune         - 套用 V2node 网络优化"
    echo "v2node update       - 更新 v2node"
    echo "v2node update x.x.x - 更新 v2node 指定版本"
    echo "v2node install      - 安装 v2node"
    echo "v2node uninstall    - 卸载 v2node"
    echo "v2node version      - 查看 v2node 版本"
    echo "------------------------------------------"
    curl -fsS --max-time 10 "https://api.v-50.me/counter" || true

    if [[ $first_install == true ]]; then
        read -rp "检测到你为第一次安装 v2node，是否自动生成 /etc/v2node/config.json？(y/n): " if_generate
        if [[ "$if_generate" =~ ^[Yy]$ ]]; then
            # 交互式收集参数，提供示例默认值
            read -rp "面板API地址[格式: https://example.com/]: " api_host
            api_host=${api_host:-https://example.com/}
            read -rp "节点ID: " node_id
            node_id=${node_id:-1}
            read -rp "节点通讯密钥: " api_key

            # 生成配置文件（覆盖可能从包中复制的模板）
            generate_v2node_config "$api_host" "$node_id" "$api_key"
        else
            echo "${green}已跳过自动生成配置。如需后续生成，可执行: v2node generate${plain}"
        fi
    fi
}

parse_args "$@"
echo -e "${green}开始安装${plain}"
install_base
install_v2node "$VERSION_ARG"

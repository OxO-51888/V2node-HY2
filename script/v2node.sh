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

confirm() {
    if [[ $# > 1 ]]; then
        echo && read -rp "$1 [默认$2]: " temp
        if [[ x"${temp}" == x"" ]]; then
            temp=$2
        fi
    else
        read -rp "$1 [y/n]: " temp
    fi
    if [[ x"${temp}" == x"y" || x"${temp}" == x"Y" ]]; then
        return 0
    else
        return 1
    fi
}

confirm_restart() {
    confirm "是否重启v2node" "y"
    if [[ $? == 0 ]]; then
        restart
    else
        show_menu
    fi
}

before_show_menu() {
    echo && echo -n -e "${yellow}按回车返回主菜单: ${plain}" && read temp
    show_menu
}

install() {
    bash <(curl -Ls https://raw.githubusercontent.com/OxO-51888/V2node-HY2/main/script/install.sh)
    if [[ $? == 0 ]]; then
        if [[ $# == 0 ]]; then
            start
        else
            start 0
        fi
    fi
}

update() {
    if [[ $# == 0 ]]; then
        echo && echo -n -e "输入指定版本(默认最新版): " && read version
    else
        version=$2
    fi
    bash <(curl -Ls https://raw.githubusercontent.com/OxO-51888/V2node-HY2/main/script/install.sh) $version
    if [[ $? == 0 ]]; then
        echo -e "${green}更新完成，已自动重启 v2node，请使用 v2node log 查看运行日志${plain}"
        exit
    fi

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

config() {
    echo "v2node在修改配置后会自动尝试重启"
    vi /etc/v2node/config.json
    sleep 2
    restart
    check_status
    case $? in
        0)
            echo -e "v2node状态: ${green}已运行${plain}"
            ;;
        1)
            echo -e "检测到您未启动v2node或v2node自动重启失败，是否查看日志？[Y/n]" && echo
            read -e -rp "(默认: y):" yn
            [[ -z ${yn} ]] && yn="y"
            if [[ ${yn} == [Yy] ]]; then
               show_log
            fi
            ;;
        2)
            echo -e "v2node状态: ${red}未安装${plain}"
    esac
}

uninstall() {
    confirm "确定要卸载 v2node 吗?" "n"
    if [[ $? != 0 ]]; then
        if [[ $# == 0 ]]; then
            show_menu
        fi
        return 0
    fi
    if [[ x"${release}" == x"alpine" ]]; then
        service v2node stop
        rc-update del v2node
        rm /etc/init.d/v2node -f
    else
        systemctl stop v2node
        systemctl disable v2node
        rm /etc/systemd/system/v2node.service -f
        systemctl daemon-reload
        systemctl reset-failed
    fi
    rm /etc/v2node/ -rf
    rm /usr/local/v2node/ -rf

    echo ""
    echo -e "卸载成功，如果你想删除此脚本，则退出脚本后运行 ${green}rm /usr/bin/v2node -f${plain} 进行删除"
    echo ""

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

start() {
    check_status
    if [[ $? == 0 ]]; then
        echo ""
        echo -e "${green}v2node已运行，无需再次启动，如需重启请选择重启${plain}"
    else
        if [[ x"${release}" == x"alpine" ]]; then
            service v2node start
        else
            systemctl start v2node
        fi
        sleep 2
        check_status
        if [[ $? == 0 ]]; then
            echo -e "${green}v2node 启动成功，请使用 v2node log 查看运行日志${plain}"
        else
            echo -e "${red}v2node可能启动失败，请稍后使用 v2node log 查看日志信息${plain}"
        fi
    fi

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

stop() {
    if [[ x"${release}" == x"alpine" ]]; then
        service v2node stop
    else
        systemctl stop v2node
    fi
    sleep 2
    check_status
    if [[ $? == 1 ]]; then
        echo -e "${green}v2node 停止成功${plain}"
    else
        echo -e "${red}v2node停止失败，可能是因为停止时间超过了两秒，请稍后查看日志信息${plain}"
    fi

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

restart() {
    if [[ x"${release}" == x"alpine" ]]; then
        service v2node restart
    else
        systemctl restart v2node
    fi
    sleep 2
    check_status
    if [[ $? == 0 ]]; then
        echo -e "${green}v2node 重启成功，请使用 v2node log 查看运行日志${plain}"
    else
        echo -e "${red}v2node可能启动失败，请稍后使用 v2node log 查看日志信息${plain}"
    fi
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

status() {
    if [[ x"${release}" == x"alpine" ]]; then
        service v2node status
    else
        systemctl status v2node --no-pager -l
    fi
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

enable() {
    if [[ x"${release}" == x"alpine" ]]; then
        rc-update add v2node
    else
        systemctl enable v2node
    fi
    if [[ $? == 0 ]]; then
        echo -e "${green}v2node 设置开机自启成功${plain}"
    else
        echo -e "${red}v2node 设置开机自启失败${plain}"
    fi

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

disable() {
    if [[ x"${release}" == x"alpine" ]]; then
        rc-update del v2node
    else
        systemctl disable v2node
    fi
    if [[ $? == 0 ]]; then
        echo -e "${green}v2node 取消开机自启成功${plain}"
    else
        echo -e "${red}v2node 取消开机自启失败${plain}"
    fi

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

show_log() {
    if [[ x"${release}" == x"alpine" ]]; then
        echo -e "${red}alpine系统暂不支持日志查看${plain}\n" && exit 1
    else
        journalctl -u v2node.service -e --no-pager -f
    fi
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

update_shell() {
    wget -O /usr/bin/v2node -N --no-check-certificate https://raw.githubusercontent.com/OxO-51888/V2node-HY2/main/script/v2node.sh
    if [[ $? != 0 ]]; then
        echo ""
        echo -e "${red}下载脚本失败，请检查本机能否连接 Github${plain}"
        before_show_menu
    else
        chmod +x /usr/bin/v2node
        echo -e "${green}升级脚本成功，请重新运行脚本${plain}" && exit 0
    fi
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

porthop() {
    apply_v2node_port_hopping
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

apply_v2node_egress_guard() {
    echo -e "${green}Applying V2node egress guard...${plain}"

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
        echo -e "${yellow}iptables not found, skip V2node egress guard.${plain}"
        return 0
    fi

    mkdir -p /usr/local/sbin
    cat > /usr/local/sbin/v2node-egress-guard.sh <<'EOF'
#!/bin/sh
set -eu

CHAIN="V2NODE_EGRESS_GUARD"

iptables -N "$CHAIN" 2>/dev/null || iptables -F "$CHAIN"
while iptables -C OUTPUT -j "$CHAIN" 2>/dev/null; do
    iptables -D OUTPUT -j "$CHAIN" 2>/dev/null || break
done
iptables -I OUTPUT 1 -j "$CHAIN"

iptables -A "$CHAIN" -p tcp -m multiport --dports 6881:6889,6969,2710,51413,16881,8999 -j REJECT --reject-with tcp-reset
iptables -A "$CHAIN" -p udp -m multiport --dports 6881:6889,6969,2710,51413,16881,8999 -j DROP
iptables -A "$CHAIN" -p tcp -m multiport --dports 3333,3334,3335,4444,5555,7777,9999,14433,14444,18081,18082 -j REJECT --reject-with tcp-reset

add_tcp_string() {
    iptables -A "$CHAIN" -p tcp -m string --string "$1" --algo bm --to 65535 -j REJECT --reject-with tcp-reset 2>/dev/null || \
    iptables -A "$CHAIN" -p tcp -m string --string "$1" --algo bm -j REJECT --reject-with tcp-reset 2>/dev/null || true
}

add_udp_string() {
    iptables -A "$CHAIN" -p udp -m string --string "$1" --algo bm --to 65535 -j DROP 2>/dev/null || \
    iptables -A "$CHAIN" -p udp -m string --string "$1" --algo bm -j DROP 2>/dev/null || true
}

for pattern in \
    "BitTorrent protocol" "BitTorrent" "magnet:?xt=urn:btih" "peer_id=" ".torrent" "announce" "info_hash" \
    "uTorrent" "Transmission" "Azureus" \
    "stratum+tcp" "mining.subscribe" "mining.authorize" "eth_submitLogin" "eth_submitWork" \
    "falundafa.org" "minghui.org" "epochtimes.com" "ntdtv.com" "aboluowang.com" "secretchina.com"; do
    add_tcp_string "$pattern"
    add_udp_string "$pattern"
done
EOF
    chmod +x /usr/local/sbin/v2node-egress-guard.sh

    if command -v systemctl >/dev/null 2>&1; then
        cat > /etc/systemd/system/v2node-egress-guard.service <<'EOF'
[Unit]
Description=V2node egress guard
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/sbin/v2node-egress-guard.sh
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF
        systemctl daemon-reload >/dev/null 2>&1 || true
        systemctl enable --now v2node-egress-guard.service >/dev/null 2>&1 || true
    else
        /usr/local/sbin/v2node-egress-guard.sh >/dev/null 2>&1 || true
        if command -v crontab >/dev/null 2>&1; then
            (crontab -l 2>/dev/null | grep -v '/usr/local/sbin/v2node-egress-guard.sh' || true; echo '@reboot /usr/local/sbin/v2node-egress-guard.sh >/dev/null 2>&1') | crontab -
        fi
    fi

    /usr/local/sbin/v2node-egress-guard.sh >/dev/null 2>&1 || true
    echo -e "${green}V2node egress guard applied.${plain}"
}

egress_guard() {
    apply_v2node_egress_guard
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
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

check_enabled() {
    if [[ x"${release}" == x"alpine" ]]; then
        temp=$(rc-update show | grep v2node)
        if [[ x"${temp}" == x"" ]]; then
            return 1
        else
            return 0
        fi
    else
        temp=$(systemctl is-enabled v2node)
        if [[ x"${temp}" == x"enabled" ]]; then
            return 0
        else
            return 1;
        fi
    fi
}

check_uninstall() {
    check_status
    if [[ $? != 2 ]]; then
        echo ""
        echo -e "${red}v2node已安装，请不要重复安装${plain}"
        if [[ $# == 0 ]]; then
            before_show_menu
        fi
        return 1
    else
        return 0
    fi
}

check_install() {
    check_status
    if [[ $? == 2 ]]; then
        echo ""
        echo -e "${red}请先安装v2node${plain}"
        if [[ $# == 0 ]]; then
            before_show_menu
        fi
        return 1
    else
        return 0
    fi
}

show_status() {
    check_status
    case $? in
        0)
            echo -e "v2node状态: ${green}已运行${plain}"
            show_enable_status
            ;;
        1)
            echo -e "v2node状态: ${yellow}未运行${plain}"
            show_enable_status
            ;;
        2)
            echo -e "v2node状态: ${red}未安装${plain}"
    esac
}

show_enable_status() {
    check_enabled
    if [[ $? == 0 ]]; then
        echo -e "是否开机自启: ${green}是${plain}"
    else
        echo -e "是否开机自启: ${red}否${plain}"
    fi
}

show_v2node_version() {
    echo -n "v2node 版本："
    /usr/local/v2node/v2node version
    echo ""
    if [[ $# == 0 ]]; then
        before_show_menu
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


generate_config_file() {
    # 交互式收集参数，提供示例默认值
    read -rp "面板API地址[格式: https://example.com/]: " api_host
    api_host=${api_host:-https://example.com/}
    read -rp "节点ID: " node_id
    node_id=${node_id:-1}
    read -rp "节点通讯密钥: " api_key

    # 生成配置文件（覆盖可能从包中复制的模板）
    generate_v2node_config "$api_host" "$node_id" "$api_key"
}

# 放开防火墙端口
open_ports() {
    systemctl stop firewalld.service 2>/dev/null
    systemctl disable firewalld.service 2>/dev/null
    setenforce 0 2>/dev/null
    ufw disable 2>/dev/null
    iptables -P INPUT ACCEPT 2>/dev/null
    iptables -P FORWARD ACCEPT 2>/dev/null
    iptables -P OUTPUT ACCEPT 2>/dev/null
    iptables -t nat -F 2>/dev/null
    iptables -t mangle -F 2>/dev/null
    iptables -F 2>/dev/null
    iptables -X 2>/dev/null
    apply_v2node_port_hopping
    apply_v2node_egress_guard
    netfilter-persistent save 2>/dev/null
    echo -e "${green}放开防火墙端口成功！${plain}"
}

show_usage() {
    echo "v2node 管理脚本使用方法: "
    echo "------------------------------------------"
    echo "v2node              - 显示管理菜单 (功能更多)"
    echo "v2node start        - 启动 v2node"
    echo "v2node stop         - 停止 v2node"
    echo "v2node restart      - 重启 v2node"
    echo "v2node status       - 查看 v2node 状态"
    echo "v2node enable       - 设置 v2node 开机自启"
    echo "v2node disable      - 取消 v2node 开机自启"
    echo "v2node log          - 查看 v2node 日志"
    echo "v2node x25519       - 生成 x25519 密钥"
    echo "v2node generate     - 生成 v2node 配置文件"
    echo "v2node guard        - Rebuild V2node egress guard"
    echo "v2node update       - 更新 v2node"
    echo "v2node update x.x.x - 安装 v2node 指定版本"
    echo "v2node install      - 安装 v2node"
    echo "v2node uninstall    - 卸载 v2node"
    echo "v2node version      - 查看 v2node 版本"
    echo "------------------------------------------"
}

show_menu() {
    echo -e "
  ${green}v2node 后端管理脚本，${plain}${red}不适用于docker${plain}
--- https://github.com/OxO-51888/V2node-HY2 ---
  ${green}0.${plain} 修改配置
————————————————
  ${green}1.${plain} 安装 v2node
  ${green}2.${plain} 更新 v2node
  ${green}3.${plain} 卸载 v2node
————————————————
  ${green}4.${plain} 启动 v2node
  ${green}5.${plain} 停止 v2node
  ${green}6.${plain} 重启 v2node
  ${green}7.${plain} 查看 v2node 状态
  ${green}8.${plain} 查看 v2node 日志
————————————————
  ${green}9.${plain} 设置 v2node 开机自启
  ${green}10.${plain} 取消 v2node 开机自启
————————————————
  ${green}11.${plain} 查看 v2node 版本
  ${green}12.${plain} 升级 v2node 维护脚本
  ${green}13.${plain} 生成 v2node 配置文件
  ${green}14.${plain} 放行 VPS 的所有网络端口
  ${green}15.${plain} 退出脚本
 "
 #后续更新可加入上方字符串中
    show_status
    echo && read -rp "请输入选择 [0-15]: " num

    case "${num}" in
        0) config ;;
        1) check_uninstall && install ;;
        2) check_install && update ;;
        3) check_install && uninstall ;;
        4) check_install && start ;;
        5) check_install && stop ;;
        6) check_install && restart ;;
        7) check_install && status ;;
        8) check_install && show_log ;;
        9) check_install && enable ;;
        10) check_install && disable ;;
        11) check_install && show_v2node_version ;;
        12) update_shell ;;
        13) generate_config_file ;;
        14) open_ports ;;
        15) exit ;;
        *) echo -e "${red}请输入正确的数字 [0-15]${plain}" ;;
    esac
}


if [[ $# > 0 ]]; then
    case $1 in
        "start") check_install 0 && start 0 ;;
        "stop") check_install 0 && stop 0 ;;
        "restart") check_install 0 && restart 0 ;;
        "status") check_install 0 && status 0 ;;
        "enable") check_install 0 && enable 0 ;;
        "disable") check_install 0 && disable 0 ;;
        "log") check_install 0 && show_log 0 ;;
        "update") check_install 0 && update 0 $2 ;;
        "config") config $* ;;
        "generate") generate_config_file ;;
        "porthop") check_install 0 && porthop 0 ;;
        "guard") check_install 0 && egress_guard 0 ;;
        "install") check_uninstall 0 && install 0 ;;
        "uninstall") check_install 0 && uninstall 0 ;;
        "version") check_install 0 && show_v2node_version 0 ;;
        "update_shell") update_shell ;;
        *) show_usage
    esac
else
    show_menu
fi

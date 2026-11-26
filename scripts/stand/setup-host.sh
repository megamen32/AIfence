#!/usr/bin/env bash
set -euo pipefail

# Run once on server-100 as root. Refuse existing names rather than adopt state.
test "$(hostname)" = "${AIFENCE_EXPECT_HOST:?Set the intended deployment hostname}"
! getent passwd aifence-stand >/dev/null
test ! -e /srv/aifence-stand
test ! -e /etc/systemd/system/aifence-stand-docker.service
test ! -e '/etc/systemd/system/srv-aifence\x2dstand-data.mount'
test "$(df -Pk /srv | awk 'NR==2 {print $4}')" -gt 52428800

useradd -m -s /usr/sbin/nologin aifence-stand
id aifence-stand
grep -q '^aifence-stand:' /etc/subuid
grep -q '^aifence-stand:' /etc/subgid
install -d -m 0711 /srv/aifence-stand
truncate -s 40G /srv/aifence-stand/disk.img
chmod 0600 /srv/aifence-stand/disk.img
mkfs.ext4 -q -F /srv/aifence-stand/disk.img
install -d -m 0700 /srv/aifence-stand/data
cat > '/etc/systemd/system/srv-aifence\x2dstand-data.mount' <<'UNIT'
[Unit]
Description=AIfence bounded stand storage
[Mount]
What=/srv/aifence-stand/disk.img
Where=/srv/aifence-stand/data
Type=ext4
Options=loop,nodev,nosuid
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl start 'srv-aifence\x2dstand-data.mount'
chown aifence-stand:aifence-stand /srv/aifence-stand/data
chmod 0700 /srv/aifence-stand/data

cat > /etc/systemd/system/aifence-stand-docker.service <<'UNIT'
[Unit]
Description=AIfence isolated rootless Docker
After=network-online.target
Wants=network-online.target
RequiresMountsFor=/srv/aifence-stand/data
ConditionPathIsMountPoint=/srv/aifence-stand/data

[Service]
User=aifence-stand
Group=aifence-stand
RuntimeDirectory=aifence-stand
RuntimeDirectoryMode=0700
Environment=XDG_RUNTIME_DIR=/run/aifence-stand
Environment=DOCKERD_ROOTLESS_ROOTLESSKIT_NET=slirp4netns
Environment=DOCKERD_ROOTLESS_ROOTLESSKIT_PORT_DRIVER=builtin
ExecStart=/usr/bin/dockerd-rootless.sh --host=unix:///run/aifence-stand/docker.sock --data-root=/srv/aifence-stand/data/docker --exec-root=/run/aifence-stand/docker --pidfile=/run/aifence-stand/docker.pid --storage-driver=overlay2 --exec-opt=native.cgroupdriver=cgroupfs --log-driver=local --log-opt=max-size=5m --log-opt=max-file=2
Delegate=yes
MemoryHigh=4G
MemoryMax=6G
MemorySwapMax=0
CPUQuota=200%
TasksMax=1024
IOAccounting=yes
IOReadBandwidthMax=/dev/nvme0n1 50M
IOWriteBandwidthMax=/dev/nvme0n1 50M
LimitNOFILE=65536
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl start aifence-stand-docker.service
systemctl enable 'srv-aifence\x2dstand-data.mount' aifence-stand-docker.service
systemctl --no-pager --full status aifence-stand-docker.service

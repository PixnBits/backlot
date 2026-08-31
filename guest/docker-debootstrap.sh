#!/bin/bash
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq debootstrap e2fsprogs ca-certificates
debootstrap --variant=minbase bookworm /rootfs http://deb.debian.org/debian
chroot /rootfs apt-get update -qq
chroot /rootfs apt-get install -y -qq python3 bubblewrap gcc libc6-dev iproute2 util-linux procps
install -d /rootfs/opt/backlot /rootfs/usr/local/bin /rootfs/workspace /rootfs/sbin
cp -a /inner /rootfs/opt/backlot/inner
install -m 0755 /world-runtime /rootfs/usr/local/bin/world-runtime
install -m 0755 /guest/init.sh /rootfs/sbin/init
echo 'hello-from-workspace' > /rootfs/workspace/hello.txt
rm -f /out/rootfs.ext4
truncate -s 2G /out/rootfs.ext4
mkfs.ext4 -F -d /rootfs /out/rootfs.ext4
sync
ls -lh /out/rootfs.ext4

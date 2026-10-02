#!/usr/bin/env python
"""Send realistic software inventory for the seeded Linux devices.

Each check-in repeats the device facts seed.sh sent (so nothing about the
device changes) and adds an `inventory` object, exactly what the real agent
sends. The personal device gets one too, to prove the server drops it.

    python test/dev/seed-inventory.py   (after test/dev/seed.sh)
"""
import hashlib
import json
import os
import random
import ssl
import urllib.request

SERVER = os.environ.get("VS_DEV_BASE", "https://localhost:18443") + "/agent/v1/checkin"

# name, os, model, encrypted, family
DEVICES = [
    ("build-01", "Ubuntu 24.04.1 LTS", "Dell PowerEdge R650", True, "ubuntu24"),
    ("design-lap-07", "Fedora Linux 41", "Lenovo ThinkPad X1 Carbon", False, "fedora"),
    ("eng-ws-12", "Debian GNU/Linux 12", "Framework Laptop 13", True, "debian"),
    ("kiosk-lobby", "Ubuntu 22.04.4 LTS", "Intel NUC 13", True, "ubuntu22"),
    ("rahul-home", "Arch Linux", "ASUS Zenbook 14", False, "arch"),
    ("meera-personal", "Ubuntu 24.04.1 LTS", "HP EliteBook 840", True, "ubuntu24"),
]

# Packages every dpkg system has, with a version per family.
DPKG_BASE = """
base-files base-passwd bash bsdutils coreutils dash debconf debianutils diffutils dpkg e2fsprogs findutils
gcc-12-base grep gzip hostname init-system-helpers libacl1 libattr1 libaudit1 libblkid1 libbz2-1.0 libc-bin libc6
libcap2 libcrypt1 libdb5.3 libexpat1 libffi8 libgcc-s1 libgcrypt20 libgmp10 libgnutls30 libgpg-error0 libidn2-0
liblz4-1 liblzma5 libmd0 libmount1 libncurses6 libnettle8 libp11-kit0 libpam0g libpcre2-8-0 libseccomp2 libselinux1
libsmartcols1 libsqlite3-0 libssl3 libsystemd0 libtinfo6 libudev1 libunistring2 libuuid1 libxxhash0 libzstd1
login mawk mount ncurses-base ncurses-bin passwd perl-base sed sysvinit-utils tar tzdata util-linux zlib1g
""".split()
DPKG_COMMON = """
apt ca-certificates cron curl dbus dmidecode ethtool gnupg htop iproute2 iptables jq less locales logrotate lsof
man-db nano net-tools nftables openssh-client openssh-server openssl rsync rsyslog sudo systemd systemd-sysv
tcpdump tmux unzip vim wget xz-utils zip bash-completion bind9-dnsutils iputils-ping netcat-openbsd ufw
python3 python3-pip git make gcc build-essential
""".split()
DPKG_DESKTOP = """
firefox libreoffice-core gimp inkscape vlc thunderbird gnome-shell gnome-terminal nautilus pipewire
network-manager cups fonts-noto-core evince
""".split()
DPKG_DEV = """
docker-ce docker-ce-cli containerd.io docker-compose-plugin code google-chrome-stable golang-go nodejs npm
postgresql-client-16 terraform kubectl
""".split()
RPM_BASE = """
bash coreutils glibc glibc-common systemd systemd-libs dbus dbus-broker openssl openssl-libs curl libcurl
python3 python3-libs python3-pip git git-core vim-minimal vim-enhanced nano sudo openssh openssh-clients
openssh-server rsync tar gzip bzip2 xz zstd util-linux NetworkManager firewalld iproute iptables-nft nftables
dnf dnf-data rpm rpm-libs selinux-policy selinux-policy-targeted audit cronie logrotate rsyslog less man-db
which findutils grep sed gawk procps-ng shadow-utils passwd pam libselinux libseccomp gnutls nettle gmp
ca-certificates crypto-policies fedora-release fedora-release-common kernel kernel-core kernel-modules
kernel-modules-core grub2-common grub2-pc grub2-efi-x64 shim-x64 dracut plymouth fwupd bluez cups
gnome-shell gnome-terminal nautilus pipewire wireplumber mesa-dri-drivers firefox libreoffice-core gimp
inkscape vlc thunderbird docker-ce docker-ce-cli containerd.io code google-chrome-stable golang nodejs gcc make
tmux htop jq zip unzip wget tcpdump lsof strace
""".split()
PACMAN_BASE = """
base linux linux-firmware linux-headers glibc gcc-libs bash coreutils systemd systemd-libs openssl curl
python python-pip git vim nano sudo openssh rsync tar gzip bzip2 xz zstd util-linux networkmanager iproute2
iptables-nft nftables pacman pacman-mirrorlist archlinux-keyring ca-certificates ca-certificates-mozilla
dbus grub efibootmgr mesa vulkan-radeon xorg-server xorg-xinit gnome-shell gnome-terminal nautilus pipewire
pipewire-pulse wireplumber firefox libreoffice-fresh gimp inkscape vlc thunderbird docker docker-compose
containerd code go nodejs npm gcc make cmake tmux htop jq zip unzip wget tcpdump lsof strace ripgrep fd bat
fzf neovim zsh man-db less which findutils grep sed gawk procps-ng shadow pam libseccomp gnutls nettle gmp
cups bluez bluez-utils ttf-dejavu noto-fonts yay paru rust cargo
""".split()

# Per-package versions; devices differ so the fleet view shows a spread.
VERSIONS = {
    "ubuntu24": {"openssl": "3.0.13-0ubuntu3.4", "curl": "8.5.0-2ubuntu10.4", "firefox": "131.0.3+build1-0ubuntu0.24.04.1",
                 "docker-ce": "5:27.3.1-1~ubuntu.24.04~noble", "python3": "3.12.3-0ubuntu2", "git": "1:2.43.0-1ubuntu7.1",
                 "code": "1.94.2-1727878498", "google-chrome-stable": "129.0.6668.89-1"},
    "ubuntu22": {"openssl": "3.0.2-0ubuntu1.18", "curl": "7.81.0-1ubuntu1.18", "firefox": "130.0+build2-0ubuntu0.22.04.1",
                 "docker-ce": "5:26.1.4-1~ubuntu.22.04~jammy", "python3": "3.10.6-1~22.04.1", "git": "1:2.34.1-1ubuntu1.11",
                 "code": "1.92.2-1723660202", "google-chrome-stable": "128.0.6613.137-1"},
    "debian": {"openssl": "3.0.15-1~deb12u1", "curl": "7.88.1-10+deb12u8", "firefox": "128.3.1esr-1~deb12u1",
               "docker-ce": "5:27.3.1-1~debian.12~bookworm", "python3": "3.11.2-1+b1", "git": "1:2.39.5-0+deb12u1",
               "code": "1.94.2-1727878498", "google-chrome-stable": "129.0.6668.89-1"},
    "fedora": {"openssl": "3.2.2-7.fc41", "curl": "8.9.1-2.fc41", "firefox": "131.0-1.fc41", "docker-ce": "3:27.3.1-1.fc41",
               "python3": "3.13.0-1.fc41", "git": "2.47.0-1.fc41", "code": "1.94.2-1727878557.el8",
               "google-chrome-stable": "129.0.6668.89-1"},
    "arch": {"openssl": "3.3.2-1", "curl": "8.10.1-1", "firefox": "131.0.2-1", "docker": "1:27.3.1-1", "python": "3.12.7-1",
             "git": "2.47.0-1", "code": "1.94.2-1", "go": "2:1.23.2-1"},
}
PUBLISHERS = {"dpkg": "Ubuntu Developers", "rpm": "Fedora Project", "pacman": ""}

FLATPAK = [("org.mozilla.firefox", "Firefox", "131.0", "flathub"), ("com.slack.Slack", "Slack", "4.39.95", "flathub"),
           ("org.videolan.VLC", "VLC", "3.0.21", "flathub"), ("com.spotify.Client", "Spotify", "1.2.48.405", "flathub"),
           ("md.obsidian.Obsidian", "Obsidian", "1.7.4", "flathub")]
SNAP = [("core22", "20240111"), ("snapd", "2.63"), ("firefox", "131.0.2-1"), ("bare", "1.0")]

# unit, description, enabled
SERVICES = [
    ("ssh", "OpenBSD Secure Shell server", "enabled"), ("cron", "Regular background program processing daemon", "enabled"),
    ("dbus", "D-Bus System Message Bus", "static"), ("systemd-journald", "Journal Service", "static"),
    ("systemd-logind", "User Login Management", "static"), ("systemd-udevd", "Rule-based Manager for Device Events and Files", "static"),
    ("systemd-resolved", "Network Name Resolution", "enabled"), ("systemd-timesyncd", "Network Time Synchronization", "enabled"),
    ("NetworkManager", "Network Manager", "enabled"), ("rsyslog", "System Logging Service", "enabled"),
    ("polkit", "Authorization Manager", "static"), ("accounts-daemon", "Accounts Service", "enabled"),
    ("avahi-daemon", "Avahi mDNS/DNS-SD Stack", "enabled"), ("cups", "CUPS Scheduler", "enabled"),
    ("bluetooth", "Bluetooth service", "enabled"), ("ufw", "Uncomplicated firewall", "enabled"),
    ("docker", "Docker Application Container Engine", "enabled"), ("containerd", "containerd container runtime", "enabled"),
    ("unattended-upgrades", "Unattended Upgrades Shutdown", "enabled"), ("snapd", "Snap Daemon", "enabled"),
    ("fwupd", "Firmware update daemon", "static"), ("ModemManager", "Modem Manager", "enabled"),
    ("wpa_supplicant", "WPA supplicant", "enabled"), ("gdm", "GNOME Display Manager", "static"),
    ("udisks2", "Disk Manager", "enabled"), ("upower", "Daemon for power management", "enabled"),
    ("thermald", "Thermal Daemon Service", "enabled"), ("irqbalance", "irqbalance daemon", "enabled"),
    ("apparmor", "Load AppArmor profiles", "enabled"), ("auditd", "Security Auditing Service", "enabled"),
    ("firewalld", "firewalld - dynamic firewall daemon", "enabled"), ("chronyd", "NTP client/server", "enabled"),
    ("nginx", "A high performance web server and a reverse proxy server", "enabled"),
    ("postgresql", "PostgreSQL RDBMS", "enabled"), ("fail2ban", "Fail2Ban Service", "enabled"),
    ("apt-daily", "Daily apt download activities", "static"), ("e2scrub_all", "Online ext4 Metadata Check for All Filesystems", "static"),
    ("cloud-init", "Cloud-init: Network Stage", "enabled"), ("sshd", "OpenSSH server daemon", "enabled"),
    ("pipewire", "PipeWire Multimedia Service", "enabled"), ("smartd", "Self Monitoring and Reporting Technology Daemon", "enabled"),
]
# device -> (units, failed, stopped, exited)
SERVICE_PLAN = {
    "build-01": ("ssh cron dbus systemd-journald systemd-logind systemd-udevd systemd-resolved systemd-timesyncd rsyslog polkit docker containerd "
                 "unattended-upgrades snapd apparmor auditd nginx fail2ban apt-daily e2scrub_all ufw irqbalance smartd cloud-init NetworkManager", {"fail2ban": "failed", "smartd": "failed", "apt-daily": "stopped"}),
    "design-lap-07": ("sshd dbus systemd-journald systemd-logind systemd-udevd systemd-resolved NetworkManager firewalld chronyd polkit cups bluetooth "
                      "avahi-daemon gdm upower udisks2 fwupd ModemManager wpa_supplicant accounts-daemon auditd pipewire thermald docker containerd", {"bluetooth": "failed", "ModemManager": "failed", "cups": "stopped"}),
    "eng-ws-12": ("ssh cron dbus systemd-journald systemd-logind systemd-udevd systemd-resolved systemd-timesyncd NetworkManager rsyslog polkit docker containerd "
                  "gdm upower udisks2 fwupd bluetooth avahi-daemon cups accounts-daemon wpa_supplicant thermald apparmor postgresql ufw", {"postgresql": "failed", "bluetooth": "failed", "cups": "stopped", "avahi-daemon": "stopped"}),
    "kiosk-lobby": ("ssh cron dbus systemd-journald systemd-logind systemd-udevd systemd-resolved systemd-timesyncd NetworkManager rsyslog polkit unattended-upgrades "
                    "apparmor gdm upower wpa_supplicant snapd fwupd udisks2 accounts-daemon irqbalance ufw cloud-init avahi-daemon cups bluetooth ModemManager", {"snapd": "stopped", "ModemManager": "failed"}),
    "rahul-home": ("sshd dbus systemd-journald systemd-logind systemd-udevd systemd-resolved systemd-timesyncd NetworkManager polkit docker containerd bluetooth "
                   "cups avahi-daemon upower udisks2 wpa_supplicant fwupd pipewire thermald smartd ufw accounts-daemon gdm irqbalance rsyslog", {"docker": "failed", "cups": "stopped", "bluetooth": "stopped"}),
    "meera-personal": ("ssh cron dbus systemd-journald systemd-logind NetworkManager rsyslog polkit cups bluetooth gdm systemd-resolved systemd-timesyncd systemd-udevd unattended-upgrades snapd apparmor avahi-daemon accounts-daemon upower udisks2 fwupd wpa_supplicant thermald irqbalance ufw docker containerd", {"cups": "stopped"}),
}
SVC = {u: (d, e) for u, d, e in SERVICES}


def facts_body(name, os_, model, enc):
    issues = [] if enc else ["disk encryption required but no LUKS volume found"]
    return {
        "facts": {"hostname": name, "osPretty": os_, "model": model, "serial": "SN-" + name,
                  "diskEncrypted": enc, "firewall": True, "kernel": "6.8.0"},
        "compliance": {"compliant": enc, "issues": issues},
    }


def pick(rng, pool, n):
    pool = list(dict.fromkeys(pool))
    return rng.sample(pool, min(n, len(pool)))


def dpkg_apps(family, rng, desktop, dev):
    names = list(DPKG_BASE) + pick(rng, DPKG_COMMON, 36)
    names += ["openssl", "curl", "python3", "git"]
    if desktop:
        names += pick(rng, DPKG_DESKTOP, 8) + ["firefox"]
    if dev:
        names += pick(rng, DPKG_DEV, 6) + ["docker-ce", "code", "google-chrome-stable"]
    out, seen = [], set()
    for n in names:
        if n in seen:
            continue
        seen.add(n)
        v = VERSIONS[family].get(n) or "%d.%d.%d-%dubuntu%d" % (rng.randint(1, 9), rng.randint(0, 40), rng.randint(0, 20), rng.randint(0, 3), rng.randint(1, 6))
        if family == "debian" and n not in VERSIONS[family]:
            v = "%d.%d.%d-%d+deb12u%d" % (rng.randint(1, 9), rng.randint(0, 40), rng.randint(0, 20), rng.randint(1, 5), rng.randint(1, 4))
        pub = "Debian Developers" if family == "debian" else "Ubuntu Developers"
        if n in ("docker-ce", "docker-ce-cli", "containerd.io", "docker-compose-plugin"):
            pub = "Docker Release (CE deb)"
        if n == "code":
            pub = "Microsoft Corporation"
        if n == "google-chrome-stable":
            pub = "Chrome Linux Team"
        out.append({"name": n, "identifier": n, "version": v, "publisher": pub, "source": "dpkg"})
    return out


def rpm_apps(rng):
    out = []
    for n in RPM_BASE:
        v = VERSIONS["fedora"].get(n) or "%d.%d.%d-%d.fc41" % (rng.randint(1, 9), rng.randint(0, 40), rng.randint(0, 20), rng.randint(1, 8))
        pub = "Fedora Project"
        if n in ("docker-ce", "docker-ce-cli", "containerd.io"):
            pub = "Docker"
        if n == "code":
            pub = "Microsoft"
        if n == "google-chrome-stable":
            pub = "Google LLC"
        out.append({"name": n, "identifier": n, "version": v, "publisher": pub, "source": "rpm"})
    return out


def pacman_apps(rng):
    out = []
    for n in PACMAN_BASE:
        v = VERSIONS["arch"].get(n) or "%d.%d.%d-%d" % (rng.randint(1, 9), rng.randint(0, 40), rng.randint(0, 20), rng.randint(1, 3))
        out.append({"name": n, "identifier": n, "version": v, "publisher": "", "source": "pacman"})
    return out


def inventory(name, family):
    rng = random.Random("inv-" + name)
    if family in ("ubuntu24", "ubuntu22", "debian"):
        desktop = name not in ("build-01",)
        dev = name in ("build-01", "eng-ws-12", "meera-personal")
        apps = dpkg_apps(family, rng, desktop, dev)
    elif family == "fedora":
        apps = rpm_apps(rng)
    else:
        apps = pacman_apps(rng)
    if desktop_has_flatpak(name):
        for app_id, label, ver, origin in FLATPAK[: 3 if name != "eng-ws-12" else 5]:
            apps.append({"name": label, "identifier": app_id, "version": ver, "publisher": origin, "source": "flatpak"})
    if family in ("ubuntu24", "ubuntu22"):
        for n, v in SNAP[: 3 if name != "kiosk-lobby" else 2]:
            apps.append({"name": n, "identifier": n, "version": v, "publisher": "canonical" if n != "firefox" else "mozilla", "source": "snap"})
    units, states = SERVICE_PLAN[name]
    services = []
    for u in units.split():
        desc, enabled = SVC[u]
        state = states.get(u, "running")
        sub = {"running": "running", "failed": "failed", "stopped": "dead"}[state]
        if u in ("apt-daily", "e2scrub_all", "cloud-init") and state == "running":
            state, sub = "stopped", "exited"
        services.append({"name": u, "identifier": u + ".service", "source": "systemd", "state": state,
                         "details": {"sub": sub, "enabled": enabled, "description": desc}})
    h = hashlib.sha256(json.dumps([apps, services], sort_keys=True).encode()).hexdigest()[:16]
    return {"apps": apps, "services": services, "hash": h}


def desktop_has_flatpak(name):
    return name in ("design-lap-07", "eng-ws-12", "rahul-home", "meera-personal")


def post(name, body):
    d = "dev-" + name
    ctx = ssl.create_default_context(cafile="tls.crt")
    ctx.load_cert_chain(d + "/cert.pem", d + "/key.pem")
    req = urllib.request.Request(SERVER, data=json.dumps(body).encode(), headers={"Content-Type": "application/json"}, method="POST")
    with urllib.request.urlopen(req, context=ctx) as r:
        return json.load(r)


def main():
    for name, os_, model, enc, family in DEVICES:
        body = facts_body(name, os_, model, enc)
        inv = inventory(name, family)
        assert 60 <= len(inv["apps"]) <= 150 or name == "meera-personal", (name, len(inv["apps"]))
        assert 25 <= len(inv["services"]) <= 40, (name, len(inv["services"]))
        body["inventory"] = inv
        resp = post(name, body)
        failed = sum(1 for s in inv["services"] if s["state"] == "failed")
        print("%-15s %3d apps %2d services (%d failed)  personal=%s" % (name, len(inv["apps"]), len(inv["services"]), failed, resp.get("personal")))


if __name__ == "__main__":
    os.chdir(os.path.join(os.path.dirname(os.path.abspath(__file__)), ".run"))
    main()

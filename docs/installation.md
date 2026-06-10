# Installation

`yllmd` release artifacts are tarballs named by version, operating system, and architecture:

```text
yllmd_YY.MM.DD.NN-Release_<os>_<arch>.tar.gz
```

Everything without `-Release` in the version should be treated as development or beta.

## Layout

Recommended system layout:

```text
/usr/local/sbin/yllmd
/usr/local/bin/yllmctl
/usr/local/etc/yllmd/config.yaml
/usr/local/libexec/yllama-runner
/usr/local/share/doc/yllmd/
/var/lib/yllmd/
/var/lib/yllmd/models/
/var/run/yllmd/
/var/log/yllmd/
```

Recommended service account:

```text
user: yllmd
group: yllm
```

Applications that need daemon access should run as users in the `yllm` group.

## Install From Tarball

Extract the artifact for your platform:

```sh
tar -xzf yllmd_YY.MM.DD.NN-Release_<os>_<arch>.tar.gz
cd yllmd_YY.MM.DD.NN-Release_<os>_<arch>
```

Install binaries and docs:

```sh
install -d -m 0755 /usr/local/sbin /usr/local/bin /usr/local/etc/yllmd /usr/local/share/doc/yllmd
install -m 0755 yllmd /usr/local/sbin/yllmd
install -m 0755 yllmctl /usr/local/bin/yllmctl
install -m 0644 config.example.yaml /usr/local/etc/yllmd/config.yaml
cp -R README.md LICENSE docs packaging /usr/local/share/doc/yllmd/
```

Create service directories:

```sh
install -d -o yllmd -g yllm -m 0750 /var/lib/yllmd /var/lib/yllmd/models /var/run/yllmd /var/log/yllmd
```

Edit `/usr/local/etc/yllmd/config.yaml` for the local model tiers and runner path before starting the service.

## Linux systemd

Create the service account:

```sh
groupadd --system yllm
useradd --system --home-dir /var/lib/yllmd --shell /usr/sbin/nologin --gid yllm yllmd
```

Install and start the service:

```sh
install -m 0644 packaging/systemd/yllmd.service /etc/systemd/system/yllmd.service
systemctl daemon-reload
systemctl enable --now yllmd
```

Check the service:

```sh
systemctl status yllmd
yllmctl health
```

## FreeBSD rc.d

Create the service account:

```sh
pw groupadd yllm
pw useradd yllmd -g yllm -d /var/lib/yllmd -s /usr/sbin/nologin
```

Install and start the service:

```sh
install -m 0755 packaging/freebsd/yllmd /usr/local/etc/rc.d/yllmd
sysrc yllmd_enable=YES
service yllmd start
```

Check the service:

```sh
service yllmd status
yllmctl health
```

## macOS launchd

Create the service account and group using your organization's standard account-management process. The launchd plist expects:

```text
user: yllmd
group: yllm
```

Install and start the service:

```sh
install -m 0644 packaging/launchd/com.yanizio.yllmd.plist /Library/LaunchDaemons/com.yanizio.yllmd.plist
launchctl bootstrap system /Library/LaunchDaemons/com.yanizio.yllmd.plist
launchctl enable system/com.yanizio.yllmd
```

Check the service:

```sh
launchctl print system/com.yanizio.yllmd
yllmctl health
```

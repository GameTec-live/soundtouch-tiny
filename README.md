# soundtouch-tiny

`soundtouch-tiny` is a small on-device replacement for the Bose SoundTouch cloud endpoints that are needed after boot and for TuneIn-style internet radio presets.

It is intentionally much smaller than a full SoundTouch cloud clone. The goal is to run directly on the speaker, listen on `127.0.0.1:8000`, satisfy the firmware's cloud calls, resolve radio station playback, and proxy HTTPS streams for old firmware with stale CA certificates.

This is heavily inspired by https://github.com/gesellix/Bose-SoundTouch . Thanks to all of the amazing work over there.

## Features

- Minimal HTTP cloud replacement for the endpoints the speaker expects at boot.
- TuneIn/BMX playback responses for stored radio presets.
- HTTPS stream proxying through the speaker-local server.
- Optional JSON preset/station config, but defaults work without a config file.
- `migrate` command that installs the binary on the speaker and redirects Bose cloud URLs to `http://127.0.0.1:8000`.
- `configure` command to search TuneIn and write presets through the speaker's local API.
- `wifi` command to scan WiFi networks and add a wireless profile, including hidden/future SSIDs via flags.
- Optional `migrate -usb-serial` mode that replaces the USB Ethernet gadget with a Linux-friendly CDC ACM serial root shell.
- `uninstall` command that restores the original Bose config and removes soundtouch-tiny files.

## Supported target

The tested target is a SoundTouch 10 speaker running ARMv7 Linux. The release asset you normally want is:

```text
soundtouch-tiny-linux-armv7
```

The binary is static and uses only Go's standard library.

## One-line on-device install

Enable SSH first, then SSH into the speaker as `root` and run:

```sh
curl -fsSL https://github.com/GameTec-live/soundtouch-tiny/releases/download/main-build/soundtouch-tiny-linux-armv7 -o /tmp/soundtouch-tiny && chmod +x /tmp/soundtouch-tiny && /tmp/soundtouch-tiny migrate -usb-serial
```

If you do not want the USB serial shell, omit `-usb-serial`:

```sh
curl -fsSL https://github.com/GameTec-live/soundtouch-tiny/releases/download/main-build/soundtouch-tiny-linux-armv7 -o /tmp/soundtouch-tiny && chmod +x /tmp/soundtouch-tiny && /tmp/soundtouch-tiny migrate
```

Power-cycle the speaker manually after migration. The migration command does not reboot unless you explicitly pass `-reboot`.

## Enable SSH on the speaker

SoundTouch firmware can enable root SSH from a specially prepared USB stick. This is community-discovered behavior, not an official Bose feature.

1. Use a small USB stick.
2. Partition it as MBR.
3. Create one FAT/FAT32 partition.
4. Set the partition's boot flag. Some speakers accept a normal FAT32 stick, but the boot flag improves compatibility.
5. Create an empty file named `remote_services` in the root of the USB stick. It must have no extension.
6. Insert the USB stick into the speaker's USB/SERVICE port.
7. Power-cycle the speaker manually.
8. Wait about 30 to 60 seconds.
9. Connect with:

```sh
ssh -oHostKeyAlgorithms=+ssh-rsa root@<speaker-ip>
```

Some OpenSSH versions also need:

```sh
ssh -oHostKeyAlgorithms=+ssh-rsa -oPubkeyAcceptedAlgorithms=+ssh-rsa root@<speaker-ip>
```

There is no password.

On Linux, one way to prepare the stick is:

```sh
sudo parted /dev/sdX --script mklabel msdos
sudo parted /dev/sdX --script mkpart primary fat32 1MiB 100%
sudo parted /dev/sdX --script set 1 boot on
sudo mkfs.vfat -F 32 /dev/sdX1
sudo mount /dev/sdX1 /mnt
sudo touch /mnt/remote_services
sync
sudo umount /mnt
```

Replace `/dev/sdX` with the actual USB block device. Do not run this against your system disk.

Notes from community reports:

- The `remote_services` file is the important trigger.
- FAT/FAT32 is expected; exFAT is not a safe choice.
- Some speakers/firmware builds need the MBR partition table and boot flag.
- On firmware that accepts the unlock, root SSH remains available after boot. If it does not, try a different small USB stick and verify the file name exactly.

Sources: the Bose SoundTouch Toolkit migration guide documents the `remote_services` USB unlock and `ssh-rsa` SSH command, including the boot-flag compatibility note; Tim Van Wassenhove's SoundCork write-up documents the same `remote_services` FAT32 procedure and notes that `touch /mnt/nv/remote_services` can make the unlock persistent.

## Factory reset and WiFi setup

A factory reset wipes WiFi credentials, account pairing, presets, and local app state. It does not downgrade firmware. Use it when the speaker is wedged or when you want a clean setup.

Common reset sequences:

| Model | Factory reset sequence | Expected result |
| --- | --- | --- |
| SoundTouch 10 | Power on, then hold **Preset 1** + **Volume -** for about 10 seconds | WiFi indicator turns solid amber |
| SoundTouch 20 | Power on, then hold **Preset 1** + **Volume -** for about 10 seconds | Lights sweep/blink, then setup mode |
| SoundTouch 20/30 Series III | Hold **Preset 1** + **Preset 6** for about 10 seconds | White LED sweep, then setup mode |
| SoundTouch 300 | Hold **Volume -** until the light bar blinks rapidly, roughly 15 seconds | Rapid blink, then reboot/setup |

If your model has a recessed reset button, holding it for about 10 seconds is another common reset path. Button combinations vary by hardware generation, so confirm the speaker entered setup mode: the WiFi LED should glow solid amber and/or the display should show setup instructions.

After reset, the speaker normally creates its own setup WiFi network named like:

```text
Bose SoundTouch XXXX
```

Connect your computer or phone to that network. The speaker is usually reachable at:

```text
http://192.0.2.1
```

The browser setup page can be used directly. If you prefer command line setup, push the new WiFi profile through the speaker API:

```sh
HOME_SSID="MyNetwork"
HOME_PASS="MyPassword"

curl -s -X POST http://192.0.2.1:8090/performWirelessSiteSurvey \
  -H 'Content-Type: text/xml' \
  --data-raw '<PerformWirelessSiteSurvey timeout="5"/>'

curl -s -X POST http://192.0.2.1:8090/addWirelessProfile \
  -H 'Content-Type: text/xml' \
  --data-raw "<AddWirelessProfile><profile ssid=\"${HOME_SSID}\" password=\"${HOME_PASS}\" securityType=\"wpa_or_wpa2\" /></AddWirelessProfile>"
```

Expected response:

```xml
<?xml version="1.0" encoding="UTF-8" ?><AddWirelessProfileResponse />
```

The speaker will leave setup AP mode and join the new WiFi network after roughly 15 to 30 seconds. Reconnect your computer to the same home WiFi and find the speaker's new IP from your router, mDNS/Bonjour, or a network scan. Then enable SSH again if needed and run the one-line install/migrate command.

## Usage

### Run the server

Normally the init script installed by `migrate` starts the server:

```sh
soundtouch-tiny serve
```

Defaults:

- Port: `8000`
- Bind address: all interfaces unless configured otherwise
- External base URL: `http://127.0.0.1:8000`

### Migrate the speaker

```sh
soundtouch-tiny migrate
```

This:

- Copies the running binary to `/mnt/nv/soundtouch-tiny`.
- Creates `/opt/soundtouch-tiny` and `/usr/bin/soundtouch-tiny` symlinks.
- Installs `/etc/init.d/soundtouch-tiny`.
- Adds early boot autostart.
- Backs up `/opt/Bose/etc/SoundTouchSdkPrivateCfg.xml` to `.original`.
- Rewrites the Bose cloud URLs to `http://127.0.0.1:8000`.
- Adds a small login hint in `/mnt/nv/.profile`.
- Ensures SSH remains enabled across reboots by creating `/mnt/nv/remote_services` if it does not already exist.

Optional USB serial root shell:

```sh
soundtouch-tiny migrate -usb-serial
```

This changes the boot USB gadget from Ethernet to CDC ACM serial and starts a root shell on `ttyGS0`. It works on Linux as `/dev/ttyACM*`. Windows may incorrectly bind this gadget as RNDIS, so do not rely on it appearing as a COM port.

### Configure presets

```sh
soundtouch-tiny configure
```

This reads the current presets from the speaker API, asks which slot to change, searches TuneIn by station name, and writes the selected station to the preset.

### Configure WiFi

Interactive scan:

```sh
soundtouch-tiny wifi
```

Hidden or not-yet-available network:

```sh
soundtouch-tiny wifi -ssid "MyNetwork" -password "secret"
```

Open network:

```sh
soundtouch-tiny wifi -ssid "OpenNetwork" -security open
```

The command uses the speaker's local API at `http://127.0.0.1:8090` by default.

### Optional JSON config

On-device installs normally do not need a config file. If you run the server off-device or want static local radio preset definitions, start from:

```text
examples/soundtouch-tiny.json
```

Then run:

```sh
soundtouch-tiny serve -config examples/soundtouch-tiny.json
```

Schema:

| Field | Type | Default | Meaning |
| --- | --- | --- | --- |
| `account_id` | string | `1234567` | Fake/compatibility Bose account ID returned by cloud-account endpoints. |
| `server_url` | string | request host | External base URL used in generated links. For on-device use, leave empty. For off-device testing, set something like `http://192.168.1.50:8000`. |
| `device_id` | string | learned or `LOCALDEVICE` | Device ID to report before the speaker has identified itself. Usually leave empty. |
| `device_name` | string | `SoundTouch` | Friendly speaker name used in XML responses. |
| `ip_address` | string | learned from requests | IP address to report in XML responses. Usually leave empty. |
| `product_code` | string | `SoundTouch` | Product name/code returned to the speaker. |
| `device_serial_number` | string | empty | Optional device serial reported in account/device XML. |
| `product_serial_number` | string | empty | Optional product serial. Falls back to device serial/device ID where needed. |
| `firmware_version` | string | `27.0.6` | Firmware version string returned by compatibility endpoints. |
| `port` | number | `8000` | HTTP port for `serve`. `migrate` also rewrites Bose URLs to this port. |
| `proxy_streams` | boolean | `false` | Proxy every stream URL through `/bmx/stream`. Useful when the speaker cannot reach streams directly. |
| `proxy_https_streams` | boolean | `true` | Proxy only HTTPS streams. This is useful on old firmware with stale CA certificates. |
| `insecure_https` | boolean | `true` | Disable upstream TLS certificate verification for proxied HTTPS streams. Needed on many SoundTouch firmwares because their CA bundle is stale. |
| `presets` | array | `[]` | Optional static preset definitions. On-device presets usually live on the speaker, so this can be empty. |

Preset object fields:

| Field | Type | Default | Meaning |
| --- | --- | --- | --- |
| `slot` | number | required | Preset button number, `1` through `6`. Must be unique. |
| `name` | string | `Preset N` | Display name. |
| `source` | string | inferred | `LOCAL_INTERNET_RADIO` for direct stream URLs, `TUNEIN` for TuneIn station IDs, or another SoundTouch source if you know what you are doing. |
| `type` | string | `stationurl` | Content item type expected by the speaker. |
| `location` | string | source-dependent | TuneIn location such as `s16660`, `p12345`, or `/v1/playback/station/s16660`. For direct local radio, can be omitted if `stream_url` is set. |
| `stream_url` | string | empty | Direct internet radio stream URL. HTTPS URLs are normally proxied on-device. |
| `art` | string | empty | Optional image/art URL. |
| `source_account` | string | empty | Optional source account/username value in generated XML. |

Minimal direct-stream config:

```json
{
  "port": 8000,
  "proxy_https_streams": true,
  "insecure_https": true,
  "presets": [
    {
      "slot": 1,
      "name": "Radio Paradise",
      "source": "LOCAL_INTERNET_RADIO",
      "stream_url": "http://stream.radioparadise.com/mp3-128"
    }
  ]
}
```

### Uninstall

```sh
soundtouch-tiny uninstall
```

This restores the original Bose XML config and USB startup files if backups exist, removes init links, removes `/usr/bin/soundtouch-tiny`, removes `/opt/soundtouch-tiny`, removes `/mnt/nv/soundtouch-tiny`, and removes the login hint.

If `migrate` created `/mnt/nv/remote_services`, `uninstall` removes it. If that file already existed before migration, `uninstall` leaves it alone.

Power-cycle manually after uninstall to return the running USB gadget and boot-time behavior to stock.

## Manual build

Install Go, then run:

```sh
scripts/build.sh
```

Outputs are written to `build/`:

```text
build/soundtouch-tiny-linux-armv7
build/soundtouch-tiny-linux-arm64
build/soundtouch-tiny-linux-amd64
build/SHA256SUMS
```

For only the speaker binary:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags="-s -w" -o build/soundtouch-tiny-linux-armv7 .
```

## GitHub Actions release

The workflow in `.github/workflows/release-main.yml` runs on every push to `main`:

1. Runs `go test ./...`.
2. Builds Linux ARMv7, ARM64, and AMD64 binaries.
3. Publishes or updates a prerelease named `main-build`.
4. Uploads the binaries and `SHA256SUMS`.

The on-device install one-liner downloads from that moving `main-build` release.

## Safety notes

- `migrate` modifies files on the speaker's root filesystem and persistent volume.
- Backups are created before overwriting Bose config files.
- `uninstall` is designed to remove all soundtouch-tiny traces it created.
- Do not use `-reboot` unless you specifically want the command to reboot the speaker. Manual power-cycling has proven more reliable on some devices.
- Keep a copy of the release binary on your computer while testing.

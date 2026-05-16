package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const soundTouchPrivateCfgPath = "/opt/Bose/etc/SoundTouchSdkPrivateCfg.xml"
const defaultInstallDir = "/mnt/nv/soundtouch-tiny"
const defaultOptLink = "/opt/soundtouch-tiny"
const defaultInitPath = "/etc/init.d/soundtouch-tiny"
const defaultBinLink = "/usr/bin/soundtouch-tiny"
const defaultShelbyUSBPath = "/etc/init.d/shelby_usb"
const defaultLaunchGettyPath = "/usr/bin/launch_getty.sh"
const defaultLoginProfilePath = "/mnt/nv/.profile"
const defaultRemoteServicesPath = "/mnt/nv/remote_services"
const installedBinaryName = "soundtouch-tiny"
const loginHintBegin = "# soundtouch-tiny begin"
const loginHintEnd = "# soundtouch-tiny end"

type privateCfg struct {
	XMLName                    xml.Name `xml:"SoundTouchSdkPrivateCfg"`
	MargeServerURL             string   `xml:"margeServerUrl"`
	StatsServerURL             string   `xml:"statsServerUrl"`
	SwUpdateURL                string   `xml:"swUpdateUrl"`
	UsePandoraProductionServer bool     `xml:"usePandoraProductionServer"`
	IsZeroconfEnabled          bool     `xml:"isZeroconfEnabled"`
	SaveMargeCustomerReport    bool     `xml:"saveMargeCustomerReport"`
	BmxRegistryURL             string   `xml:"bmxRegistryUrl"`
}

type migrateOptions struct {
	Path               string
	ConfigPath         string
	Port               int
	Install            bool
	InstallDir         string
	OptLink            string
	InitPath           string
	BinLink            string
	ShelbyUSBPath      string
	LaunchGettyPath    string
	LoginProfilePath   string
	RemoteServicesPath string
	USBSerial          bool
	Start              bool
	Reboot             bool
}

func migrateOnDevice(opts migrateOptions) error {
	if opts.Path == "" {
		opts.Path = soundTouchPrivateCfgPath
	}
	if opts.Port == 0 {
		opts.Port = defaultPort
	}
	if opts.InstallDir == "" {
		opts.InstallDir = defaultInstallDir
	}
	if opts.OptLink == "" {
		opts.OptLink = defaultOptLink
	}
	if opts.InitPath == "" {
		opts.InitPath = defaultInitPath
	}
	if opts.BinLink == "" {
		opts.BinLink = defaultBinLink
	}
	if opts.ShelbyUSBPath == "" {
		opts.ShelbyUSBPath = defaultShelbyUSBPath
	}
	if opts.LaunchGettyPath == "" {
		opts.LaunchGettyPath = defaultLaunchGettyPath
	}
	if opts.LoginProfilePath == "" {
		opts.LoginProfilePath = defaultLoginProfilePath
	}
	if opts.RemoteServicesPath == "" {
		opts.RemoteServicesPath = defaultRemoteServicesPath
	}

	data, err := os.ReadFile(opts.Path)
	if err != nil {
		return err
	}
	next, err := rewritePrivateCfg(data, opts.Port)
	if err != nil {
		return err
	}

	if err := runCommand("mount", "-o", "remount,rw", "/"); err != nil {
		log.Printf("root remount rw failed, continuing in case it is already writable: %v", err)
	}
	rootReadOnly := false
	defer func() {
		if !rootReadOnly {
			_ = runCommand("mount", "-o", "remount,ro", "/")
		}
	}()

	if opts.Install {
		if err := installOnDevice(opts); err != nil {
			return err
		}
	}
	if opts.USBSerial {
		if err := installUSBSerial(opts); err != nil {
			return err
		}
	}

	if err := backupOnce(opts.Path, data); err != nil {
		return err
	}

	if err := os.WriteFile(opts.Path, next, 0644); err != nil {
		return err
	}

	base := "http://127.0.0.1:" + fmt.Sprint(opts.Port)
	_ = runCommand("envswitch", "boseurls", "set", base, base+"/updates/soundtouch")

	_ = runCommand("mount", "-o", "remount,ro", "/")
	rootReadOnly = true

	if opts.Start && !opts.Reboot && opts.Install {
		if err := runCommand(opts.InitPath, "restart"); err != nil {
			return err
		}
	}

	if opts.Reboot {
		return runCommand("reboot")
	}
	return nil
}

func installOnDevice(opts migrateOptions) error {
	if err := os.MkdirAll(opts.InstallDir, 0755); err != nil {
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := copyFileMode(exe, filepath.Join(opts.InstallDir, installedBinaryName), 0755); err != nil {
		return err
	}

	if opts.ConfigPath != "" {
		configDest := filepath.Join(opts.InstallDir, filepath.Base(opts.ConfigPath))
		if err := copyFileMode(opts.ConfigPath, configDest, 0644); err != nil {
			return err
		}
	}

	if err := refreshSymlink(opts.OptLink, opts.InstallDir); err != nil {
		return err
	}
	if err := refreshSymlink(opts.BinLink, filepath.Join(opts.OptLink, installedBinaryName)); err != nil {
		return err
	}

	script := initScript(initScriptOptions{
		Name:       "soundtouch-tiny",
		Daemon:     filepath.Join(opts.OptLink, installedBinaryName),
		ConfigPath: installedConfigPath(opts),
		Port:       opts.Port,
	})
	if err := os.WriteFile(opts.InitPath, []byte(script), 0755); err != nil {
		return err
	}
	if err := os.Chmod(opts.InitPath, 0755); err != nil {
		return err
	}

	if err := runCommand("update-rc.d", "soundtouch-tiny", "defaults", "01", "99"); err != nil {
		log.Printf("update-rc.d priority install failed, retrying defaults: %v", err)
		if err := runCommand("update-rc.d", "soundtouch-tiny", "defaults"); err != nil {
			return err
		}
	}
	if err := installLoginHint(opts.LoginProfilePath); err != nil {
		return err
	}
	if err := installPersistentSSH(opts); err != nil {
		return err
	}
	return nil
}

func installUSBSerial(opts migrateOptions) error {
	backupDir := filepath.Join(opts.InstallDir, "usb-serial")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return err
	}

	shelbyBackup := filepath.Join(backupDir, "shelby_usb.original")
	gettyBackup := filepath.Join(backupDir, "launch_getty.sh.original")
	if err := backupUSBFileOnce(opts.ShelbyUSBPath, shelbyBackup, filepath.Join(opts.InstallDir, "usb-serial-test", "shelby_usb.original")); err != nil {
		return err
	}
	if err := backupUSBFileOnce(opts.LaunchGettyPath, gettyBackup, filepath.Join(opts.InstallDir, "usb-serial-test", "launch_getty.sh.original")); err != nil {
		return err
	}

	shelbyBase, err := os.ReadFile(shelbyBackup)
	if err != nil {
		return err
	}
	shelbyNext, err := usbSerialShelbyScript(string(shelbyBase))
	if err != nil {
		return err
	}
	if err := os.WriteFile(opts.ShelbyUSBPath, []byte(shelbyNext), 0755); err != nil {
		return err
	}
	if err := os.Chmod(opts.ShelbyUSBPath, 0755); err != nil {
		return err
	}

	gettyBase, err := os.ReadFile(gettyBackup)
	if err != nil {
		return err
	}
	rootMarker := filepath.Join(opts.InstallDir, "usb-serial-root")
	gettyNext := usbSerialGettyScript(string(gettyBase), rootMarker)
	if err := os.WriteFile(opts.LaunchGettyPath, []byte(gettyNext), 0755); err != nil {
		return err
	}
	if err := os.Chmod(opts.LaunchGettyPath, 0755); err != nil {
		return err
	}
	return os.WriteFile(rootMarker, nil, 0644)
}

func uninstallFromDevice(opts migrateOptions) error {
	if opts.Path == "" {
		opts.Path = soundTouchPrivateCfgPath
	}
	if opts.InstallDir == "" {
		opts.InstallDir = defaultInstallDir
	}
	if opts.OptLink == "" {
		opts.OptLink = defaultOptLink
	}
	if opts.InitPath == "" {
		opts.InitPath = defaultInitPath
	}
	if opts.BinLink == "" {
		opts.BinLink = defaultBinLink
	}
	if opts.ShelbyUSBPath == "" {
		opts.ShelbyUSBPath = defaultShelbyUSBPath
	}
	if opts.LaunchGettyPath == "" {
		opts.LaunchGettyPath = defaultLaunchGettyPath
	}
	if opts.LoginProfilePath == "" {
		opts.LoginProfilePath = defaultLoginProfilePath
	}
	if opts.RemoteServicesPath == "" {
		opts.RemoteServicesPath = defaultRemoteServicesPath
	}

	if err := runCommand("mount", "-o", "remount,rw", "/"); err != nil {
		log.Printf("root remount rw failed, continuing in case it is already writable: %v", err)
	}
	rootReadOnly := false
	defer func() {
		if !rootReadOnly {
			_ = runCommand("mount", "-o", "remount,ro", "/")
		}
	}()

	if _, err := os.Stat(opts.InitPath); err == nil {
		_ = runCommand(opts.InitPath, "stop")
	} else if !os.IsNotExist(err) {
		return err
	}
	_ = runCommand("update-rc.d", "-f", "soundtouch-tiny", "remove")
	if err := removeInitLinks("soundtouch-tiny"); err != nil {
		return err
	}
	if err := removeLoginHint(opts.LoginProfilePath); err != nil {
		return err
	}
	if err := removePersistentSSHIfOwned(opts); err != nil {
		return err
	}

	if err := restoreFileAndRemoveBackup(opts.Path, opts.Path+".original", 0644); err != nil {
		return err
	}
	if err := restoreFirstBackup(opts.ShelbyUSBPath, 0755,
		filepath.Join(opts.InstallDir, "usb-serial", "shelby_usb.original"),
		filepath.Join(opts.InstallDir, "usb-serial-test", "shelby_usb.original"),
	); err != nil {
		return err
	}
	if err := restoreFirstBackup(opts.LaunchGettyPath, 0755,
		filepath.Join(opts.InstallDir, "usb-serial", "launch_getty.sh.original"),
		filepath.Join(opts.InstallDir, "usb-serial-test", "launch_getty.sh.original"),
	); err != nil {
		return err
	}

	_ = runCommand("envswitch", "boseurls", "set", "https://streaming.bose.com", "https://worldwide.bose.com/updates/soundtouch")

	for _, path := range []string{
		opts.InitPath,
		opts.BinLink,
		opts.OptLink,
		"/var/run/soundtouch-tiny.pid",
		"/etc/init.d/soundtouch-usb-rollback",
	} {
		if err := removeIfExists(path); err != nil {
			return err
		}
	}
	if err := removeInitLinks("soundtouch-usb-rollback"); err != nil {
		return err
	}
	if err := os.RemoveAll(opts.InstallDir); err != nil {
		return err
	}

	_ = runCommand("mount", "-o", "remount,ro", "/")
	rootReadOnly = true
	return nil
}

func installPersistentSSH(opts migrateOptions) error {
	if opts.RemoteServicesPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(opts.RemoteServicesPath), 0755); err != nil {
		return err
	}
	existed := true
	if _, err := os.Stat(opts.RemoteServicesPath); os.IsNotExist(err) {
		existed = false
	} else if err != nil {
		return err
	}
	if existed {
		return removeIfExists(persistentSSHOwnerPath(opts))
	}
	if err := os.WriteFile(opts.RemoteServicesPath, nil, 0644); err != nil {
		return err
	}
	return os.WriteFile(persistentSSHOwnerPath(opts), nil, 0644)
}

func removePersistentSSHIfOwned(opts migrateOptions) error {
	ownerPath := persistentSSHOwnerPath(opts)
	if _, err := os.Stat(ownerPath); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := removeIfExists(opts.RemoteServicesPath); err != nil {
		return err
	}
	return removeIfExists(ownerPath)
}

func persistentSSHOwnerPath(opts migrateOptions) string {
	return filepath.Join(opts.InstallDir, "remote_services.soundtouch-tiny")
}

func installLoginHint(path string) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	cleaned := removeLoginHintBlock(string(data))
	if strings.TrimSpace(cleaned) != "" && !strings.HasSuffix(cleaned, "\n") {
		cleaned += "\n"
	}
	hint := loginHintBlock()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(cleaned+hint), 0644)
}

func removeLoginHint(path string) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	next := removeLoginHintBlock(string(data))
	if strings.TrimSpace(next) == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return os.WriteFile(path, []byte(next), 0644)
}

func loginHintBlock() string {
	return fmt.Sprintf(`%s
if [ "$PS1" ] && [ -x /usr/bin/soundtouch-tiny ]; then
    echo
    echo "soundtouch-tiny is installed. Useful commands:"
    echo "  soundtouch-tiny configure    # edit presets"
    echo "  soundtouch-tiny wifi         # scan/connect WiFi"
    echo "  soundtouch-tiny uninstall    # restore stock Bose config"
    echo
fi
%s
`, loginHintBegin, loginHintEnd)
}

func removeLoginHintBlock(s string) string {
	for {
		start := strings.Index(s, loginHintBegin)
		if start < 0 {
			return s
		}
		end := strings.Index(s[start:], loginHintEnd)
		if end < 0 {
			return s
		}
		end += start + len(loginHintEnd)
		if end < len(s) && s[end] == '\r' {
			end++
		}
		if end < len(s) && s[end] == '\n' {
			end++
		}
		s = strings.TrimRight(s[:start]+s[end:], "\r\n") + "\n"
	}
}

func backupUSBFileOnce(src, backup, legacyBackup string) error {
	if _, err := os.Stat(backup); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if legacyBackup != "" {
		if _, err := os.Stat(legacyBackup); err == nil {
			return copyFileMode(legacyBackup, backup, 0755)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return copyFileMode(src, backup, 0755)
}

func restoreFirstBackup(dst string, mode os.FileMode, backups ...string) error {
	for _, backup := range backups {
		if backup == "" {
			continue
		}
		if _, err := os.Stat(backup); err == nil {
			return restoreFileAndRemoveBackup(dst, backup, mode)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func restoreFileAndRemoveBackup(dst, backup string, mode os.FileMode) error {
	if _, err := os.Stat(backup); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := copyFileMode(backup, dst, mode); err != nil {
		return err
	}
	return os.Remove(backup)
}

func removeIfExists(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err == nil || os.IsNotExist(err) {
		return nil
	} else {
		return err
	}
}

func removeInitLinks(name string) error {
	patterns := []string{
		filepath.Join("/etc", "rc*.d", "S??"+name),
		filepath.Join("/etc", "rc*.d", "K??"+name),
	}
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return err
		}
		for _, match := range matches {
			if err := removeIfExists(match); err != nil {
				return err
			}
		}
	}
	return nil
}

func usbSerialShelbyScript(base string) (string, error) {
	const modprobe = "modprobe g_cdc idVendor=0x1209 idProduct=0xb052 iManufacturer=SoundTouch iProduct=SoundTouch-CDC-Serial iSerialNumber=st-cdc-root || modprobe g_ether"
	lines := strings.Split(base, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(line, "modprobe g_ether") || strings.Contains(line, "modprobe g_cdc") || strings.Contains(line, "modprobe g_serial") {
			prefix := ""
			if idx := strings.Index(line, "modprobe"); idx >= 0 {
				prefix = line[:idx]
			}
			lines[i] = prefix + modprobe
			return strings.Join(lines, "\n"), nil
		}
	}
	return "", fmt.Errorf("could not find USB gadget modprobe line in %s", defaultShelbyUSBPath)
}

func usbSerialGettyScript(base, rootMarker string) string {
	body := strings.TrimPrefix(base, "#!/bin/sh\n")
	body = strings.TrimPrefix(body, "#!/bin/sh\r\n")
	return fmt.Sprintf(`#!/bin/sh

if [ -e %[1]q ]; then
    exec /sbin/getty -l /usr/bin/autologin.sh -n 115200 ttyGS0
fi

%[2]s`, rootMarker, body)
}

type initScriptOptions struct {
	Name       string
	Daemon     string
	ConfigPath string
	Port       int
}

func installedConfigPath(opts migrateOptions) string {
	if opts.ConfigPath == "" {
		return ""
	}
	return filepath.Join(opts.OptLink, filepath.Base(opts.ConfigPath))
}

func initScript(opts initScriptOptions) string {
	configLine := ""
	configReadyCheck := `[ -x "$DAEMON" ] && break`
	configReadCheck := ""
	configArg := ""
	if opts.ConfigPath != "" {
		configLine = fmt.Sprintf("CONFIG=%q\n", opts.ConfigPath)
		configReadyCheck = `[ -x "$DAEMON" ] && [ -r "$CONFIG" ] && break`
		configReadCheck = `    test -r "$CONFIG" || {
      echo "ERROR: Cannot read $CONFIG." >&2
      exit 1
    }
`
		configArg = ` -config "$CONFIG"`
	}

	return fmt.Sprintf(`#!/bin/sh
### BEGIN INIT INFO
# Provides:          %[1]s
# Required-Start:    $local_fs
# Required-Stop:     $local_fs
# X-Start-Before:    bose soundtouch
# Default-Start:     S 2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: Minimal local Bose SoundTouch cloud replacement
### END INIT INFO

NAME="%[1]s"
DESC="SoundTouch tiny cloud replacement"
DAEMON="%[2]s"
%[3]sPIDFILE="/var/run/$NAME.pid"
SCRIPTNAME="/etc/init.d/$NAME"
USER="root"

export PATH="/usr/local/sbin:/usr/local/bin:/sbin:/bin:/usr/sbin:/usr/bin"
unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy

is_running() {
  [ -f "$PIDFILE" ] || return 1
  PID="$(cat "$PIDFILE" 2>/dev/null)"
  [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null
}

case "$1" in
  start)
    if is_running; then
      echo "$NAME is already running."
      exit 0
    fi

    tries=0
    while [ $tries -lt 20 ]; do
      %[4]s
      sleep 1
      tries=$((tries + 1))
    done

    test -x "$DAEMON" || {
      echo "ERROR: Cannot execute $DAEMON." >&2
      exit 1
    }
%[5]s

    echo "Starting $DESC..."
    start-stop-daemon --start \
      --quiet \
      --pidfile "$PIDFILE" \
      --background \
      --make-pidfile \
      --chuid "$USER" \
      --startas "/bin/sh" \
      -- -c "unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy; exec \"$DAEMON\" serve%[6]s -port %[7]d -quiet >/dev/null 2>&1"

    tries=0
    while [ $tries -lt 20 ]; do
      if curl -fsS --max-time 2 http://127.0.0.1:%[7]d/ >/dev/null 2>&1; then
        exit 0
      fi
      sleep 1
      tries=$((tries + 1))
    done

    echo "ERROR: $NAME started but http://127.0.0.1:%[7]d did not respond." >&2
    exit 1
    ;;

  stop)
    echo "Stopping $DESC..."
    if [ -f "$PIDFILE" ]; then
      start-stop-daemon --stop --quiet --oknodo --pidfile "$PIDFILE"
      rm -f "$PIDFILE"
    else
      echo "No $NAME running (no PID file)." >&2
    fi
    ;;

  restart|force-reload)
    "$0" stop
    sleep 1
    "$0" start
    ;;

  status)
    if is_running; then
      if curl -fsS --max-time 2 http://127.0.0.1:%[7]d/ >/dev/null 2>&1; then
        echo "$NAME is running and responding."
        exit 0
      fi
      echo "$NAME process is running but HTTP is not responding." >&2
      exit 3
    fi
    echo "$NAME is not running."
    exit 3
    ;;

  *)
    echo "Usage: $SCRIPTNAME {start|stop|restart|force-reload|status}"
    exit 1
    ;;
esac

exit 0
`, opts.Name, opts.Daemon, configLine, configReadyCheck, configReadCheck, configArg, opts.Port)
}

func copyFileMode(src, dst string, mode os.FileMode) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	if dstInfo, err := os.Stat(dst); err == nil && os.SameFile(srcInfo, dstInfo) {
		return os.Chmod(dst, mode)
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func refreshSymlink(linkPath, target string) error {
	if linkPath == "" || linkPath == target {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(linkPath), 0755); err != nil {
		return err
	}
	info, err := os.Lstat(linkPath)
	if err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("%s exists and is not a symlink", linkPath)
		}
		current, err := os.Readlink(linkPath)
		if err == nil && current == target {
			return nil
		}
		if err := os.Remove(linkPath); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Symlink(target, linkPath)
}

func rewritePrivateCfg(data []byte, port int) ([]byte, error) {
	var cfg privateCfg
	if err := xml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.XMLName.Local == "" {
		cfg.XMLName.Local = "SoundTouchSdkPrivateCfg"
	}

	base := "http://127.0.0.1:" + fmt.Sprint(port)
	cfg.MargeServerURL = base
	cfg.StatsServerURL = base
	cfg.SwUpdateURL = base + "/updates/soundtouch"
	cfg.BmxRegistryURL = base + "/bmx/registry/v1/services"
	cfg.UsePandoraProductionServer = true
	cfg.IsZeroconfEnabled = true

	out, err := xml.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	buf.Write(out)
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

func backupOnce(path string, data []byte) error {
	backup := path + ".original"
	if _, err := os.Stat(backup); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(backup), 0755); err != nil {
		return err
	}
	return os.WriteFile(backup, data, 0644)
}

var commandRunner = realRunCommand

func runCommand(name string, args ...string) error {
	return commandRunner(name, args...)
}

func realRunCommand(name string, args ...string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("empty command")
	}
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRewritePrivateCfg(t *testing.T) {
	in := []byte(`<SoundTouchSdkPrivateCfg>
  <margeServerUrl>https://streaming.bose.com</margeServerUrl>
  <statsServerUrl>https://events.api.bosecm.com</statsServerUrl>
  <swUpdateUrl>https://worldwide.bose.com/updates/soundtouch</swUpdateUrl>
  <usePandoraProductionServer>false</usePandoraProductionServer>
  <isZeroconfEnabled>false</isZeroconfEnabled>
  <saveMargeCustomerReport>false</saveMargeCustomerReport>
  <bmxRegistryUrl>https://content.api.bose.io/bmx/registry/v1/services</bmxRegistryUrl>
</SoundTouchSdkPrivateCfg>`)

	out, err := rewritePrivateCfg(in, 8000)
	if err != nil {
		t.Fatalf("rewritePrivateCfg: %v", err)
	}
	body := string(out)
	for _, want := range []string{
		"<margeServerUrl>http://127.0.0.1:8000</margeServerUrl>",
		"<statsServerUrl>http://127.0.0.1:8000</statsServerUrl>",
		"<swUpdateUrl>http://127.0.0.1:8000/updates/soundtouch</swUpdateUrl>",
		"<bmxRegistryUrl>http://127.0.0.1:8000/bmx/registry/v1/services</bmxRegistryUrl>",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in\n%s", want, body)
		}
	}
}

func TestBackupOnceDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SoundTouchSdkPrivateCfg.xml")
	if err := os.WriteFile(path, []byte("current"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := backupOnce(path, []byte("first")); err != nil {
		t.Fatalf("backupOnce first: %v", err)
	}
	if err := backupOnce(path, []byte("second")); err != nil {
		t.Fatalf("backupOnce second: %v", err)
	}
	data, err := os.ReadFile(path + ".original")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "first" {
		t.Fatalf("backup = %q, want first", data)
	}
}

func TestInitScriptStartsEarlyAndQuiet(t *testing.T) {
	script := initScript(initScriptOptions{
		Name:   "soundtouch-tiny",
		Daemon: "/opt/soundtouch-tiny/soundtouch-tiny",
		Port:   8000,
	})
	for _, want := range []string{
		"# Required-Start:    $local_fs",
		"# Default-Start:     S 2 3 4 5",
		"# X-Start-Before:    bose soundtouch",
		"unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy",
		"serve -port 8000 -quiet >/dev/null 2>&1",
		"curl -fsS --max-time 2 http://127.0.0.1:8000/",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("missing %q in\n%s", want, script)
		}
	}
}

func TestInitScriptUsesOptionalConfig(t *testing.T) {
	script := initScript(initScriptOptions{
		Name:       "soundtouch-tiny",
		Daemon:     "/opt/soundtouch-tiny/soundtouch-tiny",
		ConfigPath: "/opt/soundtouch-tiny/custom.json",
		Port:       8000,
	})
	for _, want := range []string{
		`CONFIG="/opt/soundtouch-tiny/custom.json"`,
		`[ -x "$DAEMON" ] && [ -r "$CONFIG" ] && break`,
		`serve -config "$CONFIG" -port 8000 -quiet >/dev/null 2>&1`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("missing %q in\n%s", want, script)
		}
	}
}

func TestCopyFileModeSkipsSameFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "same")
	if err := os.WriteFile(path, []byte("body"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := copyFileMode(path, path, 0755); err != nil {
		t.Fatalf("copyFileMode same file: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0755 {
		t.Fatalf("mode = %v, want 0755", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "body" {
		t.Fatalf("data = %q, want body", data)
	}
}

func TestInstallOnDeviceCreatesPathSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink mode is environment-dependent on Windows")
	}

	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	opts := migrateOptions{
		InstallDir:         filepath.Join(dir, "nv", "soundtouch-tiny"),
		OptLink:            filepath.Join(dir, "opt", "soundtouch-tiny"),
		InitPath:           filepath.Join(dir, "etc", "init.d", "soundtouch-tiny"),
		BinLink:            filepath.Join(dir, "usr", "bin", "soundtouch-tiny"),
		LoginProfilePath:   filepath.Join(dir, "mnt", "nv", ".profile"),
		RemoteServicesPath: filepath.Join(dir, "mnt", "nv", "remote_services"),
		Port:               8000,
	}

	oldArgs := os.Args
	os.Args = []string{exe}
	defer func() { os.Args = oldArgs }()

	oldRunCommand := commandRunner
	commandRunner = func(name string, args ...string) error { return nil }
	defer func() { commandRunner = oldRunCommand }()

	if err := installOnDevice(opts); err != nil {
		t.Fatalf("installOnDevice: %v", err)
	}
	target, err := os.Readlink(opts.BinLink)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(opts.OptLink, installedBinaryName)
	if target != want {
		t.Fatalf("bin symlink = %q, want %q", target, want)
	}
	profile, err := os.ReadFile(opts.LoginProfilePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"soundtouch-tiny is installed", "soundtouch-tiny configure", "soundtouch-tiny wifi", "soundtouch-tiny uninstall"} {
		if !strings.Contains(string(profile), want) {
			t.Fatalf("login profile missing %q in\n%s", want, profile)
		}
	}
	if _, err := os.Stat(opts.RemoteServicesPath); err != nil {
		t.Fatalf("remote_services marker: %v", err)
	}
	if _, err := os.Stat(persistentSSHOwnerPath(opts)); err != nil {
		t.Fatalf("remote_services ownership marker: %v", err)
	}
}

func TestInstallUSBSerialUsesCDCAndRootGetty(t *testing.T) {
	dir := t.TempDir()
	shelbyPath := filepath.Join(dir, "etc", "init.d", "shelby_usb")
	gettyPath := filepath.Join(dir, "usr", "bin", "launch_getty.sh")
	installDir := filepath.Join(dir, "mnt", "nv", "soundtouch-tiny")
	if err := os.MkdirAll(filepath.Dir(shelbyPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(gettyPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shelbyPath, []byte("#!/bin/sh\ncase \"$1\" in\n  start)\n\tmodprobe g_ether\n\t;;\nesac\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gettyPath, []byte("#!/bin/sh\n\nexec /sbin/getty -l /usr/bin/spawn_telnet_longsleep.sh -n 115200 ttyGS0\n"), 0755); err != nil {
		t.Fatal(err)
	}

	opts := migrateOptions{
		InstallDir:      installDir,
		ShelbyUSBPath:   shelbyPath,
		LaunchGettyPath: gettyPath,
	}
	if err := installUSBSerial(opts); err != nil {
		t.Fatalf("installUSBSerial: %v", err)
	}

	shelby, err := os.ReadFile(shelbyPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"modprobe g_cdc",
		"idVendor=0x1209",
		"idProduct=0xb052",
		"iSerialNumber=st-cdc-root",
		"|| modprobe g_ether",
	} {
		if !strings.Contains(string(shelby), want) {
			t.Fatalf("shelby_usb missing %q in\n%s", want, shelby)
		}
	}

	getty, err := os.ReadFile(gettyPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"usb-serial-root",
		"exec /sbin/getty -l /usr/bin/autologin.sh -n 115200 ttyGS0",
		"spawn_telnet_longsleep.sh",
	} {
		if !strings.Contains(string(getty), want) {
			t.Fatalf("launch_getty.sh missing %q in\n%s", want, getty)
		}
	}
	if _, err := os.Stat(filepath.Join(installDir, "usb-serial-root")); err != nil {
		t.Fatalf("usb serial root marker: %v", err)
	}
	if _, err := os.Stat(filepath.Join(installDir, "usb-serial", "shelby_usb.original")); err != nil {
		t.Fatalf("shelby backup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(installDir, "usb-serial", "launch_getty.sh.original")); err != nil {
		t.Fatalf("getty backup: %v", err)
	}
}

func TestUninstallRestoresAndRemovesTinyFiles(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "opt", "Bose", "etc", "SoundTouchSdkPrivateCfg.xml")
	shelbyPath := filepath.Join(dir, "etc", "init.d", "shelby_usb")
	gettyPath := filepath.Join(dir, "usr", "bin", "launch_getty.sh")
	initPath := filepath.Join(dir, "etc", "init.d", "soundtouch-tiny")
	binLink := filepath.Join(dir, "usr", "bin", "soundtouch-tiny")
	optLink := filepath.Join(dir, "opt", "soundtouch-tiny")
	profilePath := filepath.Join(dir, "mnt", "nv", ".profile")
	remoteServicesPath := filepath.Join(dir, "mnt", "nv", "remote_services")
	installDir := filepath.Join(dir, "mnt", "nv", "soundtouch-tiny")
	for _, path := range []string{cfgPath, shelbyPath, gettyPath, initPath, binLink, optLink} {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(installDir, "usb-serial"), 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		cfgPath:               "tiny cfg",
		cfgPath + ".original": "stock cfg",
		shelbyPath:            "modprobe g_cdc\n",
		gettyPath:             "autologin ttyGS0\n",
		initPath:              "init",
		binLink:               "bin",
		optLink:               "opt",
		profilePath:           "alias ll='ls -l'\n" + loginHintBlock(),
		remoteServicesPath:    "",
		filepath.Join(installDir, "remote_services.soundtouch-tiny"):        "",
		filepath.Join(installDir, "soundtouch-tiny"):                        "installed",
		filepath.Join(installDir, "usb-serial", "shelby_usb.original"):      "modprobe g_ether\n",
		filepath.Join(installDir, "usb-serial", "launch_getty.sh.original"): "#!/bin/sh\nexec /sbin/getty -l /usr/bin/spawn_telnet_longsleep.sh -n 115200 ttyGS0\n",
	}
	for path, body := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0755); err != nil {
			t.Fatal(err)
		}
	}

	var commands []string
	oldRunCommand := commandRunner
	commandRunner = func(name string, args ...string) error {
		commands = append(commands, name+" "+strings.Join(args, " "))
		return nil
	}
	defer func() { commandRunner = oldRunCommand }()

	err := uninstallFromDevice(migrateOptions{
		Path:               cfgPath,
		InstallDir:         installDir,
		OptLink:            optLink,
		InitPath:           initPath,
		BinLink:            binLink,
		ShelbyUSBPath:      shelbyPath,
		LaunchGettyPath:    gettyPath,
		LoginProfilePath:   profilePath,
		RemoteServicesPath: remoteServicesPath,
	})
	if err != nil {
		t.Fatalf("uninstallFromDevice: %v", err)
	}

	for path, want := range map[string]string{
		cfgPath:    "stock cfg",
		shelbyPath: "modprobe g_ether\n",
		gettyPath:  "#!/bin/sh\nexec /sbin/getty -l /usr/bin/spawn_telnet_longsleep.sh -n 115200 ttyGS0\n",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want {
			t.Fatalf("%s = %q, want %q", path, data, want)
		}
	}
	for _, path := range []string{cfgPath + ".original", initPath, binLink, optLink, installDir} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists or stat failed: %v", path, err)
		}
	}
	if _, err := os.Stat(remoteServicesPath); !os.IsNotExist(err) {
		t.Fatalf("remote_services should have been removed, stat err: %v", err)
	}
	profile, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(profile) != "alias ll='ls -l'\n" {
		t.Fatalf("profile = %q", profile)
	}
	if !containsCommand(commands, "update-rc.d -f soundtouch-tiny remove") {
		t.Fatalf("missing update-rc.d remove in %#v", commands)
	}
	if !containsCommand(commands, "envswitch boseurls set https://streaming.bose.com https://worldwide.bose.com/updates/soundtouch") {
		t.Fatalf("missing envswitch restore in %#v", commands)
	}
}

func TestPersistentSSHPreexistingMarkerIsPreserved(t *testing.T) {
	dir := t.TempDir()
	opts := migrateOptions{
		InstallDir:         filepath.Join(dir, "mnt", "nv", "soundtouch-tiny"),
		RemoteServicesPath: filepath.Join(dir, "mnt", "nv", "remote_services"),
	}
	if err := os.MkdirAll(filepath.Dir(opts.RemoteServicesPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(opts.RemoteServicesPath, []byte("user-owned"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(opts.InstallDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := installPersistentSSH(opts); err != nil {
		t.Fatalf("installPersistentSSH: %v", err)
	}
	if _, err := os.Stat(persistentSSHOwnerPath(opts)); !os.IsNotExist(err) {
		t.Fatalf("ownership marker should not exist for pre-existing remote_services: %v", err)
	}
	if err := removePersistentSSHIfOwned(opts); err != nil {
		t.Fatalf("removePersistentSSHIfOwned: %v", err)
	}
	data, err := os.ReadFile(opts.RemoteServicesPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "user-owned" {
		t.Fatalf("remote_services = %q, want preserved user-owned marker", data)
	}
}

func TestLoginHintInstallIsIdempotentAndRemovable(t *testing.T) {
	profile := filepath.Join(t.TempDir(), ".profile")
	if err := os.WriteFile(profile, []byte("alias tap='telnet localhost 17000'\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := installLoginHint(profile); err != nil {
		t.Fatalf("installLoginHint first: %v", err)
	}
	if err := installLoginHint(profile); err != nil {
		t.Fatalf("installLoginHint second: %v", err)
	}
	data, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(data), loginHintBegin); count != 1 {
		t.Fatalf("hint count = %d in\n%s", count, data)
	}
	if err := removeLoginHint(profile); err != nil {
		t.Fatalf("removeLoginHint: %v", err)
	}
	data, err = os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "alias tap='telnet localhost 17000'\n" {
		t.Fatalf("profile after remove = %q", data)
	}
}

func containsCommand(commands []string, want string) bool {
	for _, command := range commands {
		if command == want {
			return true
		}
	}
	return false
}

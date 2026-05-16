package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "serve":
		if err := serve(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	case "migrate":
		if err := migrate(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	case "uninstall":
		if err := uninstall(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	case "configure":
		if err := configure(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	case "wifi":
		if err := wifi(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", "", "JSON config path")
	port := fs.Int("port", 0, "HTTP port override")
	quiet := fs.Bool("quiet", false, "suppress logs")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *quiet {
		log.SetOutput(io.Discard)
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	if *port != 0 {
		cfg.Port = *port
		if err := cfg.normalize(); err != nil {
			return err
		}
	}

	log.Printf("soundtouch-tiny serving %s", cfg.externalBaseURL())
	return http.ListenAndServe(cfg.listenAddr(), NewServer(cfg))
}

func migrate(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	path := fs.String("path", soundTouchPrivateCfgPath, "SoundTouchSdkPrivateCfg.xml path")
	configPath := fs.String("config", "", "JSON config path to install")
	port := fs.Int("port", defaultPort, "local server port")
	install := fs.Bool("install", true, "install binary, config, and autostart service")
	installDir := fs.String("install-dir", defaultInstallDir, "persistent install directory")
	usbSerial := fs.Bool("usb-serial", false, "replace USB ethernet gadget with Linux-friendly CDC ACM serial root shell")
	pairAccount := fs.Bool("pair-account", true, "pair factory-reset speaker with a local Marge account")
	accountID := fs.String("account-id", defaultAccountID, "7-digit local Marge account ID for -pair-account")
	start := fs.Bool("start", true, "start service after migration")
	reboot := fs.Bool("reboot", false, "reboot after migration")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return migrateOnDevice(migrateOptions{
		Path:             *path,
		ConfigPath:       *configPath,
		Port:             *port,
		Install:          *install,
		InstallDir:       *installDir,
		OptLink:          defaultOptLink,
		InitPath:         defaultInitPath,
		BinLink:          defaultBinLink,
		LoginProfilePath: defaultLoginProfilePath,
		USBSerial:        *usbSerial,
		Start:            *start,
		Reboot:           *reboot,
		PairAccount:      *pairAccount,
		AccountID:        *accountID,
	})
}

func uninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	path := fs.String("path", soundTouchPrivateCfgPath, "SoundTouchSdkPrivateCfg.xml path")
	installDir := fs.String("install-dir", defaultInstallDir, "persistent install directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return uninstallFromDevice(migrateOptions{
		Path:             *path,
		InstallDir:       *installDir,
		OptLink:          defaultOptLink,
		InitPath:         defaultInitPath,
		BinLink:          defaultBinLink,
		ShelbyUSBPath:    defaultShelbyUSBPath,
		LaunchGettyPath:  defaultLaunchGettyPath,
		LoginProfilePath: defaultLoginProfilePath,
	})
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage:
  soundtouch-tiny serve [-config config.json] [-port 8000] [-quiet]
  soundtouch-tiny migrate [-config config.json] [-path %s] [-port 8000] [-install-dir /mnt/nv/soundtouch-tiny] [-usb-serial] [-start=false] [-reboot]
  soundtouch-tiny uninstall [-path %s] [-install-dir /mnt/nv/soundtouch-tiny]
  soundtouch-tiny configure [-device http://127.0.0.1:8090]
  soundtouch-tiny wifi [-device http://127.0.0.1:8090] [-ssid SSID] [-password PASS] [-security wpa_or_wpa2]
`, soundTouchPrivateCfgPath, soundTouchPrivateCfgPath)
}

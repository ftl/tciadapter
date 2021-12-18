//go:build windows
// +build windows

package cmd

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ftl/tci/client"
	"github.com/ftl/tciadapter/adapter"
	"github.com/spf13/cobra"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

// see https://cs.opensource.google/go/x/sys/+/0f9fa26a:windows/svc/example/install.go

const serviceName = "tciadapter"

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Run the TCI adapter as Windows service (must not be used on the command line)",
	Run:   service,
}

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the TCI adapter as Windows service",
	Run:   install,
}

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Uninstall the Windows service",
	Run:   uninstall,
}

func init() {
	rootCmd.AddCommand(serviceCmd, installCmd, uninstallCmd)
}

func service(cmd *cobra.Command, args []string) {
	log.Printf("TCI-Hamlib Adapter %s", cmd.Version)

	runningAsService, err := svc.IsWindowsService()
	if !runningAsService || err != nil {
		log.Fatalf("not running as Windows service, do not use the 'service' command on the command line!")
	}
	log.Print("running as Windows service")

	err = svc.Run(serviceName, new(serviceHandler))
}

func install(cmd *cobra.Command, args []string) {
	log.Printf("TCI-Hamlib Adapter %s", cmd.Version)
	log.Print("installing tciadapter as Windows service")

	serviceFilename, err := exePath()
	if err != nil {
		log.Fatal(err)
	}

	serviceArgs := []string{
		"service",
		"-l", *rootFlags.localAddress,
		"-t", *rootFlags.tciHost,
		"-x", strconv.Itoa(*rootFlags.trx),
	}
	if *rootFlags.traceHamlib {
		serviceArgs = append(serviceArgs, "--trace_hamlib")
	}
	if *rootFlags.traceTCI {
		serviceArgs = append(serviceArgs, "--trace_tci")
	}
	if *rootFlags.noDigimodes {
		serviceArgs = append(serviceArgs, "-d")
	}

	serviceConfig := mgr.Config{
		StartType:   mgr.StartAutomatic,
		DisplayName: "TCI-Hamlib Adapter",
		Description: "Run the TCI-Hamlib adapter as a windows service",
	}

	log.Printf("service command: %s %s", serviceFilename, strings.Join(serviceArgs, " "))

	services, err := mgr.Connect()
	if err != nil {
		log.Fatal(err)
	}
	defer services.Disconnect()

	service, err := services.OpenService(serviceName)
	if err == nil {
		service.Close()
		log.Fatalf("the %s service already exists", serviceName)
	}

	service, err = services.CreateService(serviceName, serviceFilename, serviceConfig, serviceArgs...)
	if err != nil {
		log.Fatal(err)
	}
	defer service.Close()

	err = eventlog.InstallAsEventCreate(serviceName, eventlog.Error|eventlog.Warning|eventlog.Info)
	if err != nil {
		service.Delete()
		log.Fatalf("cannot setup log for the %s service: %v", serviceName, err)
	}
	log.Print("the tciadapter Windows service was sucessfully installed")
}

func uninstall(cmd *cobra.Command, args []string) {
	log.Printf("TCI-Hamlib Adapter %s", cmd.Version)
	log.Print("uninstalling the tciadapter Windows service")

	services, err := mgr.Connect()
	if err != nil {
		log.Fatal(err)
	}
	defer services.Disconnect()

	service, err := services.OpenService(serviceName)
	if err != nil {
		log.Fatalf("the %s Windows service is currently not installed: %v", serviceName, err)
	}
	defer service.Close()

	err = service.Delete()
	if err != nil {
		log.Fatal(err)
	}

	err = eventlog.Remove(serviceName)
	if err != nil {
		log.Fatalf("cannot remove log for the %s service: %v", serviceName, err)
	}
	log.Print("the tciadapter Windows service was sucessfully uninstalled")
}

func exePath() (string, error) {
	prog := os.Args[0]
	p, err := filepath.Abs(prog)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(p)
	if err == nil {
		if !fi.Mode().IsDir() {
			return p, nil
		}
		err = fmt.Errorf("%s is directory", p)
	}
	if filepath.Ext(p) == "" {
		p += ".exe"
		fi, err := os.Stat(p)
		if err == nil {
			if !fi.Mode().IsDir() {
				return p, nil
			}
			err = fmt.Errorf("%s is directory", p)
		}
	}
	return "", err
}

type serviceHandler struct{}

func (s *serviceHandler) Execute(args []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	if *rootFlags.traceHamlib {
		log.Print("hamlib tracing enabled")
	}
	if *rootFlags.traceTCI {
		log.Print("TCI tracing enabled")
	}
	if *rootFlags.noDigimodes {
		log.Print("no_digimodes: using LSB/USB instead of DIGL/DIGU")
	}
	tciHost, err := parseTCPAddrArg(*rootFlags.tciHost, "localhost", 40001)
	if err != nil {
		log.Fatalf("invalid tci_host: %v", err)
	}
	if tciHost.Port == 0 {
		tciHost.Port = client.DefaultPort
	}
	done := make(chan struct{})

	adapter, err := adapter.Listen(*rootFlags.localAddress, tciHost, *rootFlags.trx, done, *rootFlags.traceHamlib, *rootFlags.traceTCI, *rootFlags.noDigimodes)
	if err != nil {
		log.Fatalf("starting the adapter failed: %v", err)
	}

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}
	for {
		select {
		case c := <-requests:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				close(done)
				adapter.Wait()
				return
			default:
				log.Printf("unexpected control request #%d", c)
			}
		}
	}
}

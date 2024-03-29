package cmd

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/ftl/tci/client"
	"github.com/spf13/cobra"

	"github.com/ftl/tciadapter/adapter"
)

var rootFlags = struct {
	localAddress *string
	tciHost      *string
	trx          *int
	traceHamlib  *bool
	traceTCI     *bool
	noDigimodes  *bool
}{}

var rootCmd = &cobra.Command{
	Use:   "tciadapter",
	Short: "An adapter to connect Hamlib clients to TCI servers.",
	Run:   root,
}

func Execute(version string) {
	rootCmd.Version = version
	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

func init() {
	rootFlags.localAddress = rootCmd.PersistentFlags().StringP("local_address", "l", ":4532", "Use this local address to listen for incoming Hamlib connections")
	rootFlags.tciHost = rootCmd.PersistentFlags().StringP("tci_host", "t", "localhost:40001", "Connect the adapter to this TCI host")
	rootFlags.trx = rootCmd.PersistentFlags().IntP("trx", "x", 0, "Use this TRX of the TCI host")
	rootFlags.traceHamlib = rootCmd.PersistentFlags().BoolP("trace_hamlib", "", false, "Trace the Hamlib set commands on the console")
	rootFlags.traceTCI = rootCmd.PersistentFlags().BoolP("trace_tci", "", false, "Trace the TCI communication on the console")
	rootFlags.noDigimodes = rootCmd.PersistentFlags().BoolP("no_digimodes", "d", false, "Use LSB/USB instead of the digital modes DIGL/DIGU")
}

func root(cmd *cobra.Command, args []string) {
	log.Printf("TCI-Hamlib Adapter %s", cmd.Version)
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

	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	go handleCancelation(signals, cancel)

	adapter, err := adapter.Listen(*rootFlags.localAddress, tciHost, *rootFlags.trx, ctx.Done(), *rootFlags.traceHamlib, *rootFlags.traceTCI, *rootFlags.noDigimodes, cmd.Version)
	if err != nil {
		log.Fatalf("starting the adapter failed: %v", err)
	}
	adapter.Wait()
}

func handleCancelation(signals <-chan os.Signal, cancel context.CancelFunc) {
	count := 0
	for {
		select {
		case <-signals:
			count++
			if count == 1 {
				cancel()
			} else {
				log.Fatal("hard shutdown")
			}
		}
	}
}

func parseTCPAddrArg(arg string, defaultHost string, defaultPort int) (*net.TCPAddr, error) {
	host, port := splitHostPort(arg)
	if host == "" {
		host = defaultHost
	}
	if port == "" {
		port = strconv.Itoa(defaultPort)
	}

	return net.ResolveTCPAddr("tcp", fmt.Sprintf("%s:%s", host, port))
}

func splitHostPort(hostport string) (host, port string) {
	host = hostport

	colon := strings.LastIndexByte(host, ':')
	if colon != -1 && validOptionalPort(host[colon:]) {
		host, port = host[:colon], host[colon+1:]
	}

	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}

	return
}

func validOptionalPort(port string) bool {
	if port == "" {
		return true
	}
	if port[0] != ':' {
		return false
	}
	for _, b := range port[1:] {
		if b < '0' || b > '9' {
			return false
		}
	}
	return true
}

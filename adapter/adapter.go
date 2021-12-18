package adapter

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	hamlib "github.com/ftl/rigproxy/pkg/client"
	"github.com/ftl/rigproxy/pkg/protocol"
	tci "github.com/ftl/tci/client"
)

func Listen(localAddress string, tciHost *net.TCPAddr, trx int, done <-chan struct{}, traceHamlib, traceTCI bool, noDigimodes bool) (*Adapter, error) {
	listener, err := net.Listen("tcp", localAddress)
	if err != nil {
		return nil, fmt.Errorf("cannot open local port %s: %w", localAddress, err)
	}

	result := &Adapter{
		listener:    listener,
		trxData:     newTRXData(trx),
		closed:      make(chan struct{}),
		traceHamlib: traceHamlib,
		traceTCI:    traceTCI,
		noDigimodes: noDigimodes,
	}
	result.tciClient = tci.KeepOpen(tciHost, 10*time.Second, traceTCI)
	result.tciClient.Notify(result.trxData)

	go result.run()
	go func() {
		select {
		case <-done:
			result.Close()
			return
		case <-result.closed:
			listener.Close()
			result.Close()
			return
		}
	}()

	return result, nil
}

type Adapter struct {
	listener    net.Listener
	tciClient   *tci.Client
	trxData     *TRXData
	closed      chan struct{}
	traceHamlib bool
	traceTCI    bool
	noDigimodes bool
}

func (a *Adapter) run() {
	for {
		select {
		case <-a.closed:
			return
		default:
		}

		c, err := a.listener.Accept()
		if err != nil {
			log.Print(err)
			a.Close()
			return
		}

		conn := inboundConnection{
			conn:          c,
			tciClient:     a.tciClient,
			trxData:       a.trxData,
			adapterClosed: a.closed,
			closed:        make(chan struct{}),
			trace:         a.traceHamlib,
			noDigimodes:   a.noDigimodes,
		}
		go conn.run()
		go func() {
			select {
			case <-conn.adapterClosed:
				c.Close()
				conn.Close()
			case <-conn.closed:
				c.Close()
			default:
			}
		}()
	}
}

func (a *Adapter) Close() {
	select {
	case <-a.closed:
	default:
		close(a.closed)
	}
}

func (a *Adapter) Wait() {
	<-a.closed
}

type inboundConnection struct {
	conn          io.ReadWriteCloser
	tciClient     *tci.Client
	trxData       *TRXData
	adapterClosed <-chan struct{}
	closed        chan struct{}
	trace         bool
	noDigimodes   bool
}

func (c *inboundConnection) run() {
	defer c.conn.Close()
	r := protocol.NewRequestReader(c.conn)
	for {
		req, err := r.ReadRequest()
		if err == io.EOF {
			log.Print("connection EOF")
			c.Close()
			return
		}
		if err != nil {
			log.Printf("connection: %v", err)
			c.Close()
			return
		}

		resp, err := c.handleRequest(req)
		if strings.HasPrefix(string(req.Key()), "set_") && errors.Is(err, tci.ErrTimeout) {
			resp = protocol.Response{
				Command: req.Key(),
				Result:  "0",
			}
		} else if err != nil {
			log.Printf("request failed: %v", err)
			resp = protocol.Response{
				Command: req.Key(),
				Result:  "-1",
			}
		}

		if req.ExtendedSeparator != "" {
			fmt.Fprintln(c.conn, resp.ExtendedFormat(req.ExtendedSeparator))
		} else {
			fmt.Fprintln(c.conn, resp.Format())
		}
	}
}

func (c *inboundConnection) handleRequest(req protocol.Request) (protocol.Response, error) {
	key := string(req.Key())
	if c.trace && strings.HasPrefix(key, "set_") {
		log.Printf("%s", req.LongFormat())
	}
	switch key {
	case "chk_vfo":
		return protocol.ChkVFOResponse, nil
	case "dump_state":
		return protocol.DumpStateResponse, nil
	case "get_freq":
		return protocol.GetFreqResponse(c.trxData.CurrentVFOFrequency()), nil
	case "set_freq":
		if len(req.Args) < 1 {
			return protocol.NoResponse, fmt.Errorf("set_freq: no arguments")
		}
		frequency, err := strconv.ParseFloat(req.Args[0], 64)
		if err != nil {
			return protocol.NoResponse, fmt.Errorf("set_freq: invalid frequency: %w", err)
		}
		err = c.tciClient.SetVFOFrequency(c.trxData.trx, c.trxData.currentVFO, int(frequency))
		if err != nil {
			return protocol.NoResponse, fmt.Errorf("set_freq: cannot send TCI command: %w", err)
		}
		return protocol.OKResponse(req.Key()), nil
	case "get_vfo":
		vfo := tciToHamlibVFO[c.trxData.currentVFO]
		return protocol.GetVFOResponse(string(vfo)), nil
	case "set_vfo":
		if len(req.Args) < 1 {
			return protocol.NoResponse, fmt.Errorf("set_freq: no arguments")
		}
		c.trxData.currentVFO = map[hamlib.VFO]tci.VFO{hamlib.VFOA: tci.VFOA, hamlib.VFOB: tci.VFOB}[hamlib.VFO(req.Args[0])]
		return protocol.OKResponse(req.Key()), nil
	case "get_mode":
		mode := tciToHamlibMode[c.trxData.Mode()]
		min, max := c.trxData.RXFilterBand()
		passband := max - min
		return protocol.GetModeResponse(string(mode), passband), nil
	case "set_mode":
		if len(req.Args) < 2 {
			return protocol.NoResponse, fmt.Errorf("set_mode: no arguments")
		}
		mode := c.overrideDigimode(hamlibToTCIMode[hamlib.Mode(req.Args[0])])
		// passband, err := strconv.Atoi(req.Args[1]) // TODO also take the passband into account
		err := c.tciClient.SetMode(c.trxData.trx, mode)
		if err != nil {
			return protocol.NoResponse, fmt.Errorf("set_mode: cannot send TCI command: %w", err)
		}
		return protocol.OKResponse(req.Key()), nil
	case "get_split_vfo":
		return protocol.GetSplitVFOResponse(c.trxData.SplitEnable(), string(hamlib.VFOB)), nil
	case "set_split_vfo":
		if len(req.Args) < 2 {
			return protocol.NoResponse, fmt.Errorf("set_split_vfo: no arguments")
		}
		enabled := (req.Args[0] != "0")
		// TODO handle setting the TXVFO as this is usually VFOB in TCI
		err := c.tciClient.SetSplitEnable(c.trxData.trx, enabled)
		if err != nil {
			return protocol.NoResponse, fmt.Errorf("set_split_vfo: cannot send TCI command: %w", err)
		}
		return protocol.OKResponse(req.Key()), nil
	case "get_split_freq":
		return protocol.GetSplitFreqResponse(c.trxData.VFOFrequency(tci.VFOB)), nil
	case "set_split_freq":
		if len(req.Args) < 1 {
			return protocol.NoResponse, fmt.Errorf("set_split_freq: no arguments")
		}
		frequency, err := strconv.ParseFloat(req.Args[0], 64)
		if err != nil {
			return protocol.NoResponse, fmt.Errorf("set_split_freq: invalid frequency: %w", err)
		}
		err = c.tciClient.SetVFOFrequency(c.trxData.trx, tci.VFOB, int(frequency))
		if err != nil {
			return protocol.NoResponse, fmt.Errorf("set_split_freq: cannot send TCI command: %w", err)
		}
		return protocol.OKResponse(req.Key()), nil
	case "get_split_mode":
		mode := string(tciToHamlibMode[c.trxData.Mode()])
		min, max := c.trxData.RXFilterBand()
		passband := max - min
		return protocol.GetSplitModeResponse(mode, passband), nil
	case "set_split_mode":
		return protocol.OKResponse(req.Key()), nil
	case "get_ptt":
		return protocol.GetPTTResponse(c.trxData.TX()), nil
	case "set_ptt":
		if len(req.Args) < 1 {
			return protocol.NoResponse, fmt.Errorf("set_split_vfo: no arguments")
		}
		var enabled bool
		var source tci.SignalSource
		switch req.Args[0] {
		case "1":
			enabled = true
			source = tci.SignalSourceDefault
		case "2":
			enabled = true
			source = tci.SignalSourceMIC
		case "3":
			enabled = true
			source = tci.SignalSourceVAC
		}
		err := c.tciClient.SetTX(c.trxData.trx, enabled, source)
		if err != nil {
			return protocol.NoResponse, fmt.Errorf("set_ptt: cannot send TCI command: %w", err)
		}
		return protocol.OKResponse(req.Key()), nil
	default:
		return protocol.NoResponse, fmt.Errorf("unsupported request: %v", req.LongFormat())
	}
}

func (c *inboundConnection) overrideDigimode(mode tci.Mode) tci.Mode {
	if !c.noDigimodes {
		return mode
	}
	switch mode {
	case tci.ModeDIGL:
		return tci.ModeLSB
	case tci.ModeDIGU:
		return tci.ModeUSB
	default:
		return mode
	}
}

func (c *inboundConnection) Close() {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
}

var hamlibToTCIVFO = map[hamlib.VFO]tci.VFO{
	hamlib.VFOA: tci.VFOA,
	hamlib.VFOB: tci.VFOB,
}

var tciToHamlibVFO = map[tci.VFO]hamlib.VFO{
	tci.VFOA: hamlib.VFOA,
	tci.VFOB: hamlib.VFOB,
}

var hamlibToTCIMode = map[hamlib.Mode]tci.Mode{
	hamlib.ModeUSB:     tci.ModeUSB,
	hamlib.ModeLSB:     tci.ModeLSB,
	hamlib.ModeCW:      tci.ModeCW,
	hamlib.ModeCWR:     tci.ModeCW,
	hamlib.ModeRTTY:    tci.ModeDIGU,
	hamlib.ModeRTTYR:   tci.ModeDIGL,
	hamlib.ModeAM:      tci.ModeAM,
	hamlib.ModeFM:      tci.ModeNFM,
	hamlib.ModeWFM:     tci.ModeWFM,
	hamlib.ModePKTLSB:  tci.ModeDIGL,
	hamlib.ModePKTUSB:  tci.ModeDIGU,
	hamlib.ModePKTFM:   tci.ModeNFM,
	hamlib.ModeECSSLSB: tci.ModeDIGL,
	hamlib.ModeECSSUSB: tci.ModeDIGU,
	hamlib.ModeFAX:     tci.ModeDIGU,
	hamlib.ModeSAM:     tci.ModeSAM,
	hamlib.ModeDSB:     tci.ModeDSB,
}

var tciToHamlibMode = map[tci.Mode]hamlib.Mode{
	tci.ModeAM:   hamlib.ModeAM,
	tci.ModeSAM:  hamlib.ModeSAM,
	tci.ModeDSB:  hamlib.ModeDSB,
	tci.ModeLSB:  hamlib.ModeLSB,
	tci.ModeUSB:  hamlib.ModeUSB,
	tci.ModeCW:   hamlib.ModeCW,
	tci.ModeNFM:  hamlib.ModeFM,
	tci.ModeWFM:  hamlib.ModeWFM,
	tci.ModeDIGL: hamlib.ModePKTLSB,
	tci.ModeDIGU: hamlib.ModePKTUSB,
}

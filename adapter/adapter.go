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

func Listen(localAddress string, tciHost *net.TCPAddr, trx int, done <-chan struct{}, traceHamlib, traceTCI bool, noDigimodes bool, version string) (*Adapter, error) {
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
		version:     version,
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
	version     string
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
			version:       a.version,
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
	version       string
	modeLocked    bool
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

		var response string
		if req.ExtendedSeparator != "" {
			response = resp.ExtendedFormat(req.ExtendedSeparator)
		} else {
			response = resp.Format()
		}
		if c.trace {
			log.Printf("> %s", response)
		}
		fmt.Fprintln(c.conn, response)
	}
}

func (c *inboundConnection) handleRequest(req protocol.Request) (protocol.Response, error) {
	key := strings.ToLower(string(req.Key()))
	if c.trace {
		log.Printf("< %s (%s)", req.LongFormat(), key)
	}
	switch key {
	case "chk_vfo":
		return protocol.ChkVFOResponse, nil
	case "dump_state":
		return protocol.DumpStateResponse, nil
	case "dump_caps":
		return dumpCapsResponse(c.version), nil
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
		vfo, ok := hamlibToTCIVFO[hamlib.VFO(req.Args[0])]
		if !ok {
			return protocol.NoResponse, fmt.Errorf("set_vfo: unknown VFO %s", req.Args[0])
		}
		c.trxData.currentVFO = vfo
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
		if c.modeLocked {
			return protocol.OKResponse(req.Key()), nil
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
	case "send_morse":
		if len(req.Args) < 1 {
			return protocol.NoResponse, fmt.Errorf("send_morse: no arguments")
		}
		err := c.tciClient.SendCWMacro(c.trxData.trx, req.Args[0])
		if err != nil {
			return protocol.NoResponse, fmt.Errorf("send_morse: cannot send TCI command: %w", err)
		}
		return protocol.OKResponse(req.Key()), nil
	case "stop_morse":
		err := c.tciClient.StopCW()
		if err != nil {
			return protocol.NoResponse, fmt.Errorf("stop_morse: cannot send TCI command: %w", err)
		}
		return protocol.OKResponse(req.Key()), nil
	case "wait_morse":
		c.trxData.WaitForTransmissionEnd()
		return protocol.OKResponse(req.Key()), nil
	case "set_level_keyspd":
		if len(req.Args) < 2 {
			return protocol.NoResponse, fmt.Errorf("set_level: no arguments")
		}
		wpm, err := strconv.Atoi(req.Args[1])
		if err != nil {
			return protocol.NoResponse, fmt.Errorf("set_level: invalid keyer speed in WPM: %w", err)
		}
		err = c.tciClient.SetCWMacrosSpeed(wpm)
		if err != nil {
			return protocol.NoResponse, fmt.Errorf("set_level: cannot send TCI command: %w", err)
		}
		return protocol.OKResponse(req.Key()), nil
	case "get_level_keyspd":
		wpm, err := c.tciClient.CWMacrosSpeed()
		if err != nil {
			return protocol.NoResponse, fmt.Errorf("get_level: cannot send TCI command: %w", err)
		}
		return protocol.GetLevelKeyspdResponse(wpm), nil
	case "set_lock_mode":
		if len(req.Args) < 1 {
			return protocol.NoResponse, fmt.Errorf("set_lock_mode: no arguments")
		}
		c.modeLocked = (req.Args[0] == "1")
		return protocol.OKResponse(req.Key()), nil
	case "get_lock_mode":
		return protocol.GetPTTResponse(c.modeLocked), nil
	default:
		log.Printf("unsupported request: %v", req.LongFormat())
		return notImplementedResponse(req.Key()), nil
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
	hamlib.VFOA:    tci.VFOA,
	hamlib.VFOB:    tci.VFOB,
	hamlib.MainVFO: tci.VFOA,
	hamlib.SubVFO:  tci.VFOB,
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

func notImplementedResponse(cmd protocol.CommandKey) protocol.Response {
	return protocol.Response{Command: cmd, Result: "-4"}
}

func dumpCapsResponse(version string) protocol.Response {
	return protocol.Response{
		Command: "dump_caps",
		Data: []string{`Caps dump for model: 1
Model name:	tciadapter
Mfg name:	dl3ney
Backend version:	` + version + `
Backend copyright:	MIT
Backend status:	Stable
Rig type:	Other
PTT type:	None
DCD type:	Rig capable
Port type:	None
Write delay: 0mS, timeout 0mS, 0 retry
Post Write delay: 0mS
Has targetable VFO: Y
Has async data support: N
Announce: 0x0
Max RIT: -9.990kHz/+9.990kHz
Max XIT: -9.990kHz/+9.990kHz
Max IF-SHIFT: -10.0kHz/+10.0kHz
Preamp: 10dB
Attenuator: 10dB 20dB 30dB
AGC levels: 0=OFF 1=SUPERFAST 2=FAST 5=MEDIUM 3=SLOW 6=AUTO 4=USER
CTCSS: 67.0 69.3 71.9 74.4 77.0 79.7 82.5 85.4 88.5 91.5 94.8 97.4 100.0 103.5 107.2 110.9 114.8 118.8 123.0 127.3 131.8 136.5 141.3 146.2 151.4 156.7 159.8 162.2 165.5 167.9 171.3 173.8 177.3 179.9 183.5 186.2 189.9 192.8 196.6 199.5 203.5 206.5 210.7 218.1 225.7 229.1 233.6 241.8 250.3 254.1 Hz, 50 tones
DCS: 17 23 25 26 31 32 36 43 47 50 51 53 54 65 71 72 73 74 114 115 116 122 125 131 132 134 143 145 152 155 156 162 165 172 174 205 212 223 225 226 243 244 245 246 251 252 255 261 263 265 266 271 274 306 311 315 325 331 332 343 346 351 356 364 365 371 411 412 413 423 431 432 445 446 452 454 455 462 464 465 466 503 506 516 523 526 532 546 565 606 612 624 627 631 632 654 662 664 703 712 723 731 732 734 743 754, 106 codes
Get functions: FAGC NB COMP VOX TONE TSQL SBKIN FBKIN ANF NR AIP APF MON MN RF ARO LOCK MUTE VSC REV SQL ABM BC MBC RIT AFC SATMODE SCOPE RESUME TBURST TUNER XIT NB2 CSQL AFLT ANL BC2 DUAL_WATCH DIVERSITY DSQL SCEN TRANSCEIVE SPECTRUM SPECTRUM_HOLD SEND_MORSE SEND_VOICE_MEM 
Set functions: FAGC NB COMP VOX TONE TSQL SBKIN FBKIN ANF NR AIP APF MON MN RF ARO LOCK MUTE VSC REV SQL ABM BC MBC RIT AFC SATMODE SCOPE RESUME TBURST TUNER XIT NB2 CSQL AFLT ANL BC2 DUAL_WATCH DIVERSITY DSQL SCEN TRANSCEIVE SPECTRUM SPECTRUM_HOLD SEND_MORSE SEND_VOICE_MEM 
Extra functions:
	MGEF
		Type: CHECKBUTTON
		Default: 
		Label: Magic ext func
		Tooltip: Magic ext function, as an example
Get level: PREAMP(0..0/0) ATT(0..0/0) VOXDELAY(0..0/0) AF(0..0/0) RF(0..0/0) SQL(0..0/0) IF(0..0/0) APF(0..0/0) NR(0..0/0) PBT_IN(0..0/0) PBT_OUT(0..0/0) CWPITCH(0..0/10) RFPOWER(0..0/0) MICGAIN(0..0/0) KEYSPD(0..0/0) NOTCHF(0..0/0) COMP(0..0/0) AGC(0..0/0) BKINDL(0..0/0) BAL(0..0/0) METER(0..0/0) VOXGAIN(0..0/0) ANTIVOX(0..0/0) SLOPE_LOW(0..0/0) SLOPE_HIGH(0..0/0) BKIN_DLYMS(0..0/0) RAWSTR(0..0/0) SWR(0..0/0) ALC(0..0/0) STRENGTH(0..0/0) RFPOWER_METER(0..0/0) COMP_METER(0..0/0) VD_METER(0..0/0) ID_METER(0..0/0) NOTCHF_RAW(0..0/0) MONITOR_GAIN(0..0/0) NB(0..0/0) RFPOWER_METER_WATTS(0..0/0) SPECTRUM_MODE(0..0/0) SPECTRUM_SPAN(0..0/0) SPECTRUM_EDGE_LOW(0..0/0) SPECTRUM_EDGE_HIGH(0..0/0) SPECTRUM_SPEED(0..2/1) SPECTRUM_REF(-30..10/0.5) SPECTRUM_AVG(0..3/1) SPECTRUM_ATT(0..0/0) TEMP_METER(0..0/0) BAND_SELECT(0..0/0) 
Set level: PREAMP(0..0/0) ATT(0..0/0) VOXDELAY(0..0/0) AF(0..0/0) RF(0..0/0) SQL(0..0/0) IF(0..0/0) APF(0..0/0) NR(0..0/0) PBT_IN(0..0/0) PBT_OUT(0..0/0) CWPITCH(0..0/10) RFPOWER(0..0/0) MICGAIN(0..0/0) KEYSPD(0..0/0) NOTCHF(0..0/0) COMP(0..0/0) AGC(0..0/0) BKINDL(0..0/0) BAL(0..0/0) METER(0..0/0) VOXGAIN(0..0/0) ANTIVOX(0..0/0) SLOPE_LOW(0..0/0) SLOPE_HIGH(0..0/0) BKIN_DLYMS(0..0/0) NOTCHF_RAW(0..0/0) MONITOR_GAIN(0..0/0) NB(0..0/0) RFPOWER_METER_WATTS(0..0/0) SPECTRUM_MODE(0..0/0) SPECTRUM_SPAN(0..0/0) SPECTRUM_EDGE_LOW(0..0/0) SPECTRUM_EDGE_HIGH(0..0/0) SPECTRUM_SPEED(0..2/1) SPECTRUM_REF(-30..10/0.5) SPECTRUM_AVG(0..3/1) SPECTRUM_ATT(0..0/0) TEMP_METER(0..0/0) BAND_SELECT(0..0/0) 
Extra levels:
	MGL
		Type: NUMERIC
		Default: 
		Label: Magic level
		Tooltip: Magic level, as an example
		Range: 0..1/0.001
	MGF
		Type: CHECKBUTTON
		Default: 
		Label: Magic func
		Tooltip: Magic function, as an example
	MGO
		Type: BUTTON
		Default: 
		Label: Magic Op
		Tooltip: Magic Op, as an example
	MGC
		Type: COMBO
		Default: VALUE1
		Label: Magic combo
		Tooltip: Magic combo, as an example
		Values: 0="VALUE1" 1="VALUE2" 2="NONE"
Get parameters: ANN(0..0/0) APO(0..0/0) BACKLIGHT(0..0/0) BEEP(0..0/0) TIME(0..0/0) BAT(0..0/0) KEYLIGHT(0..0/0) SCREENSAVER(0..0/0) 
Set parameters: ANN(0..0/0) APO(0..0/0) BACKLIGHT(0..0/0) BEEP(0..0/0) TIME(0..0/0) KEYLIGHT(0..0/0) SCREENSAVER(0..0/0) 
Extra parameters:
	MGP
		Type: NUMERIC
		Default: 
		Label: Magic parm
		Tooltip: Magic parameter, as an example
		Range: 0..1/0.001
Mode list: AM CW USB LSB RTTY FM WFM CWR RTTYR 
VFO list: VFOA VFOB VFOC SubA SubB MainA MainB Sub Main MEM currVFO 
VFO Ops: CPY XCHG FROM_VFO TO_VFO MCL UP DOWN BAND_UP BAND_DOWN LEFT RIGHT TUNE TOGGLE 
Scan Ops: MEM SLCT PRIO PROG DELTA VFO PLT STOP 
Number of banks:	0
Memory name desc size:	0
Memories:
	0..18:   	MEM
		Mem caps: BANK ANT FREQ MODE WIDTH TXFREQ TXMODE TXWIDTH SPLIT RPTRSHIFT RPTROFS TS RIT XIT FUNC LEVEL TONE CTCSS DCSCODE DCSSQL SCANGRP FLAG NAME EXTLVL 
	19..19:   	CALL
		Mem caps: 
	20..21:   	EDGE
		Mem caps: 
TX ranges #1 for Dummy#1:
	150000 Hz - 1500000000 Hz
		VFO list: VFOA VFOB VFOC SubA SubB MainA MainB Sub Main MEM currVFO 
		Mode list: AM CW USB LSB RTTY FM WFM CWR RTTYR 
		Antenna list: ANT1 ANT2 ANT3 ANT4 
		Low power: 5 W, High power: 100 W
RX ranges #1 for Dummy#1:
	150000 Hz - 1500000000 Hz
		VFO list: VFOA VFOB VFOC SubA SubB MainA MainB Sub Main MEM currVFO 
		Mode list: AM CW USB LSB RTTY FM WFM CWR RTTYR 
		Antenna list: ANT1 ANT2 ANT3 ANT4 
TX ranges #2 for Dummy#2:
RX ranges #2 for Dummy#2:
	150000 Hz - 1500000000 Hz
		VFO list: VFOA VFOB VFOC SubA SubB MainA MainB Sub Main MEM currVFO 
		Mode list: AM CW USB LSB RTTY FM WFM CWR RTTYR 
		Antenna list: ANT1 ANT2 ANT3 ANT4 
TX ranges #3 for TBD:
RX ranges #3 for TBD:
TX ranges #4 for TBD:
RX ranges #4 for TBD:
TX ranges #5 for TBD:
RX ranges #5 for TBD:
TX ranges #1 status for Dummy#1:	OK (0)
RX ranges #1 status for Dummy#1:	OK (0)
TX ranges #2 status for Dummy#2:	OK (0)
RX ranges #2 status for Dummy#2:	OK (0)
TX ranges #3 status for TBD:	OK (0)
RX ranges #3 status for TBD:	OK (0)
TX ranges #4 status for TBD:	OK (0)
RX ranges #4 status for TBD:	OK (0)
TX ranges #5 status for TBD:	OK (0)
RX ranges #5 status for TBD:	OK (0)
Tuning steps:
	1.0 Hz:   	AM CW USB LSB RTTY FM WFM CWR RTTYR 
	ANY:   	AM CW USB LSB RTTY FM WFM CWR RTTYR 
Tuning steps status:	OK (0)
Filters:
	2.4000 kHz:   	USB LSB 
	1.8000 kHz:   	USB LSB 
	3.0000 kHz:   	USB LSB 
	ANY:   	USB LSB 
	500.0 Hz:   	CW 
	2.4000 kHz:   	CW 
	50.0 Hz:   	CW 
	ANY:   	CW 
	300.0 Hz:   	RTTY 
	2.4000 kHz:   	RTTY 
	50.0 Hz:   	RTTY 
	ANY:   	RTTY 
	8.0000 kHz:   	AM 
	2.4000 kHz:   	AM 
	10.0000 kHz:   	AM 
	15.0000 kHz:   	FM 
	8.0000 kHz:   	FM 
	230.0000 kHz:   	WFM 
Bandwidths:
	AM	Normal: 8.0000 kHz,	Narrow: 2.4000 kHz,	Wide: 10.0000 kHz
	CW	Normal: 500.0 Hz,	Narrow: 50.0 Hz,	Wide: 2.4000 kHz
	USB	Normal: 2.4000 kHz,	Narrow: 1.8000 kHz,	Wide: 3.0000 kHz
	LSB	Normal: 2.4000 kHz,	Narrow: 1.8000 kHz,	Wide: 3.0000 kHz
	RTTY	Normal: 300.0 Hz,	Narrow: 50.0 Hz,	Wide: 2.4000 kHz
	FM	Normal: 15.0000 kHz,	Narrow: 8.0000 kHz,	Wide: 0.0 Hz
	WFM	Normal: 230.0000 kHz,	Narrow: 0.0 Hz,	Wide: 0.0 Hz
	CWR	Normal: 500.0 Hz,	Narrow: 0.0 Hz,	Wide: 0.0 Hz
	RTTYR	Normal: 300.0 Hz,	Narrow: 0.0 Hz,	Wide: 0.0 Hz
	AMS	Normal: 0.0 Hz,	Narrow: 0.0 Hz,	Wide: 0.0 Hz
	PKTLSB	Normal: 0.0 Hz,	Narrow: 0.0 Hz,	Wide: 0.0 Hz
	PKTUSB	Normal: 0.0 Hz,	Narrow: 0.0 Hz,	Wide: 0.0 Hz
	FM-D	Normal: 0.0 Hz,	Narrow: 0.0 Hz,	Wide: 0.0 Hz
	ECSSUSB	Normal: 0.0 Hz,	Narrow: 0.0 Hz,	Wide: 0.0 Hz
	ECSSLSB	Normal: 0.0 Hz,	Narrow: 0.0 Hz,	Wide: 0.0 Hz
	FAX	Normal: 0.0 Hz,	Narrow: 0.0 Hz,	Wide: 0.0 Hz
	SAM	Normal: 0.0 Hz,	Narrow: 0.0 Hz,	Wide: 0.0 Hz
	SAL	Normal: 0.0 Hz,	Narrow: 0.0 Hz,	Wide: 0.0 Hz
	SAH	Normal: 0.0 Hz,	Narrow: 0.0 Hz,	Wide: 0.0 Hz
	DSB	Normal: 0.0 Hz,	Narrow: 0.0 Hz,	Wide: 0.0 Hz
		Normal: 0.0 Hz,	Narrow: 0.0 Hz,	Wide: 0.0 Hz
	FMN	Normal: 0.0 Hz,	Narrow: 0.0 Hz,	Wide: 0.0 Hz
	AM-D	Normal: 0.0 Hz,	Narrow: 0.0 Hz,	Wide: 0.0 Hz
	P25	Normal: 0.0 Hz,	Narrow: 0.0 Hz,	Wide: 0.0 Hz
	D-STAR	Normal: 0.0 Hz,	Narrow: 0.0 Hz,	Wide: 0.0 Hz
	DPMR	Normal: 0.0 Hz,	Narrow: 0.0 Hz,	Wide: 0.0 Hz
	NXDN-VN	Normal: 0.0 Hz,	Narrow: 0.0 Hz,	Wide: 0.0 Hz
	NXDN-N	Normal: 0.0 Hz,	Narrow: 0.0 Hz,	Wide: 0.0 Hz
	DCR	Normal: 0.0 Hz,	Narrow: 0.0 Hz,	Wide: 0.0 Hz
	AMN	Normal: 0.0 Hz,	Narrow: 0.0 Hz,	Wide: 0.0 Hz
	PSK	Normal: 0.0 Hz,	Narrow: 0.0 Hz,	Wide: 0.0 Hz
Spectrum scopes: 0="Main" 1="Sub"
Spectrum modes: 1=CENTER 2=FIXED 3=CENTER_SCROLL 4=FIXED_SCROLL 
Spectrum spans: 5000 10000 20000 50000 100000 200000 500000 1000000 2000000 5000000 
Spectrum averaging modes: 0="OFF" 1="2" 2="3" 3="4" 
Spectrum attenuator: 10dB 20dB 30dB
Has priv data:	N
Has Init:	N
Has Cleanup:	N
Has Open:	Y
Has Close:	Y
Can set Conf:	N
Can get Conf:	N
Can set Frequency:	Y
Can get Frequency:	Y
Can set Mode:	Y
Can get Mode:	Y
Can set VFO:	Y
Can get VFO:	Y
Can set PTT:	Y
Can get PTT:	Y
Can get DCD:	N
Can set Repeater Duplex:	N
Can get Repeater Duplex:	N
Can set Repeater Offset:	N
Can get Repeater Offset:	N
Can set Split Freq:	Y
Can get Split Freq:	Y
Can set Split Mode:	Y
Can get Split Mode:	Y
Can set Split VFO:	Y
Can get Split VFO:	Y
Can set Tuning Step:	N
Can get Tuning Step:	N
Can set RIT:	N
Can get RIT:	N
Can set XIT:	N
Can get XIT:	N
Can set CTCSS:	N
Can get CTCSS:	N
Can set DCS:	N
Can get DCS:	N
Can set CTCSS Squelch:	N
Can get CTCSS Squelch:	N
Can set DCS Squelch:	N
Can get DCS Squelch:	N
Can set Power Stat:	N
Can get Power Stat:	N
Can Reset:	N
Can get Ant:	N
Can set Ant:	N
Can set Transceive:	Y
Can get Transceive:	Y
Can set Func:	N
Can get Func:	N
Can set Level:	N
Can get Level:	N
Can set Param:	N
Can get Param:	N
Can send DTMF:	N
Can recv DTMF:	N
Can send Morse:	Y
Can send Voice:	N
Can decode Events:	N
Can set Bank:	N
Can set Mem:	N
Can get Mem:	N
Can set Channel:	N
Can get Channel:	N
Can ctl Mem/VFO:	N
Can Scan:	N
Can get Info:	N
Can get power2mW:	N
Can get mW2power:	N

Overall backend warnings: 0
`},
		Keys:   []string{""},
		Result: "0",
	}
}

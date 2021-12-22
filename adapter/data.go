package adapter

import (
	"sync"

	tci "github.com/ftl/tci/client"
)

func newTRXData(trx int) *TRXData {
	return &TRXData{
		trx:    trx,
		vfos:   make(map[tci.VFO]vfoData),
		txSync: new(sync.WaitGroup),
	}
}

type vfoData struct {
	frequency int
}

type TRXData struct {
	trx          int
	currentVFO   tci.VFO
	vfos         map[tci.VFO]vfoData
	mode         tci.Mode
	rxFilterMin  int
	rxFilterMax  int
	splitEnabled bool
	transmitting bool
	txSync       *sync.WaitGroup
}

func (t *TRXData) CurrentVFO() tci.VFO {
	return t.currentVFO
}

func (t *TRXData) SetVFOFrequency(trx int, vfo tci.VFO, frequency int) {
	if trx != t.trx {
		return
	}
	data := t.vfos[vfo]
	data.frequency = frequency
	t.vfos[vfo] = data
}

func (t *TRXData) VFOFrequency(vfo tci.VFO) int {
	data := t.vfos[vfo]
	return data.frequency
}

func (t *TRXData) CurrentVFOFrequency() int {
	data := t.vfos[t.currentVFO]
	return data.frequency
}

func (t *TRXData) SetMode(trx int, mode tci.Mode) {
	if trx != t.trx {
		return
	}
	t.mode = mode
}

func (t *TRXData) Mode() tci.Mode {
	return t.mode
}

func (t *TRXData) SetRXFilterBand(trx int, min, max int) {
	if trx != t.trx {
		return
	}
	t.rxFilterMin = min
	t.rxFilterMax = max
}

func (t *TRXData) RXFilterBand() (int, int) {
	return t.rxFilterMin, t.rxFilterMax
}

func (t *TRXData) SetSplitEnable(trx int, enabled bool) {
	if trx != t.trx {
		return
	}
	t.splitEnabled = enabled
}

func (t *TRXData) SplitEnable() bool {
	return t.splitEnabled
}

func (t *TRXData) SetTX(trx int, enabled bool) {
	if trx != t.trx {
		return
	}
	if t.transmitting == enabled {
		return
	}
	t.transmitting = enabled
	if enabled {
		t.txSync.Add(1)
	} else {
		t.txSync.Done()
	}
}

func (t *TRXData) TX() bool {
	return t.transmitting
}

func (t *TRXData) WaitForTransmissionEnd() {
	t.txSync.Wait()
}

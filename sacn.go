package wled

import (
	"context"
	"fmt"

	"github.com/Hundemeier/go-sacn/sacn"
)

const numUniverses = 3

// sacnState holds the go-sacn transmitter and per-universe channels.
type sacnState struct {
	trans sacn.Transmitter
	ch    [numUniverses]chan<- []byte
}

// initSACN creates a transmitter, activates universes 1–3 with unicast
// to the WLED device, and returns the state for use in DoCommand.
func initSACN(wledIP string) (*sacnState, error) {
	cid := [16]byte{0x76, 0x69, 0x61, 0x6d} // "viam" prefix
	trans, err := sacn.NewTransmitter("", cid, "viam-wled-module")
	if err != nil {
		return nil, fmt.Errorf("create sACN transmitter: %w", err)
	}

	var channels [numUniverses]chan<- []byte
	for i := 0; i < numUniverses; i++ {
		universe := uint16(i + 1)
		ch, err := trans.Activate(universe)
		if err != nil {
			return nil, fmt.Errorf("activate universe %d: %w", universe, err)
		}
		trans.SetDestinations(universe, []string{wledIP})
		channels[i] = ch
	}

	return &sacnState{trans: trans, ch: channels}, nil
}

// sendFrame parses ring pixel arrays from the command and sends each ring's
// RGB data to the corresponding sACN universe channel.
func (s *wledWled) sendFrame(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	if s.sacn == nil {
		sacnState, err := initSACN(s.cfg.WledIP)
		if err != nil {
			return nil, fmt.Errorf("lazy sACN init: %w", err)
		}
		s.sacn = sacnState
		s.logger.Infow("sACN transmitter initialized on first frame")
	}

	ringsVal, ok := cmd["rings"]
	if !ok {
		return nil, fmt.Errorf("frame command missing \"rings\" key")
	}
	rings, ok := ringsVal.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("\"rings\" must be a map, got %T", ringsVal)
	}

	for i := 0; i < numUniverses; i++ {
		key := fmt.Sprintf("%d", i)
		ringVal, ok := rings[key]
		if !ok {
			continue
		}

		pixelSlice, ok := ringVal.([]interface{})
		if !ok {
			return nil, fmt.Errorf("ring %q must be an array, got %T", key, ringVal)
		}

		dmx := make([]byte, len(pixelSlice))
		for j, v := range pixelSlice {
			// Values arrive as float64 from JSON deserialization
			f, ok := v.(float64)
			if !ok {
				return nil, fmt.Errorf("ring %q pixel %d: expected number, got %T", key, j, v)
			}
			dmx[j] = byte(int(f) & 0xFF)
		}

		s.sacn.ch[i] <- dmx
	}

	return map[string]interface{}{"status": "ok"}, nil
}

// stopSACN tears down the sACN transmitter so it stops overriding HTTP effects.
// Safe to call when sACN is already nil.
func (s *wledWled) stopSACN() {
	if s.sacn == nil {
		return
	}
	s.sacn.close()
	s.sacn = nil
	s.logger.Infow("sACN transmitter stopped, HTTP effects take priority")
}

// close shuts down all universe channels and stops the transmitter.
func (st *sacnState) close() {
	for i := range st.ch {
		if st.ch[i] != nil {
			close(st.ch[i])
			st.ch[i] = nil
		}
	}
}

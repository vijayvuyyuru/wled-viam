package wled

import (
	"context"
	"fmt"
	"time"

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

// sendFrame lazily initializes the sACN transmitter on first use (mutex-protected),
// then sends per-pixel RGB data to the corresponding universe channels.
func (s *wledWled) sendFrame(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	s.sacnMu.Lock()
	if s.sacn == nil {
		st, err := initSACN(s.cfg.WledIP)
		if err != nil {
			s.sacnMu.Unlock()
			return nil, fmt.Errorf("sACN init: %w", err)
		}
		s.sacn = st
		s.logger.Infow("sACN transmitter initialized on first frame")
	}
	s.sacnMu.Unlock()

	frameStart := time.Now()

	ringsVal, ok := cmd["rings"]
	if !ok {
		return nil, fmt.Errorf("frame command missing \"rings\" key")
	}
	rings, ok := ringsVal.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("\"rings\" must be a map, got %T", ringsVal)
	}

	parseStart := time.Now()
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
			f, ok := v.(float64)
			if !ok {
				return nil, fmt.Errorf("ring %q pixel %d: expected number, got %T", key, j, v)
			}
			dmx[j] = byte(int(f) & 0xFF)
		}

		s.sacn.ch[i] <- dmx
	}
	parseDur := time.Since(parseStart)
	totalDur := time.Since(frameStart)

	s.logger.Debugw("FRAME_TIMING,wled_sacn", "parse_ms", parseDur.Milliseconds(), "total_ms", totalDur.Milliseconds())

	return map[string]interface{}{"status": "ok"}, nil
}

// teardownSACN destroys the sACN transmitter so no keep-alive packets are sent.
// This allows WLED to fall back to HTTP effect mode. Safe to call when nil.
func (s *wledWled) teardownSACN() {
	s.sacnMu.Lock()
	defer s.sacnMu.Unlock()
	if s.sacn == nil {
		return
	}
	s.sacn.close()
	s.sacn = nil
	s.logger.Infow("sACN transmitter torn down")
}

// close shuts down all universe channels. Channels are closed sequentially
// with a brief pause to let go-sacn's internal goroutines drain and avoid
// concurrent map access panics in the library.
func (st *sacnState) close() {
	for i := range st.ch {
		if st.ch[i] != nil {
			close(st.ch[i])
			st.ch[i] = nil
			time.Sleep(50 * time.Millisecond)
		}
	}
}

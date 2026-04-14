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

// enterSACNMode sets lor=1 on WLED so it accepts sACN realtime data.
// Only sends the HTTP request if not already in sACN mode.
func (s *wledWled) enterSACNMode(ctx context.Context) {
	s.sacnMu.Lock()
	alreadyActive := s.sacnActive
	s.sacnActive = true
	s.sacnMu.Unlock()

	if !alreadyActive {
		if _, err := s.PostState(ctx, map[string]interface{}{"lor": 1}); err != nil {
			s.logger.Warnw("failed to set lor=1", "error", err)
		}
		s.logger.Infow("entered sACN mode (lor=1)")
	}
}

// exitSACNMode sends black frames on all universes and sets lor=0 so WLED
// returns to HTTP effect mode. Only acts if currently in sACN mode.
func (s *wledWled) exitSACNMode(ctx context.Context) {
	s.sacnMu.Lock()
	if !s.sacnActive {
		s.sacnMu.Unlock()
		return
	}
	// Send black frames to clear any sACN pixel data
	if s.sacn != nil {
		black := make([]byte, 432) // 144 pixels × 3 channels
		for i := 0; i < numUniverses; i++ {
			s.sacn.ch[i] <- black
		}
	}
	s.sacnActive = false
	s.sacnMu.Unlock()

	// Set lor=0 so WLED immediately accepts HTTP commands
	if _, err := s.PostState(ctx, map[string]interface{}{"lor": 0}); err != nil {
		s.logger.Warnw("failed to set lor=0", "error", err)
	}
	s.logger.Infow("exited sACN mode (lor=0)")
}

// sendFrame parses ring pixel arrays from the command and sends each ring's
// RGB data to the corresponding sACN universe channel.
func (s *wledWled) sendFrame(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	if s.sacn == nil {
		return nil, fmt.Errorf("sACN transmitter not initialized")
	}

	// Ensure WLED is in realtime mode
	s.enterSACNMode(ctx)

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

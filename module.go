package wled

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	generic "go.viam.com/rdk/services/generic"
)

var Wled = resource.NewModel("vijayvuyyuru", "wled", "wled")

func init() {
	resource.RegisterService(generic.API, Wled,
		resource.Registration[resource.Resource, *Config]{
			Constructor: newWledWled,
		},
	)
}

type Config struct {
	WledIP      string          `json:"wled_ip"`
	WledPort    int             `json:"wled_port,omitempty"`
	Brightness  float64         `json:"brightness,omitempty"`
	HTTPTimeout float64         `json:"http_timeout,omitempty"`
	Segments    []SegmentConfig `json:"segments"`
}

// Validate ensures all parts of the config are valid and important fields exist.
// Returns three values:
//  1. Required dependencies: other resources that must exist for this resource to work.
//  2. Optional dependencies: other resources that may exist but are not required.
//  3. An error if any Config fields are missing or invalid.
//
// The `path` parameter indicates
// where this resource appears in the machine's JSON configuration
// (for example, "components.0"). You can use it in error messages
// to indicate which resource has a problem.
func (cfg *Config) Validate(path string) ([]string, []string, error) {
	if cfg.WledIP == "" {
		return nil, nil, fmt.Errorf("%s: wled_ip is required", path)
	}
	return nil, nil, nil
}

type wledWled struct {
	resource.AlwaysRebuild

	name resource.Name

	logger logging.Logger
	cfg    *Config

	wledBase   string
	httpClient *http.Client
	sacn       *sacnState
	sacnMu     sync.Mutex

	cancelCtx  context.Context
	cancelFunc func()
}

func newWledWled(ctx context.Context, deps resource.Dependencies, rawConf resource.Config, logger logging.Logger) (resource.Resource, error) {
	conf, err := resource.NativeConfig[*Config](rawConf)
	if err != nil {
		return nil, err
	}

	return NewWled(ctx, deps, rawConf.ResourceName(), conf, logger)

}

func NewWled(ctx context.Context, deps resource.Dependencies, name resource.Name, conf *Config, logger logging.Logger) (resource.Resource, error) {
	cancelCtx, cancelFunc := context.WithCancel(context.Background())

	port := conf.WledPort
	if port == 0 {
		port = 80
	}

	httpTimeout := conf.HTTPTimeout
	if httpTimeout <= 0 {
		httpTimeout = 10
	}

	s := &wledWled{
		name:       name,
		logger:     logger,
		cfg:        conf,
		wledBase:   fmt.Sprintf("http://%s:%d", conf.WledIP, port),
		httpClient: &http.Client{Timeout: time.Duration(httpTimeout * float64(time.Second))},
		cancelCtx:  cancelCtx,
		cancelFunc: cancelFunc,
	}

	// Apply initial brightness if configured
	if conf.Brightness > 0 {
		bri := int(conf.Brightness * 255)
		if _, err := s.PostState(ctx, map[string]interface{}{"bri": bri}); err != nil {
			logger.Warnw("failed to set initial brightness", "error", err)
		}
	}

	// sACN transmitter is lazily initialized on first frame command
	// and torn down when switching to HTTP. No keep-alive interference.

	logger.Infow("WLED module initialized", "base_url", s.wledBase)
	return s, nil
}

func (s *wledWled) Name() resource.Name {
	return s.name
}

func (s *wledWled) DoCommand(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	// Shape A — global commands with a "command" key
	if cmdVal, ok := cmd["command"]; ok {
		cmdStr, isStr := cmdVal.(string)
		if !isStr {
			return nil, fmt.Errorf("\"command\" value must be a string, got %T", cmdVal)
		}

		switch cmdStr {
		case "off":
			return s.PostState(ctx, map[string]interface{}{"on": false})
		case "on":
			return s.PostState(ctx, map[string]interface{}{"on": true})
		case "status":
			return s.GetState(ctx)
		case "frame":
			// Shape C — per-pixel frame via sACN
			return s.sendFrame(ctx, cmd)
		default:
			return nil, fmt.Errorf("unknown command: %q", cmdStr)
		}
	}

	// Shape B — passthrough: forward the entire map to WLED
	s.teardownSACN()
	return s.PostState(ctx, cmd)
}

func (s *wledWled) Status(ctx context.Context) (map[string]interface{}, error) {
	return s.GetState(ctx)
}

func (s *wledWled) Close(context.Context) error {
	s.sacnMu.Lock()
	if s.sacn != nil {
		s.sacn.close()
		s.sacn = nil
	}
	s.sacnMu.Unlock()
	s.cancelFunc()
	return nil
}

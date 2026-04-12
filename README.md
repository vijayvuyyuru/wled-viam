# Module wled

A Viam module that controls WLED-compatible LED devices via HTTP and E1.31 sACN. It provides a single generic service that acts as the sole transport owner for all LED communication — no other module or visualizer should call the WLED device directly.

## Models

This module provides the following model(s):

- [`vijayvuyyuru:wled:wled`](vijayvuyyuru_wled_wled.md) - Controls a WLED device over HTTP JSON API and E1.31 sACN

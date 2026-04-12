# Model vijayvuyyuru:wled:wled

Controls a WLED-compatible ESP32 LED controller over the network. Supports two communication paths:

- **HTTP JSON API** — for state control, built-in WLED effects, on/off, and brightness
- **E1.31 sACN (UDP)** — for real-time per-pixel frame data across up to 3 universes

## Configuration

The following attribute template can be used to configure this model:

```json
{
  "wled_ip": <string>,
  "wled_port": <int>,
  "brightness": <float>,
  "http_timeout": <float>
}
```

### Attributes

The following attributes are available for this model:

| Name           | Type   | Inclusion | Description                                         | Default |
|----------------|--------|-----------|-----------------------------------------------------|---------|
| `wled_ip`      | string | Required  | IP address or mDNS hostname of the WLED device      |         |
| `wled_port`    | int    | Optional  | HTTP port of the WLED device                        | 80      |
| `brightness`   | float  | Optional  | Initial brightness (0.0–1.0), applied on startup    |         |
| `http_timeout` | float  | Optional  | HTTP request timeout in seconds                     | 10      |

### Example Configuration

```json
{
  "wled_ip": "wled-7f091c.local",
  "brightness": 0.6,
  "http_timeout": 15
}
```

## DoCommand

The model accepts three command shapes, distinguished by the `"command"` key.

### Shape A — Global commands

Simple on/off/status commands with a top-level `"command"` key.

#### off

Turns off the WLED device.

```json
{"command": "off"}
```

#### on

Turns on the WLED device.

```json
{"command": "on"}
```

#### status

Returns the full WLED state from the device. The ESP32 is the source of truth.

```json
{"command": "status"}
```

Returns the raw WLED `/json/state` response including `on`, `bri`, and `seg` array.

### Shape B — Passthrough

Any map **without** a `"command"` key is forwarded verbatim to `POST /json/state` on the WLED device. This allows sending any valid WLED JSON directly.

#### Set a solid color on segment 0

```json
{"on": true, "bri": 255, "seg": [{"id": 0, "fx": 0, "col": [[255, 0, 0]]}]}
```

#### Set effects on multiple segments

```json
{
  "on": true,
  "seg": [
    {"id": 0, "fx": 76, "sx": 128, "ix": 255, "col": [[255, 64, 0]]},
    {"id": 1, "fx": 2, "sx": 150, "col": [[0, 100, 255]]},
    {"id": 2, "fx": 9, "sx": 128}
  ]
}
```

Common WLED segment fields:
- `fx` — effect ID (0=Solid, 2=Breathe, 9=Rainbow, 76=Meteor)
- `sx` — effect speed (0–255)
- `ix` — effect intensity (0–255)
- `col` — colors as `[[R, G, B], ...]` (up to 3 colors)
- `pal` — palette ID (e.g. 11=Rainbow)

### Shape C — Frame (per-pixel sACN)

Sends per-pixel RGB data to the WLED device via E1.31 sACN. Each ring maps to a sACN universe (ring 0 → universe 1, etc.).

```json
{
  "command": "frame",
  "rings": {
    "0": [255, 64, 0, 255, 60, 0, ...],
    "1": [0, 100, 255, ...],
    "2": [80, 0, 200, ...]
  }
}
```

Each ring value is a flat array of RGB bytes (144 pixels × 3 channels = 432 values per ring).

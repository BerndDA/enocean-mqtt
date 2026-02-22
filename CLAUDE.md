# CLAUDE.md — EnOcean-MQTT Gateway

This file provides guidance for AI assistants working on this codebase.

## Project Overview

EnOcean-MQTT Gateway is a Go application that bridges [EnOcean](https://www.enocean.com/) wireless devices with MQTT for home automation. It implements the ESP3 (EnOcean Serial Protocol 3) over TCP and exposes device events and commands through structured MQTT topics.

**Data flow:**
```
EnOcean USB Adapter → serial_mux.py (TCP :2000) → enocean-mqtt (Go) → MQTT Broker
                                    ↑                        ↑
                              (bidirectional)          (bidirectional)
```

## Repository Structure

```
enocean-mqtt/
├── main.go                    # Entry point; wires EnOcean ↔ MQTT, handles MQTT commands
├── config/
│   └── config.go              # YAML config loading, device registry, helper methods
├── enocean/
│   ├── client.go              # ESP3 TCP client, CRC8, packet parsing, send methods
│   └── profiles.go            # Telegram decoders: RPS (F6), 1BS (D5), 4BS (A5), VLD (D2)
├── mqtt/
│   └── client.go              # Paho MQTT wrapper, topic routing, LWT, JSON message types
├── tools/
│   ├── serial_mux.py          # Python serial-to-TCP multiplexer (port 2000 + 2001 capture)
│   ├── enodump.py             # Python PCAP capture tool for Wireshark
│   ├── requirements.txt       # Python deps: pyserial>=3.5
│   ├── Dockerfile             # Container for Python tools
│   └── wireshark/
│       └── enocean.lua        # Full ESP3 Wireshark dissector (Lua, ~605 lines)
├── Dockerfile                 # Multi-stage Go build
├── docker-compose.yml         # Orchestrates serial-mux + enocean-mqtt containers
├── config.yaml.example        # Annotated configuration template
├── go.mod                     # Module: enocean-mqtt, Go 1.21
├── go.sum
└── README.md
```

## Build & Run

### Go binary

```bash
go mod tidy
go build -o enocean-mqtt
./enocean-mqtt -config config.yaml
```

### Docker Compose (recommended for production)

```bash
cp config.yaml.example config.yaml
# Edit config.yaml: set enocean.host to "serial-mux" when using compose
docker compose up -d
```

### Python tools

```bash
cd tools
pip install -r requirements.txt
python serial_mux.py /dev/ttyUSB0          # Serial multiplexer
python enodump.py --host 192.168.1.100 --port 2001 -b | wireshark -k -i -
```

## Configuration

Config is YAML at `config.yaml` (path overridable via `-config` flag). Falls back to defaults if file is missing.

```yaml
enocean:
  host: "192.168.1.100"   # Host running serial_mux.py; use "serial-mux" in Docker Compose
  port: 2000

mqtt:
  host: "192.168.1.100"
  port: 1883
  client_id: "enocean-mqtt-gateway"
  topic: "enocean"        # Base topic prefix

devices:
  "01:90:B7:27":           # Device ID: uppercase hex with colon separators
    name: "kitchen_light"  # Friendly name used in MQTT topics
    type: "actuator"       # actuator | switch | dimmer | light
    sender_id: "FF:A0:1D:87"  # Gateway sender ID used during teach-in (required for commands)
```

Device ID format is always `XX:XX:XX:XX` (uppercase, colon-separated). Names become MQTT topic segments and must be URL-safe.

## MQTT Topics Reference

### Published by the gateway

| Topic | Retained | Description |
|-------|----------|-------------|
| `enocean/bridge/status` | yes | `online` / `offline` (LWT on disconnect) |
| `enocean/gateway/info` | yes | JSON gateway version info |
| `enocean/telegram` | no | Every received EnOcean telegram as JSON |
| `enocean/device/{name}/switch/{channel}` | no | RPS rocker events (press/release) |
| `enocean/device/{id}` | no | Telegram for devices without a friendly name |
| `enocean/device/{name}/measurement` | no | D2-01 energy/power measurement |

### Command topics (subscribed by the gateway)

| Topic | Payload | Description |
|-------|---------|-------------|
| `enocean/cmd/{device_name}` | JSON `ActuatorCommand` | Control device by friendly name |
| `enocean/cmd/eltako` | JSON `ActuatorCommand` | Send RPS F6 rocker switch command |
| `enocean/cmd/actuator` | JSON `ActuatorCommand` | Send D2-01 VLD actuator command |
| `enocean/cmd/reset` | any | Reset the EnOcean USB gateway |
| `enocean/cmd/{device_id}` | hex string | Raw hex bytes to send (fallback) |

### ActuatorCommand JSON schema

```json
{
  "name":      "kitchen_light",  // optional — device friendly name
  "sender_id": "FF:A0:1D:87",   // gateway sender ID (from config if omitted)
  "device_id": "01:90:B7:27",   // target device ID
  "channel":   0,               // I/O channel (byte, 0-based)
  "state":     "on",            // "on" | "off" | "dim"
  "value":     75               // 0–100 for "dim" state
}
```

## Key Code Concepts

### ESP3 Protocol (`enocean/client.go`)

- Packets begin with sync byte `0x55`, followed by header (data length, optional length, type), header CRC8, data, optional data, data CRC8.
- CRC8 uses a 256-entry lookup table (`crc8Table`).
- Synchronous command/response uses a `responseChannel chan []byte` protected by a mutex; the read loop routes response packets there and calls callbacks for radio and event packets.
- `Initialize()` runs `CO_RD_IDBASE`, disables repeater (`CO_WR_REPEATER`), and enables maturity waiting (`CO_WR_WAIT_MATURITY`).

### Telegram Types (`enocean/profiles.go`)

| RORG byte | Name | Usage |
|-----------|------|-------|
| `0xF6` | RPS | Rocker switch; controls Eltako actuators |
| `0xD5` | 1BS | Contact sensors |
| `0xA5` | 4BS | Temperature/humidity sensors |
| `0xD2` | VLD | Smart actuators/dimmers (D2-01 profile) |
| `0xD4` | UTE | Universal teach-in (Wireshark only) |

- `ParseRPSTelegram(data, status, senderID)` → `RPSData`
- `ParseVLDTelegram(vldBytes, senderID)` → `VLDData` with `.Parsed` as `*D2_01_MeasurementResponse` or similar

### Echo Filtering

The gateway tracks all configured `sender_id` values. When a telegram arrives whose sender ID matches one of those, it is dropped to avoid re-processing the gateway's own transmissions. See `config.IsOwnSenderID()` and `config.GetSenderIDs()`.

### MQTT Client (`mqtt/client.go`)

- Uses [paho.mqtt.golang](https://github.com/eclipse/paho.mqtt.golang) v1.4.3.
- Topic-unsafe characters (colons) in device IDs are replaced with underscores for topic segments.
- LWT is set at connect time: topic `{base}/bridge/status`, payload `offline`, retained.
- Auto-reconnects every 5 seconds on disconnect.
- All message structs are JSON-serialised; key types are `TelegramMessage`, `RockerSwitchMessage`, `MeasurementMessage`, `EventMessage`, `GatewayInfoMessage`.

## Go Code Conventions

- **Module name**: `enocean-mqtt` (import paths: `enocean-mqtt/config`, `enocean-mqtt/enocean`, `enocean-mqtt/mqtt`).
- **Error handling**: always wrap with `fmt.Errorf("context: %w", err)`; fatal errors in `main.go` use `log.Fatalf`.
- **Logging**: `log.SetFlags(log.LstdFlags | log.Lshortfile)` is set in `main()`. Use `log.Printf` / `log.Println` throughout; no third-party logger.
- **Concurrency**: goroutines for the read loop; `sync.Mutex` for shared state (response channel, sender ID list). Use `go func() { ... }()` for non-blocking callbacks.
- **Hex IDs**: always uppercase with colons (`strings.ToUpper` + `strings.Join(... ":")`).
- **Callback registration**: `Set*Handler(func(...))` pattern — handlers must be set *before* calling `Connect()`.
- **Graceful shutdown**: `defer client.Close()` in `main()`, SIGINT/SIGTERM via `signal.Notify`.

## Testing & Debugging

There is currently **no automated test suite**. When making changes:

1. Build with `go build ./...` to catch compile errors.
2. Use `go vet ./...` for static analysis.
3. For protocol debugging, use `enodump.py` + Wireshark with `tools/wireshark/enocean.lua`.
4. Check logs — the gateway logs every telegram, command, and error at the INFO level to stdout.

## Common Tasks

### Add a new device
1. Find the device's 4-byte EnOcean ID (observe via `enocean/telegram` topic or Wireshark).
2. Add entry under `devices:` in `config.yaml` with `name`, `type`, and `sender_id` (if controllable).
3. No code changes required for standard device types (`switch`, `actuator`, `dimmer`).

### Add support for a new telegram profile
1. Add parser function in `enocean/profiles.go` returning a typed struct.
2. Register a `case` in the main telegram handler in `main.go` (around line 76–112).
3. Add a `Publish*` method in `mqtt/client.go` if a new topic is needed.
4. Update `tools/wireshark/enocean.lua` for dissection support (optional but recommended).

### Change the MQTT topic structure
- Base topic prefix is `cfg.MQTT.Topic` (default `"enocean"`).
- All topic construction is in `mqtt/client.go`; search for string literals like `"/device/"`, `"/cmd/"`, `"/bridge/"`.

### Run with Docker Compose
```bash
# Set enocean.host: "serial-mux" in config.yaml
docker compose up --build
```
The `serial-mux` service exposes TCP ports 2000 (data) and 2001 (capture). The `enocean-mqtt` service depends on it.

## Dependencies

| Package | Version | Purpose |
|---------|---------|---------|
| `github.com/eclipse/paho.mqtt.golang` | v1.4.3 | MQTT client |
| `gopkg.in/yaml.v3` | v3.0.1 | Config parsing |
| `github.com/gorilla/websocket` | v1.5.0 | Indirect (paho dep) |
| `golang.org/x/net` | v0.8.0 | Indirect |
| `golang.org/x/sync` | v0.1.0 | Indirect |
| pyserial | ≥3.5 | Python serial port (tools only) |

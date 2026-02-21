# EnOcean-MQTT Gateway

A Go application that bridges EnOcean devices with MQTT for home automation. Includes tools for serial multiplexing, packet capture, and Wireshark protocol analysis.

## Features

- **Bidirectional Communication**: Receive telegrams and send commands to EnOcean devices
- **Friendly Device Names**: Configure device mappings in YAML for human-readable MQTT topics
- **RPS Switch Simulation**: Control Eltako actuators via rocker switch (F6) protocol
- **Echo Filtering**: Filters out own transmissions from published telegrams
- **MQTT LWT**: Bridge status (online/offline) via Last Will and Testament
- **Wireshark Integration**: Full ESP3 protocol dissector with bidirectional capture support

## Architecture

```
┌─────────────┐     ┌──────────────┐     ┌─────────────────┐     ┌──────────┐
│ EnOcean USB │────▶│ serial_mux.py│────▶│ enocean-mqtt(Go)│────▶│  MQTT    │
│  Adapter    │◀────│  (TCP 2000)  │◀────│                 │◀────│  Broker  │
└─────────────┘     └──────────────┘     └─────────────────┘     └──────────┘
                           │
                           ▼ TCP 2001 (capture)
                    ┌──────────────┐     ┌───────────┐
                    │ enodump.py   │────▶│ Wireshark │
                    │  (PCAP)      │     │           │
                    └──────────────┘     └───────────┘
```

## Installation

### Gateway (Go)

```bash
go mod tidy
go build -o enocean-mqtt
```

### Tools (Python)

```bash
cd tools
pip install -r requirements.txt
```

### Wireshark Dissector

Copy the Lua dissector to your Wireshark plugins directory:

```bash
# macOS
cp tools/wireshark/enocean.lua ~/.config/wireshark/plugins/

# Linux
cp tools/wireshark/enocean.lua ~/.local/lib/wireshark/plugins/

# Windows
copy tools\wireshark\enocean.lua %APPDATA%\Wireshark\plugins\
```

## Configuration

Create `config.yaml` from the example:

```bash
cp config.yaml.example config.yaml
```

Edit to match your setup:

```yaml
enocean:
  host: "192.168.1.100"      # serial_mux.py host
  port: 2000

mqtt:
  host: "192.168.1.100"
  port: 1883
  client_id: "enocean-mqtt-gateway"
  topic: "enocean"

devices:
  "01:90:B7:27":
    name: "kitchen_light"     # Friendly name
    type: "actuator"          # actuator, switch, dimmer
    sender_id: "FF:A0:1D:87"  # Sender ID for commands
```

## Usage

### 1. Start the Serial Multiplexer

On the machine with the EnOcean USB adapter:

```bash
python tools/serial_mux.py /dev/ttyUSB0
```

Options:
- `--port 2000` - Client port (default: 2000)
- `--capture-port 2001` - Bidirectional capture port

### 2. Run the Gateway

```bash
./enocean-mqtt -config config.yaml
```

### 3. Capture Packets (Optional)

For debugging with Wireshark:

```bash
# Legacy capture (RX only)
python tools/enodump.py --host 192.168.1.100 --port 2000 | wireshark -k -i -

# Bidirectional capture (RX + TX)
python tools/enodump.py --host 192.168.1.100 --port 2001 -b | wireshark -k -i -
```

## MQTT Topics

### Published Topics

| Topic | Description |
|-------|-------------|
| `enocean/bridge/status` | Bridge status: "online" / "offline" (retained, LWT) |
| `enocean/telegram` | All received EnOcean telegrams |
| `enocean/device/{name}/switch/{button}` | RPS rocker events (e.g., `kitchen_light/switch/A`) |
| `enocean/device/{id}` | Device telegrams (fallback if no name configured) |

### Command Topics

| Topic | Payload | Description |
|-------|---------|-------------|
| `enocean/cmd/{device_name}` | `on` / `off` | Control device by friendly name |
| `enocean/cmd/{device_id}` | `on` / `off` | Control device by ID (AA:BB:CC:DD) |

Example:
```bash
mosquitto_pub -t "enocean/cmd/kitchen_light" -m "on"
mosquitto_pub -t "enocean/cmd/kitchen_light" -m "off"
```

### Message Format

```json
{
  "timestamp": "2024-01-15T10:30:00Z",
  "sender_id": "01:90:B7:27",
  "device_name": "kitchen_light",
  "packet_type": 1,
  "data_length": 7,
  "data": "f6300190b72730",
  "raw": "55000707..."
}
```

## EnOcean Protocol Support

### Telegram Types (RORG)
- **F6 (RPS)**: Rocker switches - used for controlling Eltako actuators
- **D5 (1BS)**: Contact sensors
- **A5 (4BS)**: Temperature, humidity sensors
- **D2 (VLD)**: Variable length data (smart actuators)
- **D4 (UTE)**: Universal teach-in

### RPS Encoding (Rocker Switches)
| Data | Button | Action |
|------|--------|--------|
| 0x10 | A0 | Off (release) |
| 0x30 | A1 | On (press) |
| 0x50 | B0 | Off (release) |
| 0x70 | B1 | On (press) |

## Project Structure

```
enocean-mqtt/
├── config/
│   └── config.go           # Configuration loading
├── enocean/
│   └── client.go           # ESP3 protocol & TCP client
├── mqtt/
│   └── client.go           # MQTT client with LWT
├── tools/
│   ├── serial_mux.py       # Serial-to-TCP multiplexer
│   ├── enodump.py          # PCAP capture tool
│   ├── requirements.txt    # Python dependencies
│   └── wireshark/
│       └── enocean.lua     # ESP3 Wireshark dissector
├── main.go                 # Application entry point
├── config.yaml.example     # Example configuration
├── go.mod
└── README.md
```

## Dependencies

### Go
- [paho.mqtt.golang](https://github.com/eclipse/paho.mqtt.golang) - MQTT client
- [gopkg.in/yaml.v3](https://gopkg.in/yaml.v3) - YAML configuration

### Python
- pyserial - Serial port access

## License

MIT

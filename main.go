package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"enocean-mqtt/config"
	"enocean-mqtt/enocean"
	"enocean-mqtt/mqtt"
)

// ActuatorCommand represents an MQTT command to control an actuator
type ActuatorCommand struct {
	Name     string `json:"name"`      // Friendly device name (optional, alternative to sender_id)
	SenderID string `json:"sender_id"` // Gateway sender ID used during teach-in
	DeviceID string `json:"device_id"` // Actuator's ID
	Channel  byte   `json:"channel"`
	State    string `json:"state"` // "on", "off", or "dim"
	Value    byte   `json:"value"` // 0-100 for dim
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	configPath := flag.String("config", "config.yaml", "Path to config file")
	flag.Parse()

	log.Println("Starting EnOcean-MQTT Gateway")

	// Load config from file, fall back to defaults
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Printf("Warning: Could not load config file %s: %v", *configPath, err)
		log.Println("Using default configuration")
		cfg = config.DefaultConfig()
	} else {
		log.Printf("Loaded config from %s (%d devices configured)", *configPath, len(cfg.Devices))
	}

	// Create EnOcean client
	enoceanClient := enocean.NewClient(cfg.EnOcean.Host, cfg.EnOcean.Port)

	// Create MQTT client with config for device name resolution
	mqttClient := mqtt.NewClientWithConfig(
		cfg.MQTT.Host,
		cfg.MQTT.Port,
		cfg.MQTT.ClientID,
		cfg.MQTT.Topic,
		cfg,
	)

	// Set up telegram handler - forward EnOcean telegrams to MQTT
	enoceanClient.SetTelegramHandler(func(telegram *enocean.Telegram) {
		senderID := telegram.GetSenderID()
		deviceName := cfg.GetDeviceName(senderID)

		// Filter out echoes from our own transmissions
		if cfg.IsOwnSenderID(senderID) {
			log.Printf("Ignoring echo from own sender ID: %s", senderID)
			return
		}

		log.Printf("Received EnOcean telegram: Type=%d, Data=%s, SenderID=%s (%s)",
			telegram.PacketType,
			hex.EncodeToString(telegram.Data),
			senderID,
			deviceName)

		// Parse VLD telegrams (D2-xx profiles)
		if len(telegram.Data) > 0 && telegram.Data[0] == enocean.RORG_VLD {
			vldData := telegram.Data[1 : len(telegram.Data)-5] // Exclude RORG byte and sender ID + status

			if vld, err := enocean.ParseVLDTelegram(vldData, senderID); err == nil {
				if measurement, ok := vld.Parsed.(*enocean.D2_01_MeasurementResponse); ok {
					log.Printf("D2-01 Measurement: Channel=%d, Value=%d %s",
						measurement.IOChannel,
						measurement.MeasurementValue,
						measurement.UnitName)

					if err := mqttClient.PublishMeasurement(senderID, measurement); err != nil {
						log.Printf("Failed to publish measurement: %v", err)
					}
				}

				if status, ok := vld.Parsed.(*enocean.D2_01_StatusResponse); ok {
					log.Printf("D2-01 Status: Channel=%d, OutputValue=%d, IsOn=%v",
						status.IOChannel, status.OutputValue, status.IsOn)

					if err := mqttClient.PublishDeviceState(senderID, status); err != nil {
						log.Printf("Failed to publish device state: %v", err)
					}
				}
			}
		}

		// Parse RPS telegrams (F6-xx profiles - Rocker Switch)
		if len(telegram.Data) > 0 && telegram.Data[0] == enocean.RORG_RPS {
			// RPS format: RORG(1) + Data(1) + SenderID(4) + Status(1)
			if len(telegram.Data) >= 7 {
				rpsData := telegram.Data[1]
				status := telegram.Data[6]

				rps := enocean.ParseRPSTelegram(rpsData, status, senderID)
				action := "released"
				if rps.EnergyBow {
					action = "pressed"
				}
				log.Printf("RPS Rocker: %s Channel %s %s (%s)",
					senderID, rps.Action1Channel, rps.Action1Direction, action)

				if err := mqttClient.PublishRockerSwitch(rps); err != nil {
					log.Printf("Failed to publish rocker switch: %v", err)
				}
			}
		}

		if err := mqttClient.PublishTelegram(telegram); err != nil {
			log.Printf("Failed to publish telegram to MQTT: %v", err)
		}
	})

	// Set up MQTT command handler - forward commands to EnOcean
	mqttClient.SetCommandHandler(func(topic string, payload []byte) {
		log.Printf("Received MQTT command on %s: %s", topic, string(payload))

		// Handle specific commands
		if strings.HasSuffix(topic, "/cmd/reset") {
			log.Println("Processing reset command...")
			if err := enoceanClient.Reset(); err != nil {
				log.Printf("Failed to reset gateway: %v", err)
				mqttClient.PublishEvent("reset", "error", err.Error())
			} else {
				log.Println("Gateway reset initiated")
				mqttClient.PublishEvent("reset", "success", "Gateway reset initiated")
			}
			return
		}

		// Handle actuator commands: enocean/cmd/actuator
		if strings.HasSuffix(topic, "/cmd/actuator") {
			var cmd ActuatorCommand
			if err := json.Unmarshal(payload, &cmd); err != nil {
				log.Printf("Invalid actuator command JSON: %v", err)
				mqttClient.PublishEvent("actuator", "error", "Invalid JSON: "+err.Error())
				return
			}

			if cmd.SenderID == "" {
				log.Printf("Missing sender_id in actuator command")
				mqttClient.PublishEvent("actuator", "error", "Missing sender_id")
				return
			}

			var outputValue byte
			switch strings.ToLower(cmd.State) {
			case "on":
				outputValue = 101 // D2-01 switch on value
			case "off":
				outputValue = 0
			case "dim":
				if cmd.Value > 100 {
					outputValue = 100
				} else {
					outputValue = cmd.Value
				}
			default:
				log.Printf("Unknown state: %s", cmd.State)
				mqttClient.PublishEvent("actuator", "error", "Unknown state: "+cmd.State)
				return
			}

			if err := enoceanClient.SendActuatorOutput(cmd.SenderID, cmd.DeviceID, cmd.Channel, outputValue); err != nil {
				log.Printf("Failed to send actuator command: %v", err)
				mqttClient.PublishEvent("actuator", "error", err.Error())
			} else {
				mqttClient.PublishEvent("actuator", "success", "Command sent to "+cmd.DeviceID)
			}
			return
		}

		// Handle Eltako commands (RPS F6 rocker switch): enocean/cmd/eltako
		if strings.HasSuffix(topic, "/cmd/eltako") {
			var cmd ActuatorCommand
			if err := json.Unmarshal(payload, &cmd); err != nil {
				log.Printf("Invalid Eltako command JSON: %v", err)
				mqttClient.PublishEvent("eltako", "error", "Invalid JSON: "+err.Error())
				return
			}

			if cmd.SenderID == "" {
				log.Printf("Missing sender_id in Eltako command")
				mqttClient.PublishEvent("eltako", "error", "Missing sender_id")
				return
			}

			on := strings.ToLower(cmd.State) == "on"

			if err := enoceanClient.SendEltakoSwitch(cmd.SenderID, on); err != nil {
				log.Printf("Failed to send Eltako command: %v", err)
				mqttClient.PublishEvent("eltako", "error", err.Error())
			} else {
				mqttClient.PublishEvent("eltako", "success", "Command sent with sender "+cmd.SenderID)
			}
			return
		}

		// Handle device commands by friendly name: enocean/cmd/{device_name}
		// Payload: "on" or "off" (simple string, not JSON)
		// Extract device name from topic (last segment after /cmd/)
		if strings.Contains(topic, "/cmd/") {
			parts := strings.Split(topic, "/cmd/")
			if len(parts) == 2 {
				deviceName := parts[1]

				// Look up device by friendly name or ID
				deviceID, device := cfg.GetDeviceByName(deviceName)
				if device == nil {
					// Try as device ID directly
					deviceID = deviceName
					device = cfg.GetDevice(deviceID)
				}

				if device != nil {
					// Parse simple on/off payload
					state := strings.ToLower(strings.TrimSpace(string(payload)))

					senderID := device.SenderID
					if senderID == "" {
						log.Printf("No sender_id configured for device %s", deviceName)
						mqttClient.PublishEvent(deviceName, "error", "No sender_id configured")
						return
					}

					// Determine action based on device type
					switch device.Type {
					case "switch", "actuator", "light", "":
						// RPS F6 rocker switch (default)
						on := state == "on"
						if err := enoceanClient.SendEltakoSwitch(senderID, on); err != nil {
							log.Printf("Failed to send command to %s: %v", deviceName, err)
							mqttClient.PublishEvent(deviceName, "error", err.Error())
						} else {
							log.Printf("Sent %s command to %s (sender: %s)", state, deviceName, senderID)
							mqttClient.PublishEvent(deviceName, "success", "Command sent: "+state)
						}
					case "dimmer":
						// D2-01 VLD dimmer
						var outputValue byte
						switch state {
						case "on":
							outputValue = 101
						case "off":
							outputValue = 0
						default:
							// Try to parse as number for dimming
							log.Printf("Unknown dimmer state: %s", state)
							return
						}
						if err := enoceanClient.SendActuatorOutput(senderID, deviceID, 0, outputValue); err != nil {
							log.Printf("Failed to send dimmer command to %s: %v", deviceName, err)
							mqttClient.PublishEvent(deviceName, "error", err.Error())
						} else {
							log.Printf("Sent dimmer command to %s: value=%d", deviceName, outputValue)
							mqttClient.PublishEvent(deviceName, "success", "Dimmer command sent")
						}
					}
					return
				} else {
					log.Printf("Unknown device: %s", deviceName)
					mqttClient.PublishEvent(deviceName, "error", "Unknown device")
					return
				}
			}
		}

		// Default: send raw hex data
		data, err := hex.DecodeString(string(payload))
		if err != nil {
			log.Printf("Invalid hex payload: %v", err)
			return
		}
		if err := enoceanClient.Send(data); err != nil {
			log.Printf("Failed to send to EnOcean: %v", err)
		}
	})

	// Set up event handler for EnOcean events
	enoceanClient.SetEventHandler(func(eventCode byte, data []byte) {
		log.Printf("EnOcean event: Code=0x%02X, Data=%s", eventCode, hex.EncodeToString(data))

		switch eventCode {
		case enocean.CO_READY:
			log.Println("Gateway is ready (CO_READY event received)")
			mqttClient.PublishEvent("gateway", "ready", "Gateway is ready after reset")

			// Re-read and publish version info after reset
			go func() {
				if versionInfo, err := enoceanClient.ReadVersion(); err == nil {
					log.Printf("Gateway: %s (App: %s, API: %s, Chip: %s)",
						versionInfo.AppDescription,
						versionInfo.AppVersion,
						versionInfo.APIVersion,
						versionInfo.ChipID)
					mqttClient.PublishVersionInfo(versionInfo)
				}
			}()
		default:
			mqttClient.PublishEvent("unknown", "event", hex.EncodeToString(append([]byte{eventCode}, data...)))
		}
	})

	// Connect to MQTT broker
	if err := mqttClient.Connect(); err != nil {
		log.Fatalf("Failed to connect to MQTT: %v", err)
	}
	defer mqttClient.Close()

	// Connect to EnOcean gateway
	if err := enoceanClient.Connect(); err != nil {
		log.Fatalf("Failed to connect to EnOcean: %v", err)
	}
	defer enoceanClient.Close()

	// Initialize gateway (read IDs, disable repeater, enable maturity)
	if err := enoceanClient.Initialize(); err != nil {
		log.Printf("Warning: Gateway initialization incomplete: %v", err)
	}
	log.Printf("Gateway Base ID: %s", enoceanClient.GetBaseID())

	// Publish gateway info to MQTT
	if versionInfo, err := enoceanClient.ReadVersion(); err == nil {
		log.Printf("Gateway: %s (App: %s, API: %s, Chip: %s)",
			versionInfo.AppDescription,
			versionInfo.AppVersion,
			versionInfo.APIVersion,
			versionInfo.ChipID)
		if err := mqttClient.PublishVersionInfo(versionInfo); err != nil {
			log.Printf("Warning: Failed to publish version info: %v", err)
		}
	}
	mqttClient.PublishEvent("gateway", "ready", "Gateway initialized, Base ID: "+enoceanClient.GetBaseID())

	log.Println("Gateway running. Press Ctrl+C to exit.")

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down...")
}

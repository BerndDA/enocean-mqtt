package config

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	EnOcean EnOceanConfig     `yaml:"enocean"`
	MQTT    MQTTConfig        `yaml:"mqtt"`
	Devices map[string]Device `yaml:"devices"` // Device ID -> Device info
}

type EnOceanConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type MQTTConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	ClientID string `yaml:"client_id"`
	Topic    string `yaml:"topic"`
}

type Device struct {
	Name     string `yaml:"name"`      // Friendly name for MQTT topics
	Type     string `yaml:"type"`      // Device type: "switch", "sensor", "actuator"
	SenderID string `yaml:"sender_id"` // Sender ID to use when controlling (for actuators)
}

// GetDeviceName returns the friendly name for a device ID, or the ID itself if not found
func (c *Config) GetDeviceName(deviceID string) string {
	// Normalize the ID (uppercase, with colons)
	normalizedID := strings.ToUpper(deviceID)
	if device, ok := c.Devices[normalizedID]; ok && device.Name != "" {
		return device.Name
	}
	return deviceID
}

// GetDeviceByName finds a device by its friendly name
func (c *Config) GetDeviceByName(name string) (string, *Device) {
	nameLower := strings.ToLower(name)
	for id, device := range c.Devices {
		if strings.ToLower(device.Name) == nameLower {
			return id, &device
		}
	}
	return "", nil
}

// IsOwnSenderID checks if a sender ID is one of our configured transmitter IDs
// These are echoes from our own transmissions and should typically be ignored
func (c *Config) IsOwnSenderID(senderID string) bool {
	normalizedID := strings.ToUpper(senderID)
	for _, device := range c.Devices {
		if device.SenderID != "" && strings.ToUpper(device.SenderID) == normalizedID {
			return true
		}
	}
	return false
}

// Load reads config from a YAML file
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func DefaultConfig() *Config {
	return &Config{
		EnOcean: EnOceanConfig{
			Host: "192.168.178.24",
			Port: 2000,
		},
		MQTT: MQTTConfig{
			Host:     "192.168.178.24",
			Port:     2222,
			ClientID: "enocean-mqtt-gateway",
			Topic:    "enocean",
		},
		Devices: make(map[string]Device),
	}
}

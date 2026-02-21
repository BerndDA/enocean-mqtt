package mqtt

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"enocean-mqtt/config"
	"enocean-mqtt/enocean"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// Client handles MQTT communication
type Client struct {
	host      string
	port      int
	clientID  string
	baseTopic string
	client    paho.Client
	onCommand func(topic string, payload []byte)
	config    *config.Config // Config for device name resolution
}

// TelegramMessage represents the JSON structure for MQTT messages
type TelegramMessage struct {
	Timestamp    string `json:"timestamp"`
	DeviceName   string `json:"device_name,omitempty"`
	SenderID     string `json:"sender_id,omitempty"`
	PacketType   byte   `json:"packet_type"`
	DataLength   uint16 `json:"data_length"`
	Data         string `json:"data"`
	OptionalData string `json:"optional_data,omitempty"`
	Raw          string `json:"raw"`
}

// NewClient creates a new MQTT client (without config, for backwards compatibility)
func NewClient(host string, port int, clientID, baseTopic string) *Client {
	return &Client{
		host:      host,
		port:      port,
		clientID:  clientID,
		baseTopic: baseTopic,
	}
}

// NewClientWithConfig creates a new MQTT client with config for device name resolution
func NewClientWithConfig(host string, port int, clientID, baseTopic string, cfg *config.Config) *Client {
	return &Client{
		host:      host,
		port:      port,
		clientID:  clientID,
		baseTopic: baseTopic,
		config:    cfg,
	}
}

// getDeviceName resolves device ID to friendly name
func (c *Client) getDeviceName(deviceID string) string {
	if c.config != nil {
		return c.config.GetDeviceName(deviceID)
	}
	return deviceID
}

// getTopicName returns a sanitized name suitable for MQTT topics
func (c *Client) getTopicName(deviceID string) string {
	name := c.getDeviceName(deviceID)
	// Replace colons with underscores for topic compatibility
	return strings.ReplaceAll(name, ":", "_")
}

// SetCommandHandler sets callback for received MQTT commands
func (c *Client) SetCommandHandler(handler func(topic string, payload []byte)) {
	c.onCommand = handler
}

// Connect establishes connection to MQTT broker
func (c *Client) Connect() error {
	opts := paho.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tcp://%s:%d", c.host, c.port))
	opts.SetClientID(c.clientID)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(5 * time.Second)

	// Set Last Will and Testament - published when connection drops unexpectedly
	bridgeTopic := fmt.Sprintf("%s/bridge/status", c.baseTopic)
	opts.SetWill(bridgeTopic, "offline", 1, true)

	opts.SetOnConnectHandler(func(client paho.Client) {
		log.Printf("Connected to MQTT broker at %s:%d", c.host, c.port)
		// Publish online status
		client.Publish(bridgeTopic, 1, true, "online")
		c.subscribe()
	})

	opts.SetConnectionLostHandler(func(client paho.Client, err error) {
		log.Printf("MQTT connection lost: %v", err)
	})

	c.client = paho.NewClient(opts)

	token := c.client.Connect()
	if token.Wait() && token.Error() != nil {
		return fmt.Errorf("failed to connect to MQTT broker: %w", token.Error())
	}

	return nil
}

// subscribe subscribes to command topics
func (c *Client) subscribe() {
	cmdTopic := fmt.Sprintf("%s/cmd/#", c.baseTopic)
	token := c.client.Subscribe(cmdTopic, 1, func(client paho.Client, msg paho.Message) {
		log.Printf("Received MQTT message on %s", msg.Topic())
		if c.onCommand != nil {
			c.onCommand(msg.Topic(), msg.Payload())
		}
	})

	if token.Wait() && token.Error() != nil {
		log.Printf("Failed to subscribe to %s: %v", cmdTopic, token.Error())
	} else {
		log.Printf("Subscribed to %s", cmdTopic)
	}
}

// Close disconnects from MQTT broker
func (c *Client) Close() {
	if c.client != nil && c.client.IsConnected() {
		// Publish offline status before disconnecting
		bridgeTopic := fmt.Sprintf("%s/bridge/status", c.baseTopic)
		token := c.client.Publish(bridgeTopic, 1, true, "offline")
		token.Wait()
		c.client.Disconnect(1000)
	}
}

// IsConnected returns connection status
func (c *Client) IsConnected() bool {
	return c.client != nil && c.client.IsConnected()
}

// PublishTelegram publishes an EnOcean telegram to MQTT
func (c *Client) PublishTelegram(telegram *enocean.Telegram) error {
	senderID := telegram.GetSenderID()
	deviceName := c.getDeviceName(senderID)
	topicName := c.getTopicName(senderID)

	msg := TelegramMessage{
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		DeviceName: deviceName,
		SenderID:   senderID,
		PacketType: byte(telegram.PacketType),
		DataLength: telegram.DataLength,
		Data:       hex.EncodeToString(telegram.Data),
		Raw:        hex.EncodeToString(telegram.Raw),
	}

	if len(telegram.OptionalData) > 0 {
		msg.OptionalData = hex.EncodeToString(telegram.OptionalData)
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal telegram: %w", err)
	}

	// Publish to base topic
	topic := fmt.Sprintf("%s/telegram", c.baseTopic)
	token := c.client.Publish(topic, 1, false, payload)
	if token.Wait() && token.Error() != nil {
		return fmt.Errorf("failed to publish: %w", token.Error())
	}

	// Publish to device-specific topic using friendly name
	if senderID != "" {
		deviceTopic := fmt.Sprintf("%s/device/%s", c.baseTopic, topicName)
		token = c.client.Publish(deviceTopic, 1, false, payload)
		if token.Wait() && token.Error() != nil {
			log.Printf("Failed to publish to device topic: %v", token.Error())
		}
	}

	return nil
}

// Publish publishes a message to a specific topic
func (c *Client) Publish(topic string, payload []byte) error {
	token := c.client.Publish(topic, 1, false, payload)
	if token.Wait() && token.Error() != nil {
		return fmt.Errorf("failed to publish: %w", token.Error())
	}
	return nil
}

// GatewayInfoMessage represents gateway info for MQTT
type GatewayInfoMessage struct {
	Timestamp      string `json:"timestamp"`
	AppVersion     string `json:"app_version"`
	APIVersion     string `json:"api_version"`
	ChipID         string `json:"chip_id"`
	ChipVersion    string `json:"chip_version"`
	AppDescription string `json:"app_description"`
}

// PublishVersionInfo publishes gateway version info to MQTT
func (c *Client) PublishVersionInfo(info *enocean.VersionInfo) error {
	msg := GatewayInfoMessage{
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		AppVersion:     info.AppVersion,
		APIVersion:     info.APIVersion,
		ChipID:         info.ChipID,
		ChipVersion:    info.ChipVersion,
		AppDescription: info.AppDescription,
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal version info: %w", err)
	}

	topic := fmt.Sprintf("%s/gateway/info", c.baseTopic)
	token := c.client.Publish(topic, 1, true, payload) // retained message
	if token.Wait() && token.Error() != nil {
		return fmt.Errorf("failed to publish version info: %w", token.Error())
	}

	log.Printf("Published gateway info to %s", topic)
	return nil
}

// MeasurementMessage represents energy/power measurement for MQTT
type MeasurementMessage struct {
	Timestamp        string `json:"timestamp"`
	DeviceName       string `json:"device_name,omitempty"`
	SenderID         string `json:"sender_id"`
	Profile          string `json:"profile"`
	IOChannel        byte   `json:"io_channel"`
	MeasurementValue uint32 `json:"measurement_value"`
	Unit             string `json:"unit"`
}

// PublishMeasurement publishes a measurement to MQTT
func (c *Client) PublishMeasurement(senderID string, measurement *enocean.D2_01_MeasurementResponse) error {
	deviceName := c.getDeviceName(senderID)
	topicName := c.getTopicName(senderID)

	msg := MeasurementMessage{
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
		DeviceName:       deviceName,
		SenderID:         senderID,
		Profile:          "D2-01",
		IOChannel:        measurement.IOChannel,
		MeasurementValue: measurement.MeasurementValue,
		Unit:             measurement.UnitName,
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal measurement: %w", err)
	}

	// Publish to device measurement topic using friendly name
	topic := fmt.Sprintf("%s/device/%s/measurement", c.baseTopic, topicName)
	token := c.client.Publish(topic, 1, false, payload)
	if token.Wait() && token.Error() != nil {
		return fmt.Errorf("failed to publish measurement: %w", token.Error())
	}

	log.Printf("Published measurement to %s: %d %s", topic, measurement.MeasurementValue, measurement.UnitName)
	return nil
}

// EventMessage represents an event for MQTT
type EventMessage struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	Message   string `json:"message"`
}

// PublishEvent publishes an event to MQTT
func (c *Client) PublishEvent(eventType, status, message string) error {
	msg := EventMessage{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Type:      eventType,
		Status:    status,
		Message:   message,
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	topic := fmt.Sprintf("%s/event/%s", c.baseTopic, eventType)
	token := c.client.Publish(topic, 1, false, payload)
	if token.Wait() && token.Error() != nil {
		return fmt.Errorf("failed to publish event: %w", token.Error())
	}

	log.Printf("Published event to %s: %s - %s", topic, status, message)
	return nil
}

// RockerSwitchMessage represents a rocker switch event for MQTT
type RockerSwitchMessage struct {
	Timestamp  string `json:"timestamp"`
	DeviceName string `json:"device_name,omitempty"`
	SenderID   string `json:"sender_id"`
	Profile    string `json:"profile"`
	Channel    string `json:"channel"`
	Pressed    bool   `json:"pressed"`
	Direction  string `json:"direction"`
}

// PublishRockerSwitch publishes rocker switch events to MQTT (one per channel)
func (c *Client) PublishRockerSwitch(rps *enocean.RPSData) error {
	timestamp := time.Now().UTC().Format(time.RFC3339)
	deviceName := c.getDeviceName(rps.SenderID)

	// Publish first action
	msg := RockerSwitchMessage{
		Timestamp:  timestamp,
		DeviceName: deviceName,
		SenderID:   rps.SenderID,
		Profile:    rps.Profile,
		Channel:    rps.Action1Channel,
		Pressed:    rps.EnergyBow,
		Direction:  rps.Action1Direction,
	}

	if err := c.publishChannelMessage(rps.SenderID, rps.Action1Channel, msg); err != nil {
		return err
	}

	// Publish second action if valid (both buttons pressed simultaneously)
	if rps.Action2Valid {
		msg2 := RockerSwitchMessage{
			Timestamp:  timestamp,
			DeviceName: deviceName,
			SenderID:   rps.SenderID,
			Profile:    rps.Profile,
			Channel:    rps.Action2Channel,
			Pressed:    rps.EnergyBow,
			Direction:  rps.Action2Direction,
		}
		if err := c.publishChannelMessage(rps.SenderID, rps.Action2Channel, msg2); err != nil {
			return err
		}
	}

	return nil
}

// publishChannelMessage publishes a single channel message
func (c *Client) publishChannelMessage(senderID, channel string, msg RockerSwitchMessage) error {
	topicName := c.getTopicName(senderID)

	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal rocker switch: %w", err)
	}

	// Publish to channel-specific topic using friendly name: enocean/device/{name}/switch/{channel}
	topic := fmt.Sprintf("%s/device/%s/switch/%s", c.baseTopic, topicName, channel)
	token := c.client.Publish(topic, 1, false, payload)
	if token.Wait() && token.Error() != nil {
		return fmt.Errorf("failed to publish rocker switch: %w", token.Error())
	}

	state := "released"
	if msg.Pressed {
		state = "pressed"
	}
	log.Printf("Published switch to %s: %s %s", topic, msg.Direction, state)
	return nil
}

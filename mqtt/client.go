package mqtt

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"enocean-mqtt/config"
	"enocean-mqtt/enocean"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// Client handles MQTT communication
type Client struct {
	host          string
	port          int
	clientID      string
	baseTopic     string
	client        paho.Client
	onCommand     func(topic string, payload []byte)
	config        *config.Config // Config for device name resolution
	mu            sync.Mutex
	channelStates map[string]map[byte]byte   // senderID → ioChannel → outputValue (D2-01)
	deviceStates  map[string]bool            // senderID → combined state (true=on, false=off)
	stateKnown    map[string]bool            // senderID → whether a combined state has been published
	pendingOff    map[string]int             // senderID → pending off telegram count while debouncing
	pendingToken  map[string]uint64          // senderID → debounce token for timeout invalidation
	rockerStates  map[string]map[string]bool // senderID → channel (A/B) → isOn
	rockerKnown   map[string]bool            // senderID → whether rocker combined state is known
	rockerOn      map[string]bool            // senderID → rocker combined state (true=on, false=off)
	rockerPendOff map[string]int             // senderID → pending rocker off telegram count
	rockerToken   map[string]uint64          // senderID → rocker debounce token for timeout invalidation
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
		host:          host,
		port:          port,
		clientID:      clientID,
		baseTopic:     baseTopic,
		deviceStates:  make(map[string]bool),
		stateKnown:    make(map[string]bool),
		pendingOff:    make(map[string]int),
		pendingToken:  make(map[string]uint64),
		rockerStates:  make(map[string]map[string]bool),
		rockerKnown:   make(map[string]bool),
		rockerOn:      make(map[string]bool),
		rockerPendOff: make(map[string]int),
		rockerToken:   make(map[string]uint64),
	}
}

// NewClientWithConfig creates a new MQTT client with config for device name resolution
func NewClientWithConfig(host string, port int, clientID, baseTopic string, cfg *config.Config) *Client {
	return &Client{
		host:          host,
		port:          port,
		clientID:      clientID,
		baseTopic:     baseTopic,
		config:        cfg,
		deviceStates:  make(map[string]bool),
		stateKnown:    make(map[string]bool),
		pendingOff:    make(map[string]int),
		pendingToken:  make(map[string]uint64),
		rockerStates:  make(map[string]map[string]bool),
		rockerKnown:   make(map[string]bool),
		rockerOn:      make(map[string]bool),
		rockerPendOff: make(map[string]int),
		rockerToken:   make(map[string]uint64),
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

// PublishDeviceState tracks per-channel output values and publishes a retained
// combined state topic: "on" if any channel is on, "off" if all channels are off.
// Topic: enocean/device/{name}/state
func (c *Client) PublishDeviceState(senderID string, status *enocean.D2_01_StatusResponse) error {
	var stateToPublish string
	shouldPublish := false

	c.mu.Lock()
	if c.channelStates == nil {
		c.channelStates = make(map[string]map[byte]byte)
	}
	if c.channelStates[senderID] == nil {
		c.channelStates[senderID] = make(map[byte]byte)
	}
	c.channelStates[senderID][status.IOChannel] = status.OutputValue

	anyOn := false
	for _, val := range c.channelStates[senderID] {
		if val > 0 && val != 127 {
			anyOn = true
			break
		}
	}

	known := c.stateKnown[senderID]
	isOn := c.deviceStates[senderID]

	if anyOn {
		c.pendingOff[senderID] = 0
		c.pendingToken[senderID]++

		if !known || !isOn {
			c.stateKnown[senderID] = true
			c.deviceStates[senderID] = true
			stateToPublish = "on"
			shouldPublish = true
		}
	} else {
		if !known {
			c.stateKnown[senderID] = true
			c.deviceStates[senderID] = false
			c.pendingOff[senderID] = 0
			c.pendingToken[senderID]++
			stateToPublish = "off"
			shouldPublish = true
		} else if isOn {
			c.pendingOff[senderID]++

			if c.pendingOff[senderID] == 1 {
				token := c.pendingToken[senderID] + 1
				c.pendingToken[senderID] = token

				time.AfterFunc(1*time.Second, func() {
					c.handlePendingOffTimeout(senderID, token)
				})
			} else if c.pendingOff[senderID] >= 2 {
				c.pendingOff[senderID] = 0
				c.pendingToken[senderID]++
				c.deviceStates[senderID] = false
				stateToPublish = "off"
				shouldPublish = true
			}
		}
	}
	c.mu.Unlock()

	if !shouldPublish {
		return nil
	}

	if err := c.publishDeviceStateValue(senderID, stateToPublish); err != nil {
		return err
	}

	log.Printf("Published device state for %s: %s (channel %d = %d)", senderID, stateToPublish, status.IOChannel, status.OutputValue)
	return nil
}

func (c *Client) handlePendingOffTimeout(senderID string, token uint64) {
	shouldPublish := false

	c.mu.Lock()
	if c.pendingToken[senderID] == token && c.pendingOff[senderID] > 0 {
		anyOn := false
		for _, val := range c.channelStates[senderID] {
			if val > 0 && val != 127 {
				anyOn = true
				break
			}
		}

		if !anyOn && c.stateKnown[senderID] && c.deviceStates[senderID] {
			c.pendingOff[senderID] = 0
			c.pendingToken[senderID]++
			c.deviceStates[senderID] = false
			shouldPublish = true
		} else {
			c.pendingOff[senderID] = 0
			c.pendingToken[senderID]++
		}
	}
	c.mu.Unlock()

	if !shouldPublish {
		return
	}

	if err := c.publishDeviceStateValue(senderID, "off"); err != nil {
		log.Printf("Failed to publish debounced off state for %s: %v", senderID, err)
		return
	}

	log.Printf("Published debounced device state for %s: off", senderID)
}

func (c *Client) publishDeviceStateValue(senderID, state string) error {
	topicName := c.getTopicName(senderID)
	topic := fmt.Sprintf("%s/device/%s/state", c.baseTopic, topicName)
	token := c.client.Publish(topic, 1, true, state)
	if token.Wait() && token.Error() != nil {
		return fmt.Errorf("failed to publish device state: %w", token.Error())
	}
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
	offTelegrams := 0

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

	// Update rocker state on button press (DOWN=on, UP=off)
	if rps.EnergyBow {
		if rps.Action1Direction == "UP" {
			offTelegrams++
		}
		c.updateRockerState(rps.SenderID, rps.Action1Channel, rps.Action1Direction == "DOWN")
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

		// Update second channel state on button press
		if rps.EnergyBow {
			if rps.Action2Direction == "UP" {
				offTelegrams++
			}
			c.updateRockerState(rps.SenderID, rps.Action2Channel, rps.Action2Direction == "DOWN")
		}
	}

	// Publish combined state (retained) if button was pressed
	if rps.EnergyBow {
		c.publishRockerCombinedState(rps.SenderID, offTelegrams)
	}

	return nil
}

// updateRockerState updates the internal state for a rocker switch channel
func (c *Client) updateRockerState(senderID, channel string, isOn bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.rockerStates[senderID] == nil {
		c.rockerStates[senderID] = make(map[string]bool)
	}
	c.rockerStates[senderID][channel] = isOn
}

// publishRockerCombinedState publishes the combined ON/OFF state for a rocker switch
// State is "on" if at least one channel is on, "off" if all channels are off
func (c *Client) publishRockerCombinedState(senderID string, offTelegrams int) {
	var stateToPublish string
	shouldPublish := false

	c.mu.Lock()
	if c.rockerKnown == nil {
		c.rockerKnown = make(map[string]bool)
	}
	if c.rockerOn == nil {
		c.rockerOn = make(map[string]bool)
	}
	if c.rockerPendOff == nil {
		c.rockerPendOff = make(map[string]int)
	}
	if c.rockerToken == nil {
		c.rockerToken = make(map[string]uint64)
	}

	channels := c.rockerStates[senderID]
	isOn := false
	for _, on := range channels {
		if on {
			isOn = true
			break
		}
	}

	known := c.rockerKnown[senderID]
	currentOn := c.rockerOn[senderID]

	if isOn {
		if known && currentOn && offTelegrams > 0 {
			previous := c.rockerPendOff[senderID]
			c.rockerPendOff[senderID] = previous + offTelegrams

			if previous == 0 {
				token := c.rockerToken[senderID] + 1
				c.rockerToken[senderID] = token

				time.AfterFunc(1*time.Second, func() {
					c.handleRockerPendingOffTimeout(senderID, token)
				})
			}
		} else {
			c.rockerPendOff[senderID] = 0
			c.rockerToken[senderID]++
		}

		if !known || !currentOn {
			c.rockerKnown[senderID] = true
			c.rockerOn[senderID] = true
			stateToPublish = "on"
			shouldPublish = true
		}
	} else {
		if !known {
			c.rockerKnown[senderID] = true
			c.rockerOn[senderID] = false
			c.rockerPendOff[senderID] = 0
			c.rockerToken[senderID]++
			stateToPublish = "off"
			shouldPublish = true
		} else if currentOn {
			previous := c.rockerPendOff[senderID]
			if offTelegrams > 0 {
				c.rockerPendOff[senderID] = previous + offTelegrams
			} else {
				c.rockerPendOff[senderID] = previous + 1
			}

			if previous == 0 {
				token := c.rockerToken[senderID] + 1
				c.rockerToken[senderID] = token

				time.AfterFunc(1*time.Second, func() {
					c.handleRockerPendingOffTimeout(senderID, token)
				})
			}

			if c.rockerPendOff[senderID] >= 2 {
				c.rockerPendOff[senderID] = 0
				c.rockerToken[senderID]++
				c.rockerOn[senderID] = false
				stateToPublish = "off"
				shouldPublish = true
			}
		}
	}
	c.mu.Unlock()

	if !shouldPublish {
		return
	}

	if err := c.publishDeviceStateValue(senderID, stateToPublish); err != nil {
		log.Printf("Failed to publish rocker state for %s: %v", senderID, err)
		return
	}

	log.Printf("Published rocker state for %s: %s", senderID, stateToPublish)
}

func (c *Client) handleRockerPendingOffTimeout(senderID string, token uint64) {
	shouldPublish := false

	c.mu.Lock()
	if c.rockerToken[senderID] == token && c.rockerPendOff[senderID] > 0 {
		isOn := false
		for _, on := range c.rockerStates[senderID] {
			if on {
				isOn = true
				break
			}
		}

		if !isOn && c.rockerKnown[senderID] && c.rockerOn[senderID] {
			c.rockerPendOff[senderID] = 0
			c.rockerToken[senderID]++
			c.rockerOn[senderID] = false
			shouldPublish = true
		} else if isOn && c.rockerKnown[senderID] && c.rockerOn[senderID] {
			// Keep pending off count while still on so a later UP can complete
			// the 2-telegram debounce path immediately.
		} else {
			c.rockerPendOff[senderID] = 0
			c.rockerToken[senderID]++
		}
	}
	c.mu.Unlock()

	if !shouldPublish {
		return
	}

	if err := c.publishDeviceStateValue(senderID, "off"); err != nil {
		log.Printf("Failed to publish debounced rocker off state for %s: %v", senderID, err)
		return
	}

	log.Printf("Published debounced rocker state for %s: off", senderID)
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

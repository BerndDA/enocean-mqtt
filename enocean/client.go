package enocean

import (
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"time"
)

// PacketType defines EnOcean packet types
type PacketType byte

const (
	PacketTypeRadioERP1    PacketType = 0x01
	PacketTypeResponse     PacketType = 0x02
	PacketTypeRadioSubTel  PacketType = 0x03
	PacketTypeEvent        PacketType = 0x04
	PacketTypeCommonCmd    PacketType = 0x05
	PacketTypeSmartAckCmd  PacketType = 0x06
	PacketTypeRemoteManCmd PacketType = 0x07
)

// Common Command codes
const (
	CO_WR_RESET         byte = 0x02
	CO_RD_VERSION       byte = 0x03
	CO_RD_IDBASE        byte = 0x08
	CO_WR_REPEATER      byte = 0x09
	CO_WR_WAIT_MATURITY byte = 0x10
)

// Event codes
const (
	CO_READY byte = 0x04
)

// Response return codes
const (
	RET_OK byte = 0x00
)

// RORG (Radio ORG) types
const (
	RORG_RPS byte = 0xF6 // Repeated Switch Communication
	RORG_1BS byte = 0xD5 // 1 Byte Communication
	RORG_4BS byte = 0xA5 // 4 Byte Communication
	RORG_VLD byte = 0xD2 // Variable Length Data
)

// VersionInfo contains gateway version information
type VersionInfo struct {
	AppVersion     string `json:"app_version"`
	APIVersion     string `json:"api_version"`
	ChipID         string `json:"chip_id"`
	ChipVersion    string `json:"chip_version"`
	AppDescription string `json:"app_description"`
}

// Telegram represents an EnOcean telegram
type Telegram struct {
	SyncByte     byte
	DataLength   uint16
	OptionalLen  byte
	PacketType   PacketType
	Data         []byte
	OptionalData []byte
	CRC8H        byte
	CRC8D        byte
	Raw          []byte
}

// Client handles TCP connection to EnOcean gateway
type Client struct {
	host         string
	port         int
	conn         net.Conn
	mu           sync.Mutex
	connected    bool
	stopping     bool
	onTelegram   func(*Telegram)
	onEvent      func(eventCode byte, data []byte)
	stopChan     chan struct{}
	responseChan chan *Telegram
	buffer       []byte
	baseID       []byte // Gateway base ID for transmitting
}

// NewClient creates a new EnOcean TCP client
func NewClient(host string, port int) *Client {
	return &Client{
		host:         host,
		port:         port,
		stopChan:     make(chan struct{}),
		responseChan: make(chan *Telegram, 1),
	}
}

// SetTelegramHandler sets the callback for received telegrams
func (c *Client) SetTelegramHandler(handler func(*Telegram)) {
	c.onTelegram = handler
}

// SetEventHandler sets the callback for received events
func (c *Client) SetEventHandler(handler func(eventCode byte, data []byte)) {
	c.onEvent = handler
}

// Connect establishes TCP connection to EnOcean gateway
func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	addr := net.JoinHostPort(c.host, strconv.Itoa(c.port))
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect to EnOcean gateway: %w", err)
	}

	c.conn = conn
	c.connected = true
	log.Printf("Connected to EnOcean gateway at %s", addr)

	go c.readLoop()

	// Give read loop time to start
	time.Sleep(100 * time.Millisecond)

	return nil
}

// Close closes the TCP connection
func (c *Client) Close() error {
	c.mu.Lock()
	c.stopping = true
	c.connected = false
	c.mu.Unlock()

	close(c.stopChan)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// IsConnected returns connection status
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// readLoop continuously reads telegrams from the connection
func (c *Client) readLoop() {
	buf := make([]byte, 1024)

	for {
		select {
		case <-c.stopChan:
			return
		default:
			c.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
			n, err := c.conn.Read(buf)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				c.mu.Lock()
				stopping := c.stopping
				c.mu.Unlock()
				if stopping {
					return
				}
				if err == io.EOF {
					log.Println("EnOcean connection closed")
					c.mu.Lock()
					c.connected = false
					c.mu.Unlock()
					return
				}
				log.Printf("Error reading from EnOcean: %v", err)
				continue
			}

			if n > 0 {
				log.Printf("Received %d bytes: %s", n, hex.EncodeToString(buf[:n]))
				c.buffer = append(c.buffer, buf[:n]...)
				c.parseBuffer()
			}
		}
	}
}

// parseBuffer processes buffered data and extracts complete telegrams
func (c *Client) parseBuffer() {
	for {
		// Find sync byte
		syncIdx := -1
		for i, b := range c.buffer {
			if b == 0x55 {
				syncIdx = i
				break
			}
		}

		if syncIdx == -1 {
			c.buffer = nil
			return
		}

		// Discard bytes before sync
		if syncIdx > 0 {
			c.buffer = c.buffer[syncIdx:]
		}

		// Need at least 6 bytes for header
		if len(c.buffer) < 6 {
			return
		}

		dataLen := uint16(c.buffer[1])<<8 | uint16(c.buffer[2])
		optLen := c.buffer[3]
		packetType := PacketType(c.buffer[4])
		crc8h := c.buffer[5]

		totalLen := 6 + int(dataLen) + int(optLen) + 1

		// Wait for complete packet
		if len(c.buffer) < totalLen {
			return
		}

		// Verify header CRC
		if !c.verifyCRC8(c.buffer[1:5], crc8h) {
			log.Println("Invalid header CRC, searching for next sync byte")
			c.buffer = c.buffer[1:]
			continue
		}

		telegram := &Telegram{
			SyncByte:    0x55,
			DataLength:  dataLen,
			OptionalLen: optLen,
			PacketType:  packetType,
			CRC8H:       crc8h,
			Data:        make([]byte, dataLen),
			Raw:         make([]byte, totalLen),
		}

		copy(telegram.Data, c.buffer[6:6+int(dataLen)])
		copy(telegram.Raw, c.buffer[:totalLen])

		if optLen > 0 {
			telegram.OptionalData = make([]byte, optLen)
			copy(telegram.OptionalData, c.buffer[6+int(dataLen):6+int(dataLen)+int(optLen)])
		}

		telegram.CRC8D = c.buffer[totalLen-1]

		log.Printf("Parsed telegram: Type=%d, DataLen=%d", telegram.PacketType, telegram.DataLength)

		// Route packets based on type
		switch telegram.PacketType {
		case PacketTypeResponse:
			select {
			case c.responseChan <- telegram:
			default:
				// Drop if channel full
			}
		case PacketTypeEvent:
			if len(telegram.Data) > 0 && c.onEvent != nil {
				eventCode := telegram.Data[0]
				eventData := telegram.Data[1:]
				c.onEvent(eventCode, eventData)
			}
		default:
			if c.onTelegram != nil {
				c.onTelegram(telegram)
			}
		}

		// Remove processed packet from buffer
		c.buffer = c.buffer[totalLen:]
	}
}

// verifyCRC8 verifies CRC8 checksum
func (c *Client) verifyCRC8(data []byte, crc byte) bool {
	calculated := c.calculateCRC8(data)
	return calculated == crc
}

// calculateCRC8 calculates CRC8 for EnOcean ESP3 protocol
func (c *Client) calculateCRC8(data []byte) byte {
	crc8Table := []byte{
		0x00, 0x07, 0x0e, 0x09, 0x1c, 0x1b, 0x12, 0x15,
		0x38, 0x3f, 0x36, 0x31, 0x24, 0x23, 0x2a, 0x2d,
		0x70, 0x77, 0x7e, 0x79, 0x6c, 0x6b, 0x62, 0x65,
		0x48, 0x4f, 0x46, 0x41, 0x54, 0x53, 0x5a, 0x5d,
		0xe0, 0xe7, 0xee, 0xe9, 0xfc, 0xfb, 0xf2, 0xf5,
		0xd8, 0xdf, 0xd6, 0xd1, 0xc4, 0xc3, 0xca, 0xcd,
		0x90, 0x97, 0x9e, 0x99, 0x8c, 0x8b, 0x82, 0x85,
		0xa8, 0xaf, 0xa6, 0xa1, 0xb4, 0xb3, 0xba, 0xbd,
		0xc7, 0xc0, 0xc9, 0xce, 0xdb, 0xdc, 0xd5, 0xd2,
		0xff, 0xf8, 0xf1, 0xf6, 0xe3, 0xe4, 0xed, 0xea,
		0xb7, 0xb0, 0xb9, 0xbe, 0xab, 0xac, 0xa5, 0xa2,
		0x8f, 0x88, 0x81, 0x86, 0x93, 0x94, 0x9d, 0x9a,
		0x27, 0x20, 0x29, 0x2e, 0x3b, 0x3c, 0x35, 0x32,
		0x1f, 0x18, 0x11, 0x16, 0x03, 0x04, 0x0d, 0x0a,
		0x57, 0x50, 0x59, 0x5e, 0x4b, 0x4c, 0x45, 0x42,
		0x6f, 0x68, 0x61, 0x66, 0x73, 0x74, 0x7d, 0x7a,
		0x89, 0x8e, 0x87, 0x80, 0x95, 0x92, 0x9b, 0x9c,
		0xb1, 0xb6, 0xbf, 0xb8, 0xad, 0xaa, 0xa3, 0xa4,
		0xf9, 0xfe, 0xf7, 0xf0, 0xe5, 0xe2, 0xeb, 0xec,
		0xc1, 0xc6, 0xcf, 0xc8, 0xdd, 0xda, 0xd3, 0xd4,
		0x69, 0x6e, 0x67, 0x60, 0x75, 0x72, 0x7b, 0x7c,
		0x51, 0x56, 0x5f, 0x58, 0x4d, 0x4a, 0x43, 0x44,
		0x19, 0x1e, 0x17, 0x10, 0x05, 0x02, 0x0b, 0x0c,
		0x21, 0x26, 0x2f, 0x28, 0x3d, 0x3a, 0x33, 0x34,
		0x4e, 0x49, 0x40, 0x47, 0x52, 0x55, 0x5c, 0x5b,
		0x76, 0x71, 0x78, 0x7f, 0x6a, 0x6d, 0x64, 0x63,
		0x3e, 0x39, 0x30, 0x37, 0x22, 0x25, 0x2c, 0x2b,
		0x06, 0x01, 0x08, 0x0f, 0x1a, 0x1d, 0x14, 0x13,
		0xae, 0xa9, 0xa0, 0xa7, 0xb2, 0xb5, 0xbc, 0xbb,
		0x96, 0x91, 0x98, 0x9f, 0x8a, 0x8d, 0x84, 0x83,
		0xde, 0xd9, 0xd0, 0xd7, 0xc2, 0xc5, 0xcc, 0xcb,
		0xe6, 0xe1, 0xe8, 0xef, 0xfa, 0xfd, 0xf4, 0xf3,
	}

	var crc byte = 0
	for _, b := range data {
		crc = crc8Table[crc^b]
	}
	return crc
}

// Send sends raw data to the EnOcean gateway
func (c *Client) Send(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected || c.conn == nil {
		return fmt.Errorf("not connected")
	}

	_, err := c.conn.Write(data)
	return err
}

// GetSenderID extracts sender ID from telegram data
func (t *Telegram) GetSenderID() string {
	if len(t.Data) < 5 {
		return ""
	}
	// Sender ID is typically the last 4 bytes before status byte in radio telegrams
	if t.PacketType == PacketTypeRadioERP1 && len(t.Data) >= 6 {
		return fmt.Sprintf("%02X:%02X:%02X:%02X",
			t.Data[len(t.Data)-5],
			t.Data[len(t.Data)-4],
			t.Data[len(t.Data)-3],
			t.Data[len(t.Data)-2])
	}
	return ""
}

// buildPacket creates an ESP3 packet
func (c *Client) buildPacket(packetType PacketType, data []byte, optionalData []byte) []byte {
	dataLen := len(data)
	optLen := len(optionalData)

	// Header: Sync + DataLen(2) + OptLen(1) + PacketType(1) + CRC8H(1)
	header := []byte{
		0x55,
		byte(dataLen >> 8),
		byte(dataLen & 0xFF),
		byte(optLen),
		byte(packetType),
	}
	crc8h := c.calculateCRC8(header[1:5])
	header = append(header, crc8h)

	// Build full packet
	packet := append(header, data...)
	if optLen > 0 {
		packet = append(packet, optionalData...)
	}

	// Calculate data CRC
	crcData := append(data, optionalData...)
	crc8d := c.calculateCRC8(crcData)
	packet = append(packet, crc8d)

	return packet
}

// ReadVersion sends CO_RD_VERSION command and returns gateway version info
func (c *Client) ReadVersion() (*VersionInfo, error) {
	// Build CO_RD_VERSION command packet
	packet := c.buildPacket(PacketTypeCommonCmd, []byte{CO_RD_VERSION}, nil)
	log.Printf("Sending CO_RD_VERSION packet: %s", hex.EncodeToString(packet))

	// Clear any pending responses
	select {
	case <-c.responseChan:
	default:
	}

	// Send command
	if err := c.Send(packet); err != nil {
		return nil, fmt.Errorf("failed to send CO_RD_VERSION: %w", err)
	}

	// Wait for response with timeout
	select {
	case resp := <-c.responseChan:
		log.Printf("Received response: Type=%d, DataLen=%d, Data=%s", resp.PacketType, resp.DataLength, hex.EncodeToString(resp.Data))
		return c.parseVersionResponse(resp)
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("timeout waiting for version response")
	}
}

// parseVersionResponse parses the CO_RD_VERSION response
func (c *Client) parseVersionResponse(resp *Telegram) (*VersionInfo, error) {
	// Response format:
	// Return code (1) + APP version (4) + API version (4) + Chip ID (4) + Chip version (4) + APP description (16)
	// Total: 33 bytes
	if len(resp.Data) < 33 {
		return nil, fmt.Errorf("invalid version response length: %d", len(resp.Data))
	}

	if resp.Data[0] != RET_OK {
		return nil, fmt.Errorf("version command failed with code: 0x%02X", resp.Data[0])
	}

	info := &VersionInfo{
		AppVersion: fmt.Sprintf("%d.%d.%d.%d",
			resp.Data[1], resp.Data[2], resp.Data[3], resp.Data[4]),
		APIVersion: fmt.Sprintf("%d.%d.%d.%d",
			resp.Data[5], resp.Data[6], resp.Data[7], resp.Data[8]),
		ChipID: fmt.Sprintf("%02X:%02X:%02X:%02X",
			resp.Data[9], resp.Data[10], resp.Data[11], resp.Data[12]),
		ChipVersion: fmt.Sprintf("%d.%d.%d.%d",
			resp.Data[13], resp.Data[14], resp.Data[15], resp.Data[16]),
	}

	// Parse APP description (null-terminated string)
	descBytes := resp.Data[17:33]
	for i, b := range descBytes {
		if b == 0 {
			descBytes = descBytes[:i]
			break
		}
	}
	info.AppDescription = string(descBytes)

	return info, nil
}

// Reset sends CO_WR_RESET command to restart the gateway without clearing settings
func (c *Client) Reset() error {
	// CO_WR_RESET has no parameters - just the command code
	packet := c.buildPacket(PacketTypeCommonCmd, []byte{CO_WR_RESET}, nil)
	log.Printf("Sending CO_WR_RESET packet: %s", hex.EncodeToString(packet))

	// Clear any pending responses
	select {
	case <-c.responseChan:
	default:
	}

	// Send command
	if err := c.Send(packet); err != nil {
		return fmt.Errorf("failed to send CO_WR_RESET: %w", err)
	}

	// Wait for response with timeout
	select {
	case resp := <-c.responseChan:
		if len(resp.Data) > 0 && resp.Data[0] == RET_OK {
			log.Println("Gateway reset command accepted")
			return nil
		}
		return fmt.Errorf("reset command failed with code: 0x%02X", resp.Data[0])
	case <-time.After(5 * time.Second):
		return fmt.Errorf("timeout waiting for reset response")
	}
}

// ReadBaseID reads the gateway base ID for transmitting
func (c *Client) ReadBaseID() error {
	packet := c.buildPacket(PacketTypeCommonCmd, []byte{CO_RD_IDBASE}, nil)
	log.Printf("Sending CO_RD_IDBASE packet: %s", hex.EncodeToString(packet))

	// Clear any pending responses
	select {
	case <-c.responseChan:
	default:
	}

	// Send command
	if err := c.Send(packet); err != nil {
		return fmt.Errorf("failed to send CO_RD_IDBASE: %w", err)
	}

	// Wait for response with timeout
	select {
	case resp := <-c.responseChan:
		// Response: Return code (1) + Base ID (4) + Remaining write cycles (1)
		if len(resp.Data) < 5 {
			return fmt.Errorf("invalid base ID response length: %d", len(resp.Data))
		}
		if resp.Data[0] != RET_OK {
			return fmt.Errorf("read base ID failed with code: 0x%02X", resp.Data[0])
		}
		c.baseID = make([]byte, 4)
		copy(c.baseID, resp.Data[1:5])
		log.Printf("Gateway Base ID: %02X:%02X:%02X:%02X",
			c.baseID[0], c.baseID[1], c.baseID[2], c.baseID[3])
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("timeout waiting for base ID response")
	}
}

// GetBaseID returns the gateway base ID as formatted string
func (c *Client) GetBaseID() string {
	if len(c.baseID) != 4 {
		return ""
	}
	return fmt.Sprintf("%02X:%02X:%02X:%02X",
		c.baseID[0], c.baseID[1], c.baseID[2], c.baseID[3])
}

// DisableRepeater disables the gateway repeater function
// This matches what the reference client sends: CO_WR_REPEATER with 00:00
func (c *Client) DisableRepeater() error {
	// CO_WR_REPEATER: RepeaterEnable(1) + RepeaterLevel(1)
	// 0x00 = Repeater off, 0x00 = Level (ignored when off)
	packet := c.buildPacket(PacketTypeCommonCmd, []byte{CO_WR_REPEATER, 0x00, 0x00}, nil)
	log.Printf("Sending CO_WR_REPEATER (disable): %s", hex.EncodeToString(packet))

	select {
	case <-c.responseChan:
	default:
	}

	if err := c.Send(packet); err != nil {
		return fmt.Errorf("failed to send CO_WR_REPEATER: %w", err)
	}

	select {
	case resp := <-c.responseChan:
		if len(resp.Data) > 0 && resp.Data[0] == RET_OK {
			log.Println("Repeater disabled")
			return nil
		}
		return fmt.Errorf("disable repeater failed with code: 0x%02X", resp.Data[0])
	case <-time.After(5 * time.Second):
		return fmt.Errorf("timeout waiting for repeater response")
	}
}

// SetWaitMaturity enables waiting for telegram maturity before processing
// This ensures telegrams are fully received before being forwarded
func (c *Client) SetWaitMaturity(enable bool) error {
	var maturity byte = 0x00
	if enable {
		maturity = 0x01
	}
	packet := c.buildPacket(PacketTypeCommonCmd, []byte{CO_WR_WAIT_MATURITY, maturity}, nil)
	log.Printf("Sending CO_WR_WAIT_MATURITY (%v): %s", enable, hex.EncodeToString(packet))

	select {
	case <-c.responseChan:
	default:
	}

	if err := c.Send(packet); err != nil {
		return fmt.Errorf("failed to send CO_WR_WAIT_MATURITY: %w", err)
	}

	select {
	case resp := <-c.responseChan:
		if len(resp.Data) > 0 && resp.Data[0] == RET_OK {
			log.Printf("Wait maturity set to %v", enable)
			return nil
		}
		return fmt.Errorf("set wait maturity failed with code: 0x%02X", resp.Data[0])
	case <-time.After(5 * time.Second):
		return fmt.Errorf("timeout waiting for maturity response")
	}
}

// Initialize performs the standard gateway initialization sequence
// This matches the reference client startup: reset, read IDs, configure repeater/maturity
func (c *Client) Initialize() error {
	// 1. Read base ID first (needed for sending)
	if err := c.ReadBaseID(); err != nil {
		return fmt.Errorf("initialization failed: %w", err)
	}

	// 2. Read version info
	if _, err := c.ReadVersion(); err != nil {
		log.Printf("Warning: failed to read version: %v", err)
	}

	// 3. Disable repeater mode
	if err := c.DisableRepeater(); err != nil {
		log.Printf("Warning: failed to disable repeater: %v", err)
	}

	// 4. Enable wait for maturity
	if err := c.SetWaitMaturity(true); err != nil {
		log.Printf("Warning: failed to set wait maturity: %v", err)
	}

	return nil
}

// ParseSenderID parses a sender ID string (XX:XX:XX:XX) to bytes
func ParseSenderID(id string) ([]byte, error) {
	// Remove colons
	clean := ""
	for _, c := range id {
		if c != ':' {
			clean += string(c)
		}
	}
	if len(clean) != 8 {
		return nil, fmt.Errorf("invalid sender ID format: %s", id)
	}

	bytes := make([]byte, 4)
	for i := 0; i < 4; i++ {
		_, err := fmt.Sscanf(clean[i*2:i*2+2], "%02x", &bytes[i])
		if err != nil {
			return nil, fmt.Errorf("invalid hex in sender ID: %s", id)
		}
	}
	return bytes, nil
}

// SendActuatorOutput sends D2-01 CMD 0x01 to set actuator output (on/off/dim)
// senderID: the sender ID used during teach-in (e.g., "FF:A0:1D:87")
// destinationID: actuator ID (e.g., "05:01:7E:43")
// channel: I/O channel (0-29)
// outputValue: 0=off, 1-100=dim level, 101=on (full)
func (c *Client) SendActuatorOutput(senderID, destinationID string, channel byte, outputValue byte) error {
	sendID, err := ParseSenderID(senderID)
	if err != nil {
		return fmt.Errorf("invalid sender ID: %w", err)
	}

	destID, err := ParseSenderID(destinationID)
	if err != nil {
		return fmt.Errorf("invalid destination ID: %w", err)
	}

	// D2-01 CMD 0x01: Actuator Set Output
	// Byte 0: 0000 0001 = CMD (0x01)
	// Byte 1: CCCC C000 = I/O channel (upper 5 bits) + DimValue (lower 3 bits, we use 0)
	// Byte 2: VVVV VVVV = Output value (0-100, or 101=switch on)
	vldData := []byte{
		0x01,                  // CMD: Actuator Set Output
		(channel & 0x1F) << 3, // I/O channel in upper 5 bits
		outputValue,           // Output value
	}

	// Build RADIO_ERP1 data: RORG + VLD data + Sender ID + Status
	radioData := []byte{RORG_VLD}
	radioData = append(radioData, vldData...)
	radioData = append(radioData, sendID...)
	radioData = append(radioData, 0x00) // Status

	// Optional data: SubTelNum(1) + DestinationID(4) + dBm(1) + SecurityLevel(1)
	optionalData := []byte{0x03} // SubTelNum = 3
	optionalData = append(optionalData, destID...)
	optionalData = append(optionalData, 0xFF) // dBm (max power)
	optionalData = append(optionalData, 0x00) // Security Level

	packet := c.buildPacket(PacketTypeRadioERP1, radioData, optionalData)
	log.Printf("Sending actuator command to %s: channel=%d, value=%d, packet=%s",
		destinationID, channel, outputValue, hex.EncodeToString(packet))

	// Clear any pending responses
	select {
	case <-c.responseChan:
	default:
	}

	// Send command
	if err := c.Send(packet); err != nil {
		return fmt.Errorf("failed to send actuator command: %w", err)
	}

	// Wait for transmit response
	select {
	case resp := <-c.responseChan:
		if len(resp.Data) > 0 && resp.Data[0] == RET_OK {
			log.Printf("Actuator command sent successfully to %s", destinationID)
			return nil
		}
		return fmt.Errorf("actuator command failed with code: 0x%02X", resp.Data[0])
	case <-time.After(5 * time.Second):
		return fmt.Errorf("timeout waiting for transmit response")
	}
}

// SendRPSSwitch sends F6 RPS Rocker Switch telegram (for Eltako and similar actuators)
// This simulates a physical rocker switch press/release.
// senderID: the sender ID used during teach-in (e.g., "FF:A0:1D:87")
// on: true=switch on (button I/B0), false=switch off (button O/A0)
// pressed: true=button pressed (energy bow), false=button released
func (c *Client) SendRPSSwitch(senderID string, on bool, pressed bool) error {
	sendID, err := ParseSenderID(senderID)
	if err != nil {
		return fmt.Errorf("invalid sender ID: %w", err)
	}

	// RPS F6-02-01/02 Rocker Switch encoding:
	// Bits 7-5: Rocker 1st action (button ID)
	//   000 = Button A0 (top-left, typically "off")
	//   001 = Button A1 (bottom-left)
	//   010 = Button B0 (top-right, typically "on")
	//   011 = Button B1 (bottom-right)
	// Bit 4: Energy Bow (1=pressed, 0=released)
	// Bits 3-1: Rocker 2nd action
	// Bit 0: 2nd action valid
	var rpsData byte
	if on {
		rpsData = 0x50 // Button B0 (010 << 5) = 0x40, will add EB below
	} else {
		rpsData = 0x10 // Button A0 (000 << 5) = 0x00, will add EB below
	}
	if pressed {
		rpsData |= 0x10 // Energy Bow bit (already included in values above)
	} else {
		rpsData &= 0xEF // Clear Energy Bow bit
	}

	// Correct the encoding based on capture:
	// For "off" pressed:  0x10 (A0 + EB) - top button
	// For "off" released: 0x00 (A0, no EB)
	// For "on" pressed:   0x30 (A1 + EB) - bottom button
	// For "on" released:  0x20 (A1, no EB)
	if on {
		if pressed {
			rpsData = 0x30
		} else {
			rpsData = 0x20
		}
	} else {
		if pressed {
			rpsData = 0x10
		} else {
			rpsData = 0x00
		}
	}

	// Status byte: T21=1, NU=1 for normal RPS operation
	// T21 (bit 5) = 1: PTM module uses EEP table
	// NU (bit 4) = 1: N-message (normal message)
	status := byte(0x30)

	// Build RADIO_ERP1 data: RORG + RPS data + Sender ID + Status
	radioData := []byte{RORG_RPS}
	radioData = append(radioData, rpsData)
	radioData = append(radioData, sendID...)
	radioData = append(radioData, status)

	// No optional data - broadcast like the working client
	packet := c.buildPacket(PacketTypeRadioERP1, radioData, nil)

	state := "OFF"
	if on {
		state = "ON"
	}
	action := "released"
	if pressed {
		action = "pressed"
	}
	log.Printf("Sending RPS switch: %s %s, sender=%s, packet=%s",
		state, action, senderID, hex.EncodeToString(packet))

	// Clear any pending responses
	select {
	case <-c.responseChan:
	default:
	}

	// Send command
	if err := c.Send(packet); err != nil {
		return fmt.Errorf("failed to send RPS command: %w", err)
	}

	// Wait for transmit response
	select {
	case resp := <-c.responseChan:
		if len(resp.Data) > 0 && resp.Data[0] == RET_OK {
			log.Printf("RPS command sent successfully")
			return nil
		}
		return fmt.Errorf("RPS command failed with code: 0x%02X", resp.Data[0])
	case <-time.After(5 * time.Second):
		return fmt.Errorf("timeout waiting for transmit response")
	}
}

// SendEltakoSwitch sends F6 RPS rocker switch telegram to control Eltako actuators
// This is a convenience wrapper that sends a button press.
// senderID: the sender ID used during teach-in (e.g., "FF:A0:1D:87")
// on: true=switch on, false=switch off
func (c *Client) SendEltakoSwitch(senderID string, on bool) error {
	// Send button press
	return c.SendRPSSwitch(senderID, on, true)
}

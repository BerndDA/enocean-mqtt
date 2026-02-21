package enocean

import (
	"encoding/json"
	"fmt"
)

// D2-01 Command types
const (
	D2_01_CMD_ACTUATOR_SET_OUTPUT             byte = 0x01
	D2_01_CMD_ACTUATOR_SET_LOCAL              byte = 0x02
	D2_01_CMD_ACTUATOR_STATUS_QUERY           byte = 0x03
	D2_01_CMD_ACTUATOR_STATUS_RESPONSE        byte = 0x04
	D2_01_CMD_ACTUATOR_SET_MEASUREMENT        byte = 0x05
	D2_01_CMD_ACTUATOR_MEASUREMENT_QUERY      byte = 0x06
	D2_01_CMD_ACTUATOR_MEASUREMENT_RESPONSE   byte = 0x07
	D2_01_CMD_ACTUATOR_SET_PILOT_WIRE         byte = 0x08
	D2_01_CMD_ACTUATOR_PILOT_WIRE_QUERY       byte = 0x09
	D2_01_CMD_ACTUATOR_PILOT_WIRE_RESPONSE    byte = 0x0A
	D2_01_CMD_ACTUATOR_SET_EXTERNAL_INTERFACE byte = 0x0B
)

// MeasurementUnit types for D2-01
type MeasurementUnit byte

const (
	MeasurementUnitEnergy MeasurementUnit = 0 // Energy (Ws)
	MeasurementUnitPower  MeasurementUnit = 1 // Power (W)
)

func (m MeasurementUnit) String() string {
	switch m {
	case MeasurementUnitEnergy:
		return "Ws"
	case MeasurementUnitPower:
		return "W"
	default:
		return "unknown"
	}
}

// VLDData represents parsed VLD telegram data
type VLDData struct {
	RORG     byte   `json:"rorg"`
	SenderID string `json:"sender_id"`
	Data     []byte `json:"-"`
	Profile  string `json:"profile,omitempty"`
	Parsed   any    `json:"parsed,omitempty"`
}

// D2_01_MeasurementResponse represents D2-01 Actuator Measurement Response
type D2_01_MeasurementResponse struct {
	Command          byte            `json:"command"`
	CommandName      string          `json:"command_name"`
	IOChannel        byte            `json:"io_channel"`
	Unit             MeasurementUnit `json:"unit"`
	UnitName         string          `json:"unit_name"`
	MeasurementValue uint32          `json:"measurement_value"`
}

// ParseVLDTelegram parses a VLD (D2) telegram
func ParseVLDTelegram(data []byte, senderID string) (*VLDData, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("VLD data too short: %d bytes", len(data))
	}

	vld := &VLDData{
		RORG:     RORG_VLD,
		SenderID: senderID,
		Data:     data,
	}

	// Try to parse as D2-01 (Electronic Switch/Dimmer)
	if parsed, err := parseD2_01(data); err == nil {
		vld.Profile = "D2-01"
		vld.Parsed = parsed
	}

	return vld, nil
}

// parseD2_01 parses D2-01 Electronic Switch/Dimmer telegrams
func parseD2_01(data []byte) (any, error) {
	if len(data) < 1 {
		return nil, fmt.Errorf("D2-01 data too short")
	}

	// Command is in lower 4 bits of first byte
	cmd := data[0] & 0x0F

	switch cmd {
	case D2_01_CMD_ACTUATOR_MEASUREMENT_RESPONSE:
		return parseD2_01_MeasurementResponse(data)
	default:
		return nil, fmt.Errorf("unknown D2-01 command: 0x%02X", cmd)
	}
}

// parseD2_01_MeasurementResponse parses command 0x07
func parseD2_01_MeasurementResponse(data []byte) (*D2_01_MeasurementResponse, error) {
	// Format:
	// Byte 0: .... CCCC = Command (0x07)
	// Byte 1: ...C CCCC = I/O Channel (5 bits, upper bit from byte 0)
	// Byte 2: .... .UUU = Measurement Unit (3 bits)
	// Bytes 2-5: Measurement Value (32 bits, upper bits in byte 2)

	if len(data) < 6 {
		return nil, fmt.Errorf("D2-01 measurement response too short: %d bytes", len(data))
	}

	cmd := data[0] & 0x0F
	ioChannel := ((data[0]>>4)&0x01)<<4 | (data[1] & 0x1F)
	unit := MeasurementUnit(data[2] & 0x07)

	// Measurement value is in upper 5 bits of byte 2 + bytes 3-5 (29 bits total, but typically 32-bit aligned)
	// Actually looking at the data 076000000000:
	// Byte 0: 0x07 -> cmd=7
	// Byte 1: 0x60 -> ioChannel = 0
	// Byte 2: 0x00 -> unit = 0
	// Bytes 3-5: 0x000000 -> value = 0

	measurementValue := uint32(data[2]>>3)<<24 | uint32(data[3])<<16 | uint32(data[4])<<8 | uint32(data[5])

	return &D2_01_MeasurementResponse{
		Command:          cmd,
		CommandName:      "Actuator Measurement Response",
		IOChannel:        ioChannel,
		Unit:             unit,
		UnitName:         unit.String(),
		MeasurementValue: measurementValue,
	}, nil
}

// ToJSON converts VLDData to JSON
func (v *VLDData) ToJSON() ([]byte, error) {
	return json.Marshal(v)
}

// RockerAction represents a rocker button action
type RockerAction byte

const (
	RockerActionAI RockerAction = 0 // Channel A, Top/Up (I)
	RockerActionAO RockerAction = 1 // Channel A, Bottom/Down (O)
	RockerActionBI RockerAction = 2 // Channel B, Top/Up (I)
	RockerActionBO RockerAction = 3 // Channel B, Bottom/Down (O)
)

func (r RockerAction) String() string {
	switch r {
	case RockerActionAI:
		return "A_UP"
	case RockerActionAO:
		return "A_DOWN"
	case RockerActionBI:
		return "B_UP"
	case RockerActionBO:
		return "B_DOWN"
	default:
		return "UNKNOWN"
	}
}

func (r RockerAction) Channel() string {
	switch r {
	case RockerActionAI, RockerActionAO:
		return "A"
	case RockerActionBI, RockerActionBO:
		return "B"
	default:
		return "?"
	}
}

func (r RockerAction) Direction() string {
	switch r {
	case RockerActionAI, RockerActionBI:
		return "UP"
	case RockerActionAO, RockerActionBO:
		return "DOWN"
	default:
		return "?"
	}
}

// RPSData represents parsed RPS telegram data (F6-02 Rocker Switch)
type RPSData struct {
	SenderID         string       `json:"sender_id"`
	Profile          string       `json:"profile"`
	EnergyBow        bool         `json:"energy_bow"`        // true=pressed, false=released
	Action1          RockerAction `json:"action1"`           // First rocker action
	Action1Name      string       `json:"action1_name"`      // Human-readable action
	Action1Channel   string       `json:"action1_channel"`   // "A" or "B"
	Action1Direction string       `json:"action1_direction"` // "UP" or "DOWN"
	Action2Valid     bool         `json:"action2_valid"`     // Is second action valid?
	Action2          RockerAction `json:"action2,omitempty"`
	Action2Name      string       `json:"action2_name,omitempty"`
	Action2Channel   string       `json:"action2_channel,omitempty"`
	Action2Direction string       `json:"action2_direction,omitempty"`
	T21              bool         `json:"t21"` // EEP Table
	NU               bool         `json:"nu"`  // N-Message
}

// ParseRPSTelegram parses an RPS (F6) telegram - Rocker Switch
func ParseRPSTelegram(data byte, status byte, senderID string) *RPSData {
	// RPS Data byte format:
	// Bits 7-5: Rocker 1st Action (0-7)
	// Bit 4: Energy Bow (1=pressed, 0=released)
	// Bits 3-1: Rocker 2nd Action (0-7)
	// Bit 0: 2nd Action Valid
	//
	// Status byte:
	// Bit 5: T21 (EEP Table)
	// Bit 4: NU (N-Message)
	// Bits 3-0: Repeater Counter

	action1 := RockerAction((data >> 5) & 0x07)
	energyBow := (data & 0x10) != 0
	action2 := RockerAction((data >> 1) & 0x07)
	action2Valid := (data & 0x01) != 0
	t21 := (status & 0x20) != 0
	nu := (status & 0x10) != 0

	rps := &RPSData{
		SenderID:         senderID,
		Profile:          "F6-02",
		EnergyBow:        energyBow,
		Action1:          action1,
		Action1Name:      action1.String(),
		Action1Channel:   action1.Channel(),
		Action1Direction: action1.Direction(),
		Action2Valid:     action2Valid,
		T21:              t21,
		NU:               nu,
	}

	if action2Valid {
		rps.Action2 = action2
		rps.Action2Name = action2.String()
		rps.Action2Channel = action2.Channel()
		rps.Action2Direction = action2.Direction()
	}

	return rps
}

// ToJSON converts RPSData to JSON
func (r *RPSData) ToJSON() ([]byte, error) {
	return json.Marshal(r)
}

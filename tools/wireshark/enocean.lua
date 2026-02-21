-- EnOcean ESP3 Dissector for Wireshark
local enocean = Proto("enocean", "EnOcean ESP3")

-- Direction markers (from bidirectional capture)
local direction_types = {
    [0x00] = "RX",  -- Data received from serial (device -> host)
    [0x01] = "TX",  -- Data sent to serial (host -> device)
}

-- Packet Types
local packet_types = {
    [1] = "RADIO_ERP1",
    [2] = "RESPONSE",
    [3] = "RADIO_SUB_TEL",
    [4] = "EVENT",
    [5] = "COMMON_COMMAND",
    [6] = "SMART_ACK_COMMAND",
    [7] = "REMOTE_MAN_COMMAND",
    [9] = "RADIO_MESSAGE",
    [10] = "RADIO_ERP2",
    [16] = "RADIO_802_15_4",
    [17] = "COMMAND_2_4",
}

-- RORG types (Radio telegram types)
local rorg_types = {
    [0x00] = "RPS (Repeated Switch Communication)",
    [0xF6] = "RPS (Rocker Switch)",
    [0xD5] = "1BS (1 Byte Communication)",
    [0xA5] = "4BS (4 Byte Communication)",
    [0xD2] = "VLD (Variable Length Data)",
    [0xD4] = "UTE (Universal Teach-In)",
    [0xD1] = "MSC (Manufacturer Specific Communication)",
    [0xA6] = "ADT (Addressed Telegram)",
    [0xC5] = "SYS_EX (Smart Ack Extended)",
    [0xC6] = "SM_LRN_REQ (Smart Ack Learn Request)",
    [0xC7] = "SM_LRN_ANS (Smart Ack Learn Answer)",
    [0x30] = "SEC (Secure Telegram)",
    [0x31] = "SEC_ENCAPS (Secure Telegram with Encapsulation)",
    [0x35] = "SEC_TI (Secure Teach-In)",
    [0x40] = "GP_TI (GP Teach-In Request)",
    [0x41] = "GP_TR (GP Teach-In Response)",
    [0x33] = "SEC_CDM (Chained Data Message)",
    [0xB0] = "GP_SD (GP Selective Data)",
    [0xB1] = "GP_CD (GP Complete Data)",
}

-- Return codes for RESPONSE packets
local return_codes = {
    [0x00] = "OK",
    [0x01] = "ERROR",
    [0x02] = "NOT_SUPPORTED",
    [0x03] = "WRONG_PARAM",
    [0x04] = "OPERATION_DENIED",
    [0x05] = "LOCK_SET",
    [0x82] = "FLASH_HW_ERROR",
    [0x90] = "BASEID_OUT_OF_RANGE",
    [0x91] = "BASEID_MAX_REACHED",
}

-- Event codes
local event_codes = {
    [0x01] = "SA_RECLAIM_NOT_SUCCESSFUL",
    [0x02] = "SA_CONFIRM_LEARN",
    [0x03] = "SA_LEARN_ACK",
    [0x04] = "CO_READY",
    [0x05] = "CO_EVENT_SECUREDEVICES",
    [0x06] = "CO_DUTYCYCLE_LIMIT",
    [0x07] = "CO_TRANSMIT_FAILED",
    [0x08] = "CO_TX_DONE",
    [0x09] = "CO_LRN_MODE_DISABLED",
}

-- D2-01 VLD Commands (Electronic Switches/Dimmers with Energy Measurement)
local d2_01_commands = {
    [0x01] = "Actuator Set Output",
    [0x02] = "Actuator Set Local",
    [0x03] = "Actuator Status Query",
    [0x04] = "Actuator Status Response",
    [0x05] = "Actuator Set Measurement",
    [0x06] = "Actuator Measurement Query",
    [0x07] = "Actuator Measurement Response",
    [0x08] = "Actuator Set Pilot Wire Mode",
    [0x09] = "Actuator Pilot Wire Mode Query",
    [0x0A] = "Actuator Pilot Wire Mode Response",
    [0x0B] = "Actuator Set External Interface Settings",
    [0x0C] = "Actuator External Interface Settings Query",
    [0x0D] = "Actuator External Interface Settings Response",
}

-- D2-01 Measurement Units
local d2_01_units = {
    [0x00] = "Energy (Ws)",
    [0x01] = "Energy (Wh)",
    [0x02] = "Energy (kWh)",
    [0x03] = "Power (W)",
    [0x04] = "Power (kW)",
}

-- D2-01 Error Levels
local d2_01_error_level = {
    [0x00] = "Hardware OK",
    [0x01] = "Hardware warning",
    [0x02] = "Hardware failure",
    [0x03] = "Reserved",
}

-- Common command codes
local common_commands = {
    [0x01] = "CO_WR_SLEEP",
    [0x02] = "CO_WR_RESET",
    [0x03] = "CO_RD_VERSION",
    [0x04] = "CO_RD_SYS_LOG",
    [0x05] = "CO_WR_SYS_LOG",
    [0x06] = "CO_WR_BIST",
    [0x07] = "CO_WR_IDBASE",
    [0x08] = "CO_RD_IDBASE",
    [0x09] = "CO_WR_REPEATER",
    [0x0A] = "CO_RD_REPEATER",
    [0x0B] = "CO_WR_FILTER_ADD",
    [0x0C] = "CO_WR_FILTER_DEL",
    [0x0D] = "CO_WR_FILTER_DEL_ALL",
    [0x0E] = "CO_WR_FILTER_ENABLE",
    [0x0F] = "CO_RD_FILTER",
    [0x10] = "CO_WR_WAIT_MATURITY",
    [0x11] = "CO_WR_SUBTEL",
    [0x12] = "CO_WR_MEM",
    [0x13] = "CO_RD_MEM",
    [0x14] = "CO_RD_MEM_ADDRESS",
    [0x15] = "CO_RD_SECURITY",
    [0x16] = "CO_WR_SECURITY",
    [0x17] = "CO_WR_LEARNMODE",
    [0x18] = "CO_RD_LEARNMODE",
    [0x19] = "CO_WR_SECUREDEVICE_ADD",
    [0x1A] = "CO_WR_SECUREDEVICE_DEL",
    [0x1B] = "CO_RD_SECUREDEVICE_BY_INDEX",
    [0x1C] = "CO_WR_MODE",
    [0x1D] = "CO_RD_NUMSECUREDEVICES",
    [0x1E] = "CO_RD_SECUREDEVICE_BY_ID",
    [0x1F] = "CO_WR_SECUREDEVICE_ADD_PSK",
    [0x20] = "CO_WR_SECUREDEVICE_SENDTEACHIN",
    [0x21] = "CO_WR_TEMPORARY_RLC_WINDOW",
    [0x22] = "CO_RD_SECUREDEVICE_PSK",
    [0x23] = "CO_RD_DUTYCYCLE_LIMIT",
    [0x25] = "CO_SET_BAUDRATE",
    [0x26] = "CO_GET_FREQUENCY_INFO",
    [0x27] = "CO_GET_STEPCODE",
    [0x32] = "CO_WR_REMAN_CODE",
    [0x33] = "CO_WR_STARTUP_DELAY",
    [0x34] = "CO_WR_REMAN_REPEATING",
    [0x35] = "CO_RD_REMAN_REPEATING",
    [0x36] = "CO_SET_NOISETHRESHOLD",
    [0x37] = "CO_GET_NOISETHRESHOLD",
}

-- Direction field (for bidirectional capture)
local f_direction = ProtoField.uint8("enocean.direction", "Direction", base.HEX, direction_types)

-- ESP3 Header fields
local f_sync = ProtoField.uint8("enocean.sync", "Sync Byte", base.HEX)
local f_data_len = ProtoField.uint16("enocean.data_len", "Data Length", base.DEC)
local f_opt_len = ProtoField.uint8("enocean.opt_len", "Optional Length", base.DEC)
local f_type = ProtoField.uint8("enocean.type", "Packet Type", base.DEC, packet_types)
local f_crc8h = ProtoField.uint8("enocean.crc8h", "Header CRC8", base.HEX)
local f_data = ProtoField.bytes("enocean.data", "Data")
local f_opt = ProtoField.bytes("enocean.optional", "Optional Data")
local f_crc8d = ProtoField.uint8("enocean.crc8d", "Data CRC8", base.HEX)

-- RADIO_ERP1 fields
local f_rorg = ProtoField.uint8("enocean.rorg", "R-ORG (Telegram Type)", base.HEX, rorg_types)
local f_erp1_data = ProtoField.bytes("enocean.erp1.data", "ERP1 Data Payload")
local f_sender_id = ProtoField.bytes("enocean.sender_id", "Sender ID")
local f_sender_id_str = ProtoField.string("enocean.sender_id_str", "Sender ID")
local f_status = ProtoField.uint8("enocean.status", "Status", base.HEX)
local f_status_rp_counter = ProtoField.uint8("enocean.status.rp_counter", "Repeater Counter", base.DEC, nil, 0x0F)
local f_status_t21 = ProtoField.bool("enocean.status.t21", "T21 (EEP Table)", 8, nil, 0x20)
local f_status_nu = ProtoField.bool("enocean.status.nu", "NU (N-Message)", 8, nil, 0x10)

-- RADIO_ERP1 Optional Data fields
local f_subtel_num = ProtoField.uint8("enocean.subtel_num", "SubTelNum (# of subtelegrams)", base.DEC)
local f_dest_id = ProtoField.bytes("enocean.dest_id", "Destination ID")
local f_dest_id_str = ProtoField.string("enocean.dest_id_str", "Destination ID")
local f_dbm = ProtoField.uint8("enocean.dbm", "dBm", base.DEC)
local f_rssi = ProtoField.int8("enocean.rssi", "RSSI", base.DEC)
local f_sec_level = ProtoField.uint8("enocean.sec_level", "Security Level", base.DEC)

-- RPS (F6) specific fields
local f_rps_data = ProtoField.uint8("enocean.rps.data", "RPS Data", base.HEX)
local f_rps_rocker_1st = ProtoField.uint8("enocean.rps.rocker_1st", "Rocker 1st Action", base.HEX, nil, 0xE0)
local f_rps_eb = ProtoField.bool("enocean.rps.eb", "Energy Bow", 8, {"Pressed", "Released"}, 0x10)
local f_rps_rocker_2nd = ProtoField.uint8("enocean.rps.rocker_2nd", "Rocker 2nd Action", base.HEX, nil, 0x0E)
local f_rps_sa = ProtoField.bool("enocean.rps.sa", "2nd Action Valid", 8, nil, 0x01)

-- 1BS (D5) specific fields
local f_1bs_data = ProtoField.uint8("enocean.1bs.data", "1BS Data", base.HEX)
local f_1bs_lrn = ProtoField.bool("enocean.1bs.lrn", "Learn Button", 8, {"Not pressed (Data)", "Pressed (Teach-in)"}, 0x08)
local f_1bs_co = ProtoField.bool("enocean.1bs.contact", "Contact", 8, {"Open", "Closed"}, 0x01)

-- 4BS (A5) specific fields  
local f_4bs_data = ProtoField.bytes("enocean.4bs.data", "4BS Data")
local f_4bs_db3 = ProtoField.uint8("enocean.4bs.db3", "Data Byte 3 (DB3)", base.HEX)
local f_4bs_db2 = ProtoField.uint8("enocean.4bs.db2", "Data Byte 2 (DB2)", base.HEX)
local f_4bs_db1 = ProtoField.uint8("enocean.4bs.db1", "Data Byte 1 (DB1)", base.HEX)
local f_4bs_db0 = ProtoField.uint8("enocean.4bs.db0", "Data Byte 0 (DB0)", base.HEX)
local f_4bs_lrn = ProtoField.bool("enocean.4bs.lrn", "Learn Button", 8, {"Not pressed (Data)", "Pressed (Teach-in)"}, 0x08)

-- VLD (D2) specific fields
local f_vld_data = ProtoField.bytes("enocean.vld.data", "VLD Data")

-- D2-01 VLD fields (Electronic Switches/Dimmers)
local f_d2_01_cmd = ProtoField.uint8("enocean.d2_01.cmd", "Command", base.HEX, d2_01_commands, 0x0F)
local f_d2_01_io_channel = ProtoField.uint8("enocean.d2_01.io_channel", "I/O Channel", base.DEC, nil, 0x1F)
local f_d2_01_output_value = ProtoField.uint8("enocean.d2_01.output_value", "Output Value", base.DEC)
local f_d2_01_dim_value = ProtoField.uint8("enocean.d2_01.dim_value", "Dim Value (%)", base.DEC)

-- D2-01 Status Response fields (CMD 0x04)
local f_d2_01_pf = ProtoField.bool("enocean.d2_01.pf", "Power Failure", 8, {"Power failure detected", "Power OK"}, 0x80)
local f_d2_01_pfd = ProtoField.bool("enocean.d2_01.pfd", "Power Failure Detection", 8, {"Supported", "Not supported"}, 0x40)
local f_d2_01_oc = ProtoField.bool("enocean.d2_01.oc", "Over Current Shut Down", 8, {"Occurred", "OK"}, 0x20)
local f_d2_01_el = ProtoField.uint8("enocean.d2_01.el", "Error Level", base.DEC, d2_01_error_level, 0x18)
local f_d2_01_lc = ProtoField.bool("enocean.d2_01.lc", "Local Control", 8, {"Enabled", "Disabled"}, 0x04)

-- D2-01 Measurement Response fields (CMD 0x07)
local f_d2_01_unit = ProtoField.uint8("enocean.d2_01.unit", "Measurement Unit", base.DEC, d2_01_units, 0x07)
local f_d2_01_meas_value = ProtoField.uint32("enocean.d2_01.meas_value", "Measurement Value", base.DEC)

-- UTE (D4) Teach-In fields
local f_ute_cmd = ProtoField.uint8("enocean.ute.cmd", "UTE Command", base.HEX)
local f_ute_channels = ProtoField.uint8("enocean.ute.channels", "Number of Channels", base.DEC)
local f_ute_manuf = ProtoField.uint16("enocean.ute.manuf", "Manufacturer ID", base.HEX)
local f_ute_type = ProtoField.uint8("enocean.ute.type", "Type", base.HEX)
local f_ute_func = ProtoField.uint8("enocean.ute.func", "Func", base.HEX)
local f_ute_rorg = ProtoField.uint8("enocean.ute.rorg", "RORG", base.HEX, rorg_types)

-- RESPONSE fields
local f_return_code = ProtoField.uint8("enocean.return_code", "Return Code", base.HEX, return_codes)
local f_response_data = ProtoField.bytes("enocean.response.data", "Response Data")

-- EVENT fields
local f_event_code = ProtoField.uint8("enocean.event_code", "Event Code", base.HEX, event_codes)
local f_event_data = ProtoField.bytes("enocean.event.data", "Event Data")

-- COMMON_COMMAND fields
local f_cmd_code = ProtoField.uint8("enocean.cmd_code", "Command Code", base.HEX, common_commands)
local f_cmd_data = ProtoField.bytes("enocean.cmd.data", "Command Data")

enocean.fields = { 
    f_direction,
    f_sync, f_data_len, f_opt_len, f_type, f_crc8h, f_data, f_opt, f_crc8d,
    f_rorg, f_erp1_data, f_sender_id, f_sender_id_str, f_status, 
    f_status_rp_counter, f_status_t21, f_status_nu,
    f_subtel_num, f_dest_id, f_dest_id_str, f_dbm, f_rssi, f_sec_level,
    f_rps_data, f_rps_rocker_1st, f_rps_eb, f_rps_rocker_2nd, f_rps_sa,
    f_1bs_data, f_1bs_lrn, f_1bs_co,
    f_4bs_data, f_4bs_db3, f_4bs_db2, f_4bs_db1, f_4bs_db0, f_4bs_lrn,
    f_vld_data,
    f_d2_01_cmd, f_d2_01_io_channel, f_d2_01_output_value, f_d2_01_dim_value,
    f_d2_01_pf, f_d2_01_pfd, f_d2_01_oc, f_d2_01_el, f_d2_01_lc,
    f_d2_01_unit, f_d2_01_meas_value,
    f_ute_cmd, f_ute_channels, f_ute_manuf, f_ute_type, f_ute_func, f_ute_rorg,
    f_return_code, f_response_data,
    f_event_code, f_event_data,
    f_cmd_code, f_cmd_data,
}

-- Helper function to format bytes as ID string (XX:XX:XX:XX)
local function format_id(buffer, offset, len)
    local parts = {}
    for i = 0, len - 1 do
        parts[#parts + 1] = string.format("%02X", buffer(offset + i, 1):uint())
    end
    return table.concat(parts, ":")
end

-- Dissect RADIO_ERP1 data payload
local function dissect_radio_erp1(buffer, pinfo, subtree, data_offset, data_len, opt_offset, opt_len, info_prefix)
    info_prefix = info_prefix or ""
    if data_len < 6 then return end  -- Minimum: RORG(1) + Data(0) + Sender(4) + Status(1)
    
    local data_tree = subtree:add(buffer(data_offset, data_len), "RADIO_ERP1 Data")
    
    -- RORG (telegram type)
    local rorg = buffer(data_offset, 1):uint()
    data_tree:add(f_rorg, buffer(data_offset, 1))
    
    -- Calculate payload length: data_len - RORG(1) - SenderID(4) - Status(1)
    local payload_len = data_len - 6
    local payload_offset = data_offset + 1
    local sender_offset = data_offset + 1 + payload_len
    local status_offset = sender_offset + 4
    
    -- Parse RORG-specific data
    local rorg_name = rorg_types[rorg] or "Unknown"
    
    if rorg == 0xF6 then  -- RPS
        if payload_len >= 1 then
            local rps_tree = data_tree:add(buffer(payload_offset, 1), "RPS Telegram")
            rps_tree:add(f_rps_data, buffer(payload_offset, 1))
            rps_tree:add(f_rps_rocker_1st, buffer(payload_offset, 1))
            rps_tree:add(f_rps_eb, buffer(payload_offset, 1))
            rps_tree:add(f_rps_rocker_2nd, buffer(payload_offset, 1))
            rps_tree:add(f_rps_sa, buffer(payload_offset, 1))
        end
    elseif rorg == 0xD5 then  -- 1BS
        if payload_len >= 1 then
            local bs1_tree = data_tree:add(buffer(payload_offset, 1), "1BS Telegram")
            bs1_tree:add(f_1bs_data, buffer(payload_offset, 1))
            bs1_tree:add(f_1bs_lrn, buffer(payload_offset, 1))
            bs1_tree:add(f_1bs_co, buffer(payload_offset, 1))
        end
    elseif rorg == 0xA5 then  -- 4BS
        if payload_len >= 4 then
            local bs4_tree = data_tree:add(buffer(payload_offset, 4), "4BS Telegram")
            bs4_tree:add(f_4bs_db3, buffer(payload_offset, 1))
            bs4_tree:add(f_4bs_db2, buffer(payload_offset + 1, 1))
            bs4_tree:add(f_4bs_db1, buffer(payload_offset + 2, 1))
            bs4_tree:add(f_4bs_db0, buffer(payload_offset + 3, 1))
            bs4_tree:add(f_4bs_lrn, buffer(payload_offset + 3, 1))
        end
    elseif rorg == 0xD2 then  -- VLD
        if payload_len > 0 then
            local vld_tree = data_tree:add(buffer(payload_offset, payload_len), "VLD Telegram")
            vld_tree:add(f_vld_data, buffer(payload_offset, payload_len))
            
            -- Try to parse D2-01 (Electronic Switches/Dimmers)
            local cmd = bit.band(buffer(payload_offset, 1):uint(), 0x0F)
            local cmd_name = d2_01_commands[cmd]
            
            if cmd_name then
                local d2_tree = vld_tree:add(buffer(payload_offset, payload_len), "D2-01 Electronic Switch/Dimmer")
                d2_tree:add(f_d2_01_cmd, buffer(payload_offset, 1))
                
                if cmd == 0x01 then  -- Actuator Set Output
                    if payload_len >= 3 then
                        d2_tree:add(f_d2_01_io_channel, buffer(payload_offset + 1, 1))
                        local out_val = buffer(payload_offset + 2, 1):uint()
                        local out_item = d2_tree:add(f_d2_01_output_value, buffer(payload_offset + 2, 1))
                        if out_val == 0 then out_item:append_text(" (Off)")
                        elseif out_val == 100 then out_item:append_text(" (On)")
                        elseif out_val == 127 then out_item:append_text(" (Keep current)")
                        else out_item:append_text(string.format(" (%d%%)", out_val))
                        end
                    end
                    
                elseif cmd == 0x03 then  -- Actuator Status Query
                    if payload_len >= 2 then
                        d2_tree:add(f_d2_01_io_channel, buffer(payload_offset + 1, 1))
                    end
                    
                elseif cmd == 0x04 then  -- Actuator Status Response
                    if payload_len >= 3 then
                        d2_tree:add(f_d2_01_io_channel, buffer(payload_offset + 1, 1))
                        local out_val = buffer(payload_offset + 2, 1):uint()
                        local out_item = d2_tree:add(f_d2_01_output_value, buffer(payload_offset + 2, 1))
                        if out_val == 0 then out_item:append_text(" (Off)")
                        elseif out_val == 100 then out_item:append_text(" (On)")
                        elseif out_val == 127 then out_item:append_text(" (Not valid/not set)")
                        else out_item:append_text(string.format(" (%d%%)", out_val))
                        end
                        
                        if payload_len >= 4 then
                            local status_byte = buffer(payload_offset + 3, 1):uint()
                            local status_tree = d2_tree:add(buffer(payload_offset + 3, 1), "Status Flags")
                            status_tree:add(f_d2_01_pf, buffer(payload_offset + 3, 1))
                            status_tree:add(f_d2_01_pfd, buffer(payload_offset + 3, 1))
                            status_tree:add(f_d2_01_oc, buffer(payload_offset + 3, 1))
                            status_tree:add(f_d2_01_el, buffer(payload_offset + 3, 1))
                            status_tree:add(f_d2_01_lc, buffer(payload_offset + 3, 1))
                        end
                    end
                    
                elseif cmd == 0x06 then  -- Actuator Measurement Query
                    if payload_len >= 2 then
                        d2_tree:add(f_d2_01_io_channel, buffer(payload_offset + 1, 1))
                    end
                    
                elseif cmd == 0x07 then  -- Actuator Measurement Response
                    if payload_len >= 6 then
                        d2_tree:add(f_d2_01_io_channel, buffer(payload_offset + 1, 1))
                        d2_tree:add(f_d2_01_unit, buffer(payload_offset + 1, 1))
                        
                        -- 4-byte measurement value (big-endian)
                        local meas_val = buffer(payload_offset + 2, 4):uint()
                        local meas_item = d2_tree:add(f_d2_01_meas_value, buffer(payload_offset + 2, 4))
                        
                        local unit = bit.band(buffer(payload_offset + 1, 1):uint(), 0x07)
                        local unit_str = ""
                        if unit == 0x00 then unit_str = " Ws"
                        elseif unit == 0x01 then unit_str = " Wh"
                        elseif unit == 0x02 then unit_str = " kWh"
                        elseif unit == 0x03 then unit_str = " W"
                        elseif unit == 0x04 then unit_str = " kW"
                        end
                        meas_item:append_text(unit_str)
                        
                        -- Show in info column
                        if unit == 0x03 or unit == 0x04 then
                            pinfo.cols.info:append(string.format(" [Power: %d%s]", meas_val, unit_str))
                        else
                            pinfo.cols.info:append(string.format(" [Energy: %d%s]", meas_val, unit_str))
                        end
                    end
                end
            end
        end
    elseif rorg == 0xD4 then  -- UTE
        if payload_len >= 7 then
            local ute_tree = data_tree:add(buffer(payload_offset, payload_len), "UTE Teach-In")
            ute_tree:add(f_ute_cmd, buffer(payload_offset, 1))
            ute_tree:add(f_ute_channels, buffer(payload_offset + 1, 1))
            ute_tree:add(f_ute_manuf, buffer(payload_offset + 2, 2))
            ute_tree:add(f_ute_type, buffer(payload_offset + 4, 1))
            ute_tree:add(f_ute_func, buffer(payload_offset + 5, 1))
            ute_tree:add(f_ute_rorg, buffer(payload_offset + 6, 1))
        end
    else
        -- Generic payload display for other RORGs
        if payload_len > 0 then
            data_tree:add(f_erp1_data, buffer(payload_offset, payload_len))
        end
    end
    
    -- Sender ID (4 bytes)
    local sender_id = format_id(buffer, sender_offset, 4)
    local sender_item = data_tree:add(f_sender_id, buffer(sender_offset, 4))
    sender_item:append_text(" (" .. sender_id .. ")")
    
    -- Status byte
    local status_tree = data_tree:add(buffer(status_offset, 1), "Status")
    status_tree:add(f_status, buffer(status_offset, 1))
    status_tree:add(f_status_rp_counter, buffer(status_offset, 1))
    status_tree:add(f_status_t21, buffer(status_offset, 1))
    status_tree:add(f_status_nu, buffer(status_offset, 1))
    
    -- Update info column
    pinfo.cols.info = info_prefix .. string.format("RADIO_ERP1 %s from %s", rorg_name:match("^(%S+)") or string.format("0x%02X", rorg), sender_id)
    
    -- Parse optional data (7 bytes for ERP1: SubTelNum + DestID + dBm + SecurityLevel)
    if opt_len >= 7 then
        local opt_tree = subtree:add(buffer(opt_offset, opt_len), "RADIO_ERP1 Optional Data")
        opt_tree:add(f_subtel_num, buffer(opt_offset, 1))
        
        local dest_id = format_id(buffer, opt_offset + 1, 4)
        local dest_item = opt_tree:add(f_dest_id, buffer(opt_offset + 1, 4))
        dest_item:append_text(" (" .. dest_id .. ")")
        
        local dbm_value = buffer(opt_offset + 5, 1):uint()
        local rssi = -dbm_value
        local dbm_item = opt_tree:add(f_dbm, buffer(opt_offset + 5, 1))
        dbm_item:append_text(string.format(" (RSSI: %d dBm)", rssi))
        
        opt_tree:add(f_sec_level, buffer(opt_offset + 6, 1))
        
        return true  -- Indicate we handled optional data
    end
    
    return false
end

-- Dissect RESPONSE packet
local function dissect_response(buffer, pinfo, subtree, data_offset, data_len, info_prefix)
    info_prefix = info_prefix or ""
    if data_len < 1 then return end
    
    local resp_tree = subtree:add(buffer(data_offset, data_len), "RESPONSE Data")
    local ret_code = buffer(data_offset, 1):uint()
    resp_tree:add(f_return_code, buffer(data_offset, 1))
    
    if data_len > 1 then
        resp_tree:add(f_response_data, buffer(data_offset + 1, data_len - 1))
    end
    
    local ret_name = return_codes[ret_code] or string.format("0x%02X", ret_code)
    pinfo.cols.info = info_prefix .. string.format("RESPONSE: %s", ret_name)
end

-- Dissect EVENT packet
local function dissect_event(buffer, pinfo, subtree, data_offset, data_len, info_prefix)
    info_prefix = info_prefix or ""
    if data_len < 1 then return end
    
    local event_tree = subtree:add(buffer(data_offset, data_len), "EVENT Data")
    local evt_code = buffer(data_offset, 1):uint()
    event_tree:add(f_event_code, buffer(data_offset, 1))
    
    if data_len > 1 then
        event_tree:add(f_event_data, buffer(data_offset + 1, data_len - 1))
    end
    
    local evt_name = event_codes[evt_code] or string.format("0x%02X", evt_code)
    pinfo.cols.info = info_prefix .. string.format("EVENT: %s", evt_name)
end

-- Dissect COMMON_COMMAND packet
local function dissect_common_command(buffer, pinfo, subtree, data_offset, data_len, info_prefix)
    info_prefix = info_prefix or ""
    if data_len < 1 then return end
    
    local cmd_tree = subtree:add(buffer(data_offset, data_len), "COMMON_COMMAND Data")
    local cmd_code = buffer(data_offset, 1):uint()
    cmd_tree:add(f_cmd_code, buffer(data_offset, 1))
    
    if data_len > 1 then
        cmd_tree:add(f_cmd_data, buffer(data_offset + 1, data_len - 1))
    end
    
    local cmd_name = common_commands[cmd_code] or string.format("0x%02X", cmd_code)
    pinfo.cols.info = info_prefix .. string.format("COMMAND: %s", cmd_name)
end

function enocean.dissector(buffer, pinfo, tree)
    if buffer:len() < 7 then return end
    
    pinfo.cols.protocol = "EnOcean"
    
    -- Check for bidirectional capture (direction byte prefix)
    -- Direction byte: 0x00 (RX) or 0x01 (TX), followed by sync byte 0x55
    local base_offset = 0
    local direction = nil
    local direction_str = ""
    
    local first_byte = buffer(0, 1):uint()
    if buffer:len() >= 8 and (first_byte == 0x00 or first_byte == 0x01) then
        local second_byte = buffer(1, 1):uint()
        if second_byte == 0x55 then
            -- Bidirectional capture mode
            direction = first_byte
            direction_str = direction_types[direction] or "?"
            base_offset = 1
        end
    end
    
    local subtree = tree:add(enocean, buffer(), "EnOcean ESP3 Packet")
    
    -- Direction field (if present)
    if direction ~= nil then
        local dir_item = subtree:add(f_direction, buffer(0, 1))
        if direction == 0x00 then
            dir_item:append_text(" (Received from device)")
        else
            dir_item:append_text(" (Sent to device)")
        end
    end
    
    -- Header
    local header_tree = subtree:add(buffer(base_offset, 6), "ESP3 Header")
    header_tree:add(f_sync, buffer(base_offset + 0, 1))
    header_tree:add(f_data_len, buffer(base_offset + 1, 2))
    header_tree:add(f_opt_len, buffer(base_offset + 3, 1))
    header_tree:add(f_type, buffer(base_offset + 4, 1))
    header_tree:add(f_crc8h, buffer(base_offset + 5, 1))
    
    local data_len = buffer(base_offset + 1, 2):uint()
    local opt_len = buffer(base_offset + 3, 1):uint()
    local pkt_type = buffer(base_offset + 4, 1):uint()
    
    -- Build info column with direction prefix if present
    local info_prefix = ""
    if direction ~= nil then
        info_prefix = "[" .. direction_str .. "] "
    end
    pinfo.cols.info = info_prefix .. (packet_types[pkt_type] or ("Type " .. pkt_type))
    
    local data_offset = base_offset + 6
    local opt_offset = data_offset + data_len
    local crc_offset = opt_offset + opt_len
    
    local opt_handled = false
    
    -- Type-specific dissection
    if pkt_type == 1 then  -- RADIO_ERP1
        if data_len > 0 and buffer:len() >= data_offset + data_len then
            opt_handled = dissect_radio_erp1(buffer, pinfo, subtree, data_offset, data_len, opt_offset, opt_len, info_prefix)
        end
    elseif pkt_type == 2 then  -- RESPONSE
        if data_len > 0 and buffer:len() >= data_offset + data_len then
            dissect_response(buffer, pinfo, subtree, data_offset, data_len, info_prefix)
        end
    elseif pkt_type == 4 then  -- EVENT
        if data_len > 0 and buffer:len() >= data_offset + data_len then
            dissect_event(buffer, pinfo, subtree, data_offset, data_len, info_prefix)
        end
    elseif pkt_type == 5 then  -- COMMON_COMMAND
        if data_len > 0 and buffer:len() >= data_offset + data_len then
            dissect_common_command(buffer, pinfo, subtree, data_offset, data_len, info_prefix)
        end
    else
        -- Generic data display for other packet types
        if data_len > 0 and buffer:len() >= data_offset + data_len then
            subtree:add(f_data, buffer(data_offset, data_len))
        end
    end
    
    -- Optional data (if not already handled)
    if not opt_handled and opt_len > 0 and buffer:len() >= opt_offset + opt_len then
        subtree:add(f_opt, buffer(opt_offset, opt_len))
    end

    -- Data CRC8
    if buffer:len() >= crc_offset + 1 then
        subtree:add(f_crc8d, buffer(crc_offset, 1))
    end
end

DissectorTable.get("wtap_encap"):add(147, enocean)

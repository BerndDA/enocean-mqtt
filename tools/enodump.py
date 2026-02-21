#!/usr/bin/env python3
"""
Capture EnOcean ESP3 packets to PCAP format for Wireshark.
Supports both legacy (RX only) and bidirectional capture modes.
"""
import sys
import struct
import time
import socket
import argparse

# PCAP global header (linktype 147 = DLT_USER0)
PCAP_GLOBAL_HEADER = struct.pack('<IHHiIII',
    0xa1b2c3d4,  # magic
    2, 4,         # version
    0,            # timezone
    0,            # sigfigs
    65535,        # snaplen
    147           # DLT_USER0
)

# Direction markers (must match serial_mux.py)
DIR_RX = 0x00  # Data received from serial (device -> host)
DIR_TX = 0x01  # Data sent to serial (host -> device)

def write_pcap_packet(data, direction=None):
    """Write a PCAP packet. If direction is specified, prepend a direction byte."""
    ts = time.time()
    sec = int(ts)
    usec = int((ts - sec) * 1_000_000)
    if direction is not None:
        # Prepend direction byte to packet data for Wireshark dissection
        data = bytes([direction]) + data
    header = struct.pack('<IIII', sec, usec, len(data), len(data))
    sys.stdout.buffer.write(header)
    sys.stdout.buffer.write(data)
    sys.stdout.buffer.flush()

def process_esp3_packets(buf, direction=None):
    """Extract complete ESP3 packets from buffer. Returns (packets, remaining_buffer)."""
    packets = []
    # ESP3 sync byte is 0x55
    while b'\x55' in buf:
        idx = buf.index(0x55)
        if idx > 0:
            buf = buf[idx:]  # discard before sync
        if len(buf) < 6:
            break  # need more data for header
        data_len = (buf[1] << 8) | buf[2]
        opt_len = buf[3]
        packet_len = 6 + data_len + opt_len + 1  # header + data + optional + crc
        if len(buf) < packet_len:
            break
        packet = bytes(buf[:packet_len])
        packets.append((packet, direction))
        buf = buf[packet_len:]
    return packets, buf

def capture_raw(host, port):
    """Capture from raw TCP port (RX only, legacy mode)."""
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.connect((host, port))
    sock.settimeout(1.0)

    sys.stdout.buffer.write(PCAP_GLOBAL_HEADER)
    sys.stdout.buffer.flush()

    buf = bytearray()
    while True:
        try:
            chunk = sock.recv(1024)
            if not chunk:
                break  # connection closed
        except socket.timeout:
            continue
        buf.extend(chunk)
        packets, buf = process_esp3_packets(buf)
        for packet, _ in packets:
            write_pcap_packet(packet)

def capture_bidirectional(host, port):
    """Capture from capture port (RX + TX with direction markers)."""
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.connect((host, port))
    sock.settimeout(1.0)

    sys.stdout.buffer.write(PCAP_GLOBAL_HEADER)
    sys.stdout.buffer.flush()

    # Separate buffers for RX and TX to handle partial ESP3 packets
    rx_buf = bytearray()
    tx_buf = bytearray()
    frame_buf = bytearray()

    while True:
        try:
            chunk = sock.recv(1024)
            if not chunk:
                break  # connection closed
        except socket.timeout:
            continue
        
        frame_buf.extend(chunk)
        
        # Parse framed data: [direction:1][length:2][data:N]
        while len(frame_buf) >= 3:
            direction = frame_buf[0]
            data_len = (frame_buf[1] << 8) | frame_buf[2]
            frame_len = 3 + data_len
            if len(frame_buf) < frame_len:
                break  # need more data
            
            data = bytes(frame_buf[3:frame_len])
            frame_buf = frame_buf[frame_len:]
            
            # Add to appropriate direction buffer and process
            if direction == DIR_RX:
                rx_buf.extend(data)
                packets, rx_buf = process_esp3_packets(rx_buf, DIR_RX)
            else:
                tx_buf.extend(data)
                packets, tx_buf = process_esp3_packets(tx_buf, DIR_TX)
            
            for packet, pkt_dir in packets:
                write_pcap_packet(packet, pkt_dir)

def main():
    parser = argparse.ArgumentParser(description='Capture EnOcean ESP3 packets to PCAP')
    parser.add_argument('--host', default='127.0.0.1', help='TCP host (default: 127.0.0.1)')
    parser.add_argument('--port', type=int, default=2000, help='TCP port (default: 2000)')
    parser.add_argument('--bidirectional', '-b', action='store_true',
                        help='Enable bidirectional capture (connect to capture port)')
    args = parser.parse_args()

    if args.bidirectional:
        capture_bidirectional(args.host, args.port)
    else:
        capture_raw(args.host, args.port)

if __name__ == '__main__':
    main()

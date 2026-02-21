#!/usr/bin/env python3
"""
Serial to TCP multiplexer - broadcasts serial data to all connected TCP clients.
Also provides a capture port for bidirectional packet capture (RX + TX).
"""
import socket
import select
import threading
import argparse
import struct
import serial

# Direction markers for capture port
DIR_RX = 0x00  # Data received from serial (device -> host)
DIR_TX = 0x01  # Data sent to serial (host -> device)

class SerialMultiplexer:
    def __init__(self, serial_port, baud_rate, tcp_port, capture_port=None):
        self.serial_port = serial_port
        self.baud_rate = baud_rate
        self.tcp_port = tcp_port
        self.capture_port = capture_port
        self.clients = []
        self.clients_lock = threading.Lock()
        self.capture_clients = []
        self.capture_clients_lock = threading.Lock()
        self.running = True

    def broadcast(self, data):
        """Send data to all connected clients."""
        with self.clients_lock:
            dead = []
            for client in self.clients:
                try:
                    client.sendall(data)
                except (BrokenPipeError, ConnectionResetError, OSError):
                    dead.append(client)
            for client in dead:
                self.clients.remove(client)
                try:
                    client.close()
                except:
                    pass

    def broadcast_capture(self, direction, data):
        """Send framed data to all capture clients.
        
        Frame format: [direction:1][length:2][data:N]
        - direction: 0x00 = RX (from serial), 0x01 = TX (to serial)
        - length: 16-bit big-endian length of data
        """
        frame = struct.pack('>BH', direction, len(data)) + data
        with self.capture_clients_lock:
            dead = []
            for client in self.capture_clients:
                try:
                    client.sendall(frame)
                except (BrokenPipeError, ConnectionResetError, OSError):
                    dead.append(client)
            for client in dead:
                self.capture_clients.remove(client)
                try:
                    client.close()
                except:
                    pass

    def handle_client_writes(self, client, ser):
        """Handle writes from a client to the serial port."""
        try:
            while self.running:
                ready, _, _ = select.select([client], [], [], 1.0)
                if ready:
                    data = client.recv(1024)
                    if not data:
                        break
                    ser.write(data)
                    # Broadcast TX data to capture clients
                    self.broadcast_capture(DIR_TX, data)
        except:
            pass
        finally:
            with self.clients_lock:
                if client in self.clients:
                    self.clients.remove(client)
            try:
                client.close()
            except:
                pass

    def accept_capture_clients(self, capture_server):
        """Thread to accept connections on the capture port."""
        while self.running:
            try:
                client, addr = capture_server.accept()
                print(f"New capture connection from {addr}")
                with self.capture_clients_lock:
                    self.capture_clients.append(client)
            except BlockingIOError:
                pass
            except:
                if self.running:
                    pass

    def run(self):
        # Open serial port
        ser = serial.Serial(self.serial_port, self.baud_rate, timeout=0.1)
        print(f"Opened serial port {self.serial_port} at {self.baud_rate} baud")

        # Start TCP server
        server = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        server.bind(('0.0.0.0', self.tcp_port))
        server.listen(10)
        server.setblocking(False)
        print(f"Listening on TCP port {self.tcp_port}")

        # Start capture server if enabled
        capture_server = None
        capture_thread = None
        if self.capture_port:
            capture_server = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            capture_server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            capture_server.bind(('0.0.0.0', self.capture_port))
            capture_server.listen(10)
            capture_server.setblocking(False)
            capture_server.settimeout(1.0)
            print(f"Capture port listening on TCP port {self.capture_port}")
            capture_thread = threading.Thread(target=self.accept_capture_clients, args=(capture_server,), daemon=True)
            capture_thread.start()

        try:
            while self.running:
                # Accept new connections
                try:
                    client, addr = server.accept()
                    print(f"New connection from {addr}")
                    with self.clients_lock:
                        self.clients.append(client)
                    # Start thread to handle writes from this client
                    t = threading.Thread(target=self.handle_client_writes, args=(client, ser), daemon=True)
                    t.start()
                except BlockingIOError:
                    pass

                # Read from serial and broadcast
                data = ser.read(ser.in_waiting or 1)
                if data:
                    self.broadcast(data)
                    # Broadcast RX data to capture clients
                    self.broadcast_capture(DIR_RX, data)

        except KeyboardInterrupt:
            print("\nShutting down...")
        finally:
            self.running = False
            ser.close()
            server.close()
            if capture_server:
                capture_server.close()
            with self.clients_lock:
                for client in self.clients:
                    try:
                        client.close()
                    except:
                        pass
            with self.capture_clients_lock:
                for client in self.capture_clients:
                    try:
                        client.close()
                    except:
                        pass

def main():
    parser = argparse.ArgumentParser(description='Serial to TCP multiplexer')
    parser.add_argument('--serial', default='/dev/serial/by-id/usb-Prolific_Technology_Inc._USB-Serial_Controller-if00-port0',
                        help='Serial port path')
    parser.add_argument('--baud', type=int, default=57600, help='Baud rate (default: 57600)')
    parser.add_argument('--port', type=int, default=2000, help='TCP port (default: 2000)')
    parser.add_argument('--capture-port', type=int, default=None,
                        help='TCP port for bidirectional capture (optional)')
    args = parser.parse_args()

    mux = SerialMultiplexer(args.serial, args.baud, args.port, args.capture_port)
    mux.run()

if __name__ == '__main__':
    main()

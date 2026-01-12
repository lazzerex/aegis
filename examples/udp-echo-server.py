#!/usr/bin/env python3
"""
Simple UDP echo server for testing Aegis UDP proxy.

This server receives UDP packets and echoes them back with a timestamp
and server identifier.
"""

import argparse
import socket
import json
from datetime import datetime
import sys


def main():
    parser = argparse.ArgumentParser(
        description='Simple UDP echo server for Aegis proxy testing'
    )
    parser.add_argument(
        '--port',
        type=int,
        default=5000,
        help='Port to listen on (default: 5000)'
    )
    parser.add_argument(
        '--name',
        type=str,
        default='udp-backend',
        help='Server name (default: udp-backend)'
    )
    parser.add_argument(
        '--host',
        type=str,
        default='0.0.0.0',
        help='Host to bind to (default: 0.0.0.0)'
    )

    args = parser.parse_args()

    # Create UDP socket
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    sock.bind((args.host, args.port))

    print(f"╔{'═' * 58}╗")
    print(f"║ Aegis UDP Echo Server                                    ║")
    print(f"╠{'═' * 58}╣")
    print(f"║ Name:    {args.name:<48}║")
    print(f"║ Address: {args.host}:{args.port:<43}║")
    print(f"╚{'═' * 58}╝")
    print()
    print("Server is ready to receive UDP packets.")
    print("Press Ctrl+C to stop.\n")

    packet_count = 0

    try:
        while True:
            # Receive data
            data, addr = sock.recvfrom(4096)
            packet_count += 1

            # Log received packet
            timestamp = datetime.utcnow().isoformat() + 'Z'
            print(f"[{timestamp}] [{args.name}] Received {len(data)} bytes from {addr[0]}:{addr[1]}")

            # Decode and print message if it's text
            try:
                message = data.decode('utf-8')
                print(f"  Message: {message[:100]}")
            except UnicodeDecodeError:
                print(f"  Binary data: {data[:50].hex()}...")

            # Create response with metadata
            response_data = {
                'server': args.name,
                'timestamp': timestamp,
                'packet_number': packet_count,
                'received_bytes': len(data),
                'echo': data.decode('utf-8', errors='ignore')
            }

            # Send back JSON response
            response = json.dumps(response_data).encode('utf-8')
            sock.sendto(response, addr)
            print(f"  Echoed {len(response)} bytes back\n")

    except KeyboardInterrupt:
        print("\n\nShutting down server...")
        sock.close()
        print(f"Server stopped. Total packets handled: {packet_count}")
        sys.exit(0)


if __name__ == '__main__':
    main()

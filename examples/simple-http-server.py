import argparse
import json
import time
from http.server import HTTPServer, BaseHTTPRequestHandler
from datetime import datetime


class HealthCheckHandler(BaseHTTPRequestHandler):
    """HTTP handler."""
    
    server_name = "backend"
    
    def do_GET(self):
        """Handle GET requests."""
        if self.path == '/health':
            self._send_health_response()
        elif self.path.startswith('/api/'):
            self._send_api_response()
        else:
            self._send_default_response()
    
    def _send_health_response(self):
        """Send health check response."""
        self.send_response(200)
        self.send_header('Content-type', 'application/json')
        self.end_headers()
        
        response = {
            'status': 'healthy',
            'server': self.server_name,
            'timestamp': datetime.utcnow().isoformat() + 'Z',
            'uptime': time.time() - self.server.start_time
        }
        self.wfile.write(json.dumps(response, indent=2).encode())
    
    def _send_api_response(self):
        """Send API response."""
        self.send_response(200)
        self.send_header('Content-type', 'application/json')
        self.send_header('X-Backend-Server', self.server_name)
        self.end_headers()
        
        response = {
            'message': f'Response from {self.server_name}',
            'path': self.path,
            'method': self.command,
            'server': self.server_name,
            'timestamp': datetime.utcnow().isoformat() + 'Z'
        }
        self.wfile.write(json.dumps(response, indent=2).encode())
    
    def _send_default_response(self):
        """Send default response."""
        self.send_response(200)
        self.send_header('Content-type', 'text/html')
        self.end_headers()
        
        html = f"""<!DOCTYPE html>
<html>
<head>
    <title>{self.server_name}</title>
    <style>
        body {{ font-family: Arial, sans-serif; margin: 50px; }}
        .info {{ background: #f0f0f0; padding: 20px; border-radius: 5px; }}
    </style>
</head>
<body>
    <h1>Aegis Test Backend: {self.server_name}</h1>
    <div class="info">
        <p><strong>Server:</strong> {self.server_name}</p>
        <p><strong>Path:</strong> {self.path}</p>
        <p><strong>Time:</strong> {datetime.utcnow().isoformat()}Z</p>
    </div>
    <h2>Endpoints:</h2>
    <ul>
        <li><a href="/health">/health</a> - Health check endpoint</li>
        <li><a href="/api/test">/api/test</a> - API test endpoint</li>
    </ul>
</body>
</html>"""
        self.wfile.write(html.encode())
    
    def log_message(self, format, *args):
        """Custom log format."""
        timestamp = datetime.utcnow().isoformat()
        print(f"[{timestamp}] [{self.server_name}] {format % args}")


def main():
    parser = argparse.ArgumentParser(
        description='Simple HTTP server for Aegis proxy testing'
    )
    parser.add_argument(
        '--port', 
        type=int, 
        default=3000,
        help='Port to listen on (default: 3000)'
    )
    parser.add_argument(
        '--name', 
        type=str, 
        default='backend',
        help='Server name (default: backend)'
    )
    parser.add_argument(
        '--host',
        type=str,
        default='0.0.0.0',
        help='Host to bind to (default: 0.0.0.0)'
    )
    
    args = parser.parse_args()
    
    HealthCheckHandler.server_name = args.name
    server = HTTPServer((args.host, args.port), HealthCheckHandler)
    server.start_time = time.time()
    
    print(f"╔{'═' * 58}╗")
    print(f"║ Aegis Test Backend Server                               ║")
    print(f"╠{'═' * 58}╣")
    print(f"║ Name:    {args.name:<48}║")
    print(f"║ Address: {args.host}:{args.port:<43}║")
    print(f"╚{'═' * 58}╝")
    print()
    print("Server is ready to accept connections.")
    print("Press Ctrl+C to stop.\n")
    
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\n\nShutting down server...")
        server.shutdown()
        print("Server stopped.")


if __name__ == '__main__':
    main()

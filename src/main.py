import http.server
import socketserver
import os

PORT = 8000
DIR = os.path.join(os.path.dirname(__file__), "../html")

class Handler(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, directory=DIR, **kwargs)

    def log_message(self, format, *args):
        print(f"{self.address_string()} - {format % args}")

if __name__ == "__main__":
    with socketserver.TCPServer(("", PORT), Handler) as httpd:
        print(f"serving → http://localhost:{PORT}")
        httpd.serve_forever()
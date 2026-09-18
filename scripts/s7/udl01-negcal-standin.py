#!/usr/bin/env python3
"""udl01-negcal-standin.py — 负向校准专用**服务替身**（绝不接触真实用户服务）。

行为对齐 nginx 的最小面：静态文件 + Content-Security-Policy 头，
使 udl01-deploy-user-console-v3.sh 的 served/asset/CSP 门禁能在隔离目录上被真实触发
（而不是被静默跳过）。用法：udl01-negcal-standin.py <port> <docroot>。
"""
import functools
import http.server
import socketserver
import sys

CSP = "default-src self; script-src self; object-src none; base-uri self"


class Handler(http.server.SimpleHTTPRequestHandler):
    def end_headers(self):
        self.send_header("Content-Security-Policy", CSP)
        super().end_headers()

    def log_message(self, fmt, *args):
        sys.stderr.write("%s - - [%s] %s\n" % (self.address_string(), self.log_date_time_string(), fmt % args))


def main():
    port = int(sys.argv[1])
    root = sys.argv[2]
    handler = functools.partial(Handler, directory=root)
    socketserver.ThreadingTCPServer.allow_reuse_address = True
    with socketserver.ThreadingTCPServer(("127.0.0.1", port), handler) as httpd:
        httpd.serve_forever()


if __name__ == "__main__":
    main()

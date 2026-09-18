#!/usr/bin/env python3
"""Count core-NATS messages per subject for a bounded window.

The P4 bus round needs to know whether ONE refused request published a scope
control message. The JetStream totals are useless for that: the platform has a
background publish rate of several messages per second, so a raw total delta
cannot tell the request signal from the noise. This counter subscribes to the
subject space directly and tallies the messages that actually arrive while the
window is open.

Protocol: the NATS client protocol over a plain TCP socket, no client library.
  CONNECT {"verbose":false,"pedantic":false,"name":"p4-counter"}\r\n
  SUB <subject> 1\r\n
  PING\r\n
then read MSG frames:  MSG <subject> <sid> [reply] <size>\r\n<payload>\r\n

Usage: nats_subject_counter.py <url-host:port> <subject-prefix> <seconds> <outfile>
Writes "<count> <subject>" lines plus a TOTAL line.
"""
import socket
import sys
import time

hostport, subject, seconds, outfile = sys.argv[1], sys.argv[2], float(sys.argv[3]), sys.argv[4]
if ":" in hostport:
    host, port = hostport.rsplit(":", 1)
    port = int(port)
else:
    host, port = hostport, 4222

counts = {}
deadline = time.time() + seconds

try:
    sock = socket.create_connection((host, port), timeout=5)
except OSError as exc:
    with open(outfile, "w") as fh:
        fh.write("CONNECT_FAILED %s\n" % exc)
    sys.exit(0)

sock.settimeout(0.5)
sock.sendall(
    ('CONNECT {"verbose":false,"pedantic":false,"name":"p4-counter"}\r\n').encode()
    + ("SUB %s 1\r\n" % subject).encode()
    + b"PING\r\n"
)

buf = b""
while time.time() < deadline:
    try:
        chunk = sock.recv(65536)
    except socket.timeout:
        continue
    except OSError:
        break
    if not chunk:
        break
    buf += chunk
    while True:
        nl = buf.find(b"\r\n")
        if nl < 0:
            break
        line = buf[:nl].decode("utf-8", "replace")
        parts = line.split()
        if parts and parts[0] == "MSG":
            size = int(parts[-1])
            if len(buf) < nl + 2 + size + 2:
                break
            payload = buf[nl + 2:nl + 2 + size]
            subj = parts[1]
            counts[subj] = counts.get(subj, 0) + 1
            buf = buf[nl + 2 + size + 2:]
            continue
        if parts and parts[0] == "PING":
            try:
                sock.sendall(b"PONG\r\n")
            except OSError:
                pass
        buf = buf[nl + 2:]

try:
    sock.close()
except OSError:
    pass

with open(outfile, "w") as fh:
    for subj in sorted(counts):
        fh.write("%d %s\n" % (counts[subj], subj))
    fh.write("TOTAL %d\n" % sum(counts.values()))

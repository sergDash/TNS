#!/usr/bin/env python3
"""Снимок страницы через Marionette: переход, ожидание, скриншот.

Нужен, чтобы проверять страницу после того, как она получила данные,
а не на событии load. Firefox запускается с --marionette и слушает 2828.
"""
import base64
import json
import socket
import sys
import time

PORT = 2828


class Marionette:
    def __init__(self, port=PORT, timeout=20.0):
        deadline = time.time() + timeout
        while True:
            try:
                self.sock = socket.create_connection(("127.0.0.1", port), timeout=5)
                break
            except OSError:
                if time.time() > deadline:
                    raise
                time.sleep(0.2)
        self.buf = b""
        self.id = 0
        self._send({"applicationType": "gecko", "marionetteProtocol": 3})
        self.handshake = self._recv()

    def _send(self, payload):
        body = json.dumps(payload).encode()
        self.sock.sendall(str(len(body)).encode() + b":" + body)

    def _recv(self):
        while b":" not in self.buf:
            chunk = self.sock.recv(65536)
            if not chunk:
                raise RuntimeError("соединение закрыто")
            self.buf += chunk
        head, rest = self.buf.split(b":", 1)
        size = int(head)
        while len(rest) < size:
            chunk = self.sock.recv(65536)
            if not chunk:
                raise RuntimeError("соединение закрыто")
            rest += chunk
        self.buf = rest[size:]
        return json.loads(rest[:size])

    def call(self, name, params=None):
        self.id += 1
        self._send([0, self.id, name, params or {}])
        while True:
            msg = self._recv()
            if isinstance(msg, list) and len(msg) > 1 and msg[1] == self.id:
                if msg[2] is not None:
                    raise RuntimeError("%s: %s" % (name, msg[2]))
                return msg[3]


def main():
    url, out, wait = sys.argv[1], sys.argv[2], float(sys.argv[3]) if len(sys.argv) > 3 else 1.5
    m = Marionette()
    m.call("WebDriver:NewSession", {"capabilities": {}})
    m.call("WebDriver:Navigate", {"url": url})
    time.sleep(wait)
    shot = m.call("WebDriver:TakeScreenshot", {"full": False})
    # Firefox отдаёт снимок объектом {"value": "<base64>"}.
    if isinstance(shot, dict):
        shot = shot.get("value", "")
    with open(out, "wb") as f:
        f.write(base64.b64decode(shot))
    m.call("Marionette:Quit", {"flags": ["eForceQuit"]})
    print("снимок:", out)


if __name__ == "__main__":
    main()

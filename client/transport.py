import json
import socket
import struct
from typing import Any

class TransportError(RuntimeError):
    pass

def send_legacy_json(sock: socket.socket, message: dict[str, Any]) -> None:
    sock.sendall(json.dumps(message, separators=(",", ":")).encode("utf-8"))

def receive_legacy_json(sock: socket.socket, buffer_size: int = 65536):
    raw = sock.recv(buffer_size)
    if not raw:
        return None
    try:
        value = json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise TransportError("El servidor envió un JSON inválido.") from exc
    if not isinstance(value, dict):
        raise TransportError("La respuesta no es un objeto JSON.")
    return value

def send_framed_json(sock: socket.socket, message: dict[str, Any]) -> None:
    payload = json.dumps(message, separators=(",", ":")).encode("utf-8")
    sock.sendall(struct.pack("!I", len(payload)) + payload)

def _receive_exact(sock: socket.socket, size: int):
    data = bytearray()
    while len(data) < size:
        chunk = sock.recv(size - len(data))
        if not chunk:
            return None
        data.extend(chunk)
    return bytes(data)

def receive_framed_json(sock: socket.socket):
    header = _receive_exact(sock, 4)
    if header is None:
        return None
    size = struct.unpack("!I", header)[0]
    if size <= 0 or size > 10_000_000:
        raise TransportError(f"Tamaño de trama inválido: {size}")
    payload = _receive_exact(sock, size)
    if payload is None:
        raise TransportError("La conexión terminó antes de recibir la trama completa.")
    try:
        value = json.loads(payload.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise TransportError("La trama no contiene JSON válido.") from exc
    if not isinstance(value, dict):
        raise TransportError("La trama no es un objeto JSON.")
    return value

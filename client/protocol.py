from dataclasses import dataclass
from typing import Any
from client.link_layer import decode, encode
from client.noise import apply_noise
from client.presentation import deserialize_message, serialize_message

@dataclass(frozen=True)
class OutgoingFrame:
    envelope: dict[str, Any]
    flip_count: int
    original_bit_length: int
    encoded_bit_length: int
    @property
    def overhead_bits(self):
        return self.encoded_bit_length - self.original_bit_length

def build_frame(message: dict[str, Any], algorithm: str, error_rate: float) -> OutgoingFrame:
    payload = serialize_message(message)
    original_bits = len(payload) * 8
    encoded = encode(payload, algorithm)
    noisy = apply_noise(encoded, error_rate)
    envelope = {
        "version": 1,
        "algorithm": algorithm,
        "frame": noisy.bits,
        "metadata": {
            "error_rate": error_rate,
            "flip_count_sender": noisy.flip_count,
            "original_bits": original_bits,
            "encoded_bits": len(encoded),
        },
    }
    return OutgoingFrame(envelope, noisy.flip_count, original_bits, len(encoded))

def parse_frame(envelope: dict[str, Any]):
    algorithm = envelope.get("algorithm")
    frame = envelope.get("frame")
    if algorithm not in {"hamming", "crc32"}:
        raise ValueError("Algoritmo desconocido.")
    if not isinstance(frame, str):
        raise ValueError("La trama no contiene una cadena binaria.")
    decoded = decode(frame, algorithm)
    return deserialize_message(decoded.payload), decoded

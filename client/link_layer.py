from dataclasses import dataclass
from algorithms import crc32, hamming
from client.presentation import bits_to_bytes, bytes_to_bits

class IntegrityError(ValueError):
    pass

@dataclass(frozen=True)
class DecodedFrame:
    payload: bytes
    algorithm: str
    error_detected: bool
    error_corrected: bool
    correction_count: int

def encode(payload: bytes, algorithm: str) -> str:
    if algorithm == "hamming":
        return hamming.encode(payload)
    if algorithm == "crc32":
        return crc32.encode(bytes_to_bits(payload))
    raise ValueError(f"Algoritmo no soportado: {algorithm}")

def decode(frame_bits: str, algorithm: str) -> DecodedFrame:
    if algorithm == "hamming":
        result = hamming.decode(frame_bits)
        return DecodedFrame(result.data, algorithm, result.corrected_blocks > 0,
                            result.corrected_blocks > 0, result.corrected_blocks)
    if algorithm == "crc32":
        if not crc32.verify(frame_bits):
            raise IntegrityError("CRC-32 detectó que la trama fue alterada.")
        return DecodedFrame(bits_to_bytes(crc32.extract_data(frame_bits)), algorithm,
                            False, False, 0)
    raise ValueError(f"Algoritmo no soportado: {algorithm}")

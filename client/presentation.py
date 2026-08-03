import json
from typing import Any

class PresentationError(ValueError):
    pass

def serialize_message(message: dict[str, Any]) -> bytes:
    return json.dumps(message, ensure_ascii=False, separators=(",", ":")).encode("utf-8")

def deserialize_message(payload: bytes) -> dict[str, Any]:
    try:
        value = json.loads(payload.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise PresentationError("No fue posible decodificar el mensaje.") from exc
    if not isinstance(value, dict):
        raise PresentationError("El mensaje no es un objeto JSON.")
    return value

def bytes_to_bits(payload: bytes) -> str:
    return "".join(f"{b:08b}" for b in payload)

def bits_to_bytes(bits: str) -> bytes:
    if any(b not in "01" for b in bits):
        raise PresentationError("La cadena solo puede contener 0 y 1.")
    if len(bits) % 8 != 0:
        raise PresentationError("La longitud binaria no es múltiplo de 8.")
    return bytes(int(bits[i:i+8], 2) for i in range(0, len(bits), 8))

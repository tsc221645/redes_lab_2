import json
import socket


HOST = "127.0.0.1"  # Cambia a "0.0.0.0" si el emisor corre en otra computadora.
PORT = 1025
MAX_MESSAGE_BYTES = 1_000_000

# CRC-32 IEEE 802.3: x^32 + x^26 + x^23 + ... + 1
# Se usa esta representacion binaria normal (33 bits, incluyendo x^32).
CRC32_POLYNOMIAL = "100000100110000010001110110110111"


def send_msg(conn, action, data):
    """Envia un objeto JSON terminado en salto de linea."""
    message = {"action": action, "data": data}
    conn.sendall((json.dumps(message) + "\n").encode("utf-8"))


def recv_msg(reader):
    """Lee un JSON por linea; devuelve None si el cliente cerro la conexion."""
    raw = reader.readline(MAX_MESSAGE_BYTES + 1)
    if not raw:
        return None
    if len(raw) > MAX_MESSAGE_BYTES:
        raise ValueError("La trama supera el tamano maximo permitido.")
    return json.loads(raw.decode("utf-8"))


def validate_bits(bits):
    if not isinstance(bits, str) or not bits:
        raise ValueError("La trama debe ser una cadena binaria no vacia.")
    if any(bit not in "01" for bit in bits):
        raise ValueError("La trama solo puede contener 0 y 1.")


def bits_to_ascii(bits):
    """Convierte grupos de 8 bits ASCII a texto."""
    if len(bits) % 8 != 0:
        raise ValueError(
            f"Los datos recuperados tienen {len(bits)} bits; no forman bytes ASCII completos."
        )
    return "".join(chr(int(bits[index:index + 8], 2)) for index in range(0, len(bits), 8))


def is_parity_position(position):
    return position > 0 and (position & (position - 1)) == 0


def hamming_decode(frame):
    """
    Verifica Hamming con paridad par y corrige un error de un bit.

    Convencion compartida con el emisor: las posiciones se numeran desde 1,
    de izquierda a derecha; las posiciones 1, 2, 4, 8, ... son de paridad.
    """
    validate_bits(frame)
    codeword = [int(bit) for bit in frame]
    total_bits = len(codeword)
    syndrome = 0

    parity_position = 1
    while parity_position <= total_bits:
        parity = 0
        for position in range(1, total_bits + 1):
            if position & parity_position:
                parity ^= codeword[position - 1]
        if parity:
            syndrome += parity_position
        parity_position <<= 1

    corrected_bit = None
    if syndrome:
        if syndrome > total_bits:
            raise ValueError(
                "Hamming detecto una inconsistencia que no corresponde a una posicion valida."
            )
        codeword[syndrome - 1] ^= 1
        corrected_bit = syndrome  # Posicion 1-based, igual que en la teoria.

    data_bits = "".join(
        str(bit)
        for position, bit in enumerate(codeword, start=1)
        if not is_parity_position(position)
    )
    message = bits_to_ascii(data_bits)
    return message, corrected_bit


def crc32_remainder(data_bits):
    """Calcula el residuo CRC-32 mediante division modulo 2."""
    validate_bits(data_bits)
    dividend = list(data_bits + "0" * 32)
    generator = CRC32_POLYNOMIAL

    for index in range(len(data_bits)):
        if dividend[index] == "1":
            for offset, generator_bit in enumerate(generator):
                dividend[index + offset] = str(int(dividend[index + offset]) ^ int(generator_bit))

    return "".join(dividend[-32:])


def crc32_decode(frame, original_bit_length=None):
    """Verifica una trama data || crc, donde el CRC ocupa los ultimos 32 bits."""
    validate_bits(frame)
    if len(frame) <= 32:
        raise ValueError("Una trama CRC-32 debe incluir datos y 32 bits de CRC.")

    data_bits = frame[:-32]
    received_crc = frame[-32:]
    calculated_crc = crc32_remainder(data_bits)

    if received_crc != calculated_crc:
        return None, received_crc, calculated_crc

    # CRC-32 requiere padding cuando los datos originales tienen 32 bits o menos.
    # El emisor incluye la longitud original para que el receptor pueda retirarlo.
    if original_bit_length is not None:
        if not isinstance(original_bit_length, int) or not 0 < original_bit_length <= len(data_bits):
            raise ValueError("original_bit_length no es valido para esta trama CRC-32.")
        data_bits = data_bits[:original_bit_length]

    return bits_to_ascii(data_bits), received_crc, calculated_crc


def process_frame(data):
    algorithm = data.get("algorithm", "").upper()
    frame = data.get("frame")

    if algorithm == "HAMMING":
        message, corrected_bit = hamming_decode(frame)
        if corrected_bit is None:
            return {
                "status": "ok",
                "algorithm": "HAMMING",
                "message": message,
                "detail": "Trama valida; no fue necesario corregir errores.",
            }
        return {
            "status": "corrected",
            "algorithm": "HAMMING",
            "message": message,
            "corrected_bit": corrected_bit,
            "detail": f"Se corrigio el bit {corrected_bit} (posicion 1-based).",
        }

    if algorithm == "CRC32":
        message, received_crc, calculated_crc = crc32_decode(
            frame, data.get("original_bit_length")
        )
        if message is None:
            return {
                "status": "error",
                "algorithm": "CRC32",
                "message": None,
                "detail": "CRC-32 invalido: se detecto un error y la trama fue descartada.",
                "received_crc": received_crc,
                "calculated_crc": calculated_crc,
            }
        return {
            "status": "ok",
            "algorithm": "CRC32",
            "message": message,
            "detail": "CRC-32 valido; no se detectaron errores.",
        }

    raise ValueError("Algoritmo no soportado. Use HAMMING o CRC32.")


def handle_client(conn, addr):
    print(f"[SERVER] Conexion desde {addr}")
    with conn.makefile("rb") as reader:
        while True:
            try:
                request = recv_msg(reader)
                if request is None:
                    print("[SERVER] Cliente desconectado.")
                    return

                if request.get("action") != "send_frame":
                    send_msg(conn, "frame_result", {
                        "status": "error",
                        "detail": "La accion esperada es 'send_frame'.",
                    })
                    continue

                result = process_frame(request.get("data", {}))
                print(f"[SERVER] {result['algorithm']}: {result['detail']}")
                if result.get("message") is not None:
                    print(f"[SERVER] Mensaje: {result['message']}")
                send_msg(conn, "frame_result", result)

            except (UnicodeDecodeError, json.JSONDecodeError, ValueError) as error:
                print(f"[SERVER] Solicitud invalida: {error}")
                send_msg(conn, "frame_result", {"status": "error", "detail": str(error)})
            except ConnectionError:
                print("[SERVER] Conexion terminada inesperadamente.")
                return


def main():
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as server_socket:
        server_socket.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        server_socket.bind((HOST, PORT))
        server_socket.listen()
        print(f"[SERVER] Escuchando en {HOST}:{PORT}")

        while True:
            conn, addr = server_socket.accept()
            with conn:
                handle_client(conn, addr)


if __name__ == "__main__":
    main()

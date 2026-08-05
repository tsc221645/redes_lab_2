"""Servidor del laboratorio: aplicacion bancaria sobre un canal protegido."""

import json
import random
import socket

HOST = "127.0.0.1"
PORT = 1025
MAX_MESSAGE_BYTES = 1_000_000
CRC32_POLYNOMIAL = "100000100110000010001110110110111"

# Presentacion
def validate_bits(bits):
    if not isinstance(bits, str) or not bits or any(bit not in "01" for bit in bits):
        raise ValueError("La trama debe ser una cadena binaria no vacia.")


def ascii_to_bits(text):
    if not isinstance(text, str) or any(ord(char) > 127 for char in text):
        raise ValueError("El mensaje debe contener unicamente ASCII.")
    return "".join(f"{byte:08b}" for byte in text.encode("ascii"))


def bits_to_ascii(bits):
    validate_bits(bits)
    if len(bits) % 8:
        raise ValueError("Los datos recuperados no forman bytes completos.")
    raw = bytes(int(bits[i:i + 8], 2) for i in range(0, len(bits), 8))
    return raw.decode("ascii")


# Enlace: Hamming(12,8), posiciones 1..12 de izquierda a derecha.
def hamming_encode(data_bits):
    validate_bits(data_bits)
    if len(data_bits) % 8:
        raise ValueError("Hamming(12,8) requiere datos en bytes.")
    result = []
    for start in range(0, len(data_bits), 8):
        data = data_bits[start:start + 8]
        word = [0] * 13
        data_index = 0
        for position in range(1, 13):
            if position not in (1, 2, 4, 8):
                word[position] = int(data[data_index])
                data_index += 1
        for parity_position in (1, 2, 4, 8):
            word[parity_position] = sum(
                word[position] for position in range(1, 13)
                if position & parity_position and position != parity_position
            ) % 2
        result.extend(str(word[position]) for position in range(1, 13))
    return "".join(result)


def hamming_decode(frame):
    validate_bits(frame)
    if len(frame) % 12:
        raise ValueError("Una trama Hamming debe contener bloques de 12 bits.")
    data_bits, corrected = [], []
    for block_start in range(0, len(frame), 12):
        word = [0] + [int(bit) for bit in frame[block_start:block_start + 12]]
        syndrome = 0
        for parity_position in (1, 2, 4, 8):
            if sum(word[position] for position in range(1, 13) if position & parity_position) % 2:
                syndrome += parity_position
        if syndrome:
            if syndrome > 12:
                raise ValueError("Sindrome Hamming invalido.")
            word[syndrome] ^= 1
            corrected.append(block_start + syndrome)
        data_bits.extend(str(word[position]) for position in range(1, 13)
                         if position not in (1, 2, 4, 8))
    return bits_to_ascii("".join(data_bits)), corrected


def crc32_remainder(data_bits):
    validate_bits(data_bits)
    dividend = list(data_bits + "0" * 32)
    for index in range(len(data_bits)):
        if dividend[index] == "1":
            for offset, bit in enumerate(CRC32_POLYNOMIAL):
                dividend[index + offset] = str(int(dividend[index + offset]) ^ int(bit))
    return "".join(dividend[-32:])


def crc32_encode(data_bits):
    validate_bits(data_bits)
    return data_bits + crc32_remainder(data_bits)


def crc32_decode(frame, original_bit_length):
    validate_bits(frame)
    if len(frame) <= 32 or not isinstance(original_bit_length, int):
        raise ValueError("Una trama CRC32 requiere datos, CRC y longitud original.")
    data, received = frame[:-32], frame[-32:]
    calculated = crc32_remainder(data)
    if received != calculated:
        return None, received, calculated
    if not 0 < original_bit_length <= len(data) or original_bit_length % 8:
        raise ValueError("original_bit_length invalido.")
    return bits_to_ascii(data[:original_bit_length]), received, calculated


# Ruido
def apply_noise(bits, mode="none", probability=0.0, forced_bit=0, rng=None):
    validate_bits(bits)
    if mode == "none":
        return bits, []
    output = list(bits)
    flipped = []
    if mode == "forced":
        if not isinstance(forced_bit, int) or not 1 <= forced_bit <= len(output):
            raise ValueError("forced_bit fuera de rango.")
        output[forced_bit - 1] = "1" if output[forced_bit - 1] == "0" else "0"
        return "".join(output), [forced_bit]
    if mode != "random" or not 0 <= probability <= 1:
        raise ValueError("Configuracion de ruido invalida.")
    rng = rng or random.Random()
    for index in range(len(output)):
        if rng.random() < probability:
            output[index] = "1" if output[index] == "0" else "0"
            flipped.append(index + 1)
    return "".join(output), flipped


# Transmision
def send_msg(conn, action, data):
    conn.sendall((json.dumps({"action": action, "data": data}, separators=(",", ":")) + "\n").encode())


def recv_msg(reader):
    raw = reader.readline(MAX_MESSAGE_BYTES + 1)
    if not raw:
        return None
    if len(raw) > MAX_MESSAGE_BYTES:
        raise ValueError("La trama supera el tamano maximo permitido.")
    return json.loads(raw.decode("utf-8"))


def encode_payload(message, algorithm):
    bits = ascii_to_bits(message)
    algorithm = algorithm.upper()
    frame = hamming_encode(bits) if algorithm == "HAMMING" else crc32_encode(bits) if algorithm == "CRC32" else None
    if frame is None:
        raise ValueError("Algoritmo no soportado. Use HAMMING o CRC32.")
    return frame, len(bits)


def decode_payload(data):
    algorithm = str(data.get("algorithm", "")).upper()
    if algorithm == "HAMMING":
        message, corrected = hamming_decode(data.get("frame"))
        return message, "corrected" if corrected else "ok", {"corrected_bits": corrected}
    if algorithm == "CRC32":
        message, received, calculated = crc32_decode(data.get("frame"), data.get("original_bit_length"))
        if message is None:
            return None, "error", {"received_crc": received, "calculated_crc": calculated}
        return message, "ok", {}
    raise ValueError("Algoritmo no soportado. Use HAMMING o CRC32.")


# Aplicacion bancaria
def process_banking_message(message, session, accounts):
    try:
        request = json.loads(message)
    except json.JSONDecodeError:
        return {"status": "ok", "message": message, "detail": "Mensaje de prueba recibido."}
    action = request.get("action")
    data = request.get("data") or {}
    if action == "login":
        account = accounts.get(str(data.get("card")))
        if not account or str(data.get("pin")) != account["pin"]:
            return {"status": "error", "message": None, "detail": "Tarjeta o PIN incorrectos."}
        session["card"] = str(data["card"])
        return {"status": "ok", "message": "Login exitoso.", "balance": account["balance"]}
    if action == "withdraw":
        if "card" not in session:
            return {"status": "error", "message": None, "detail": "Debe iniciar sesion."}
        try:
            amount = float(data.get("amount"))
        except (TypeError, ValueError):
            return {"status": "error", "message": None, "detail": "El monto debe ser numerico."}
        account = accounts[session["card"]]
        if amount <= 0:
            return {"status": "error", "message": None, "detail": "El monto debe ser mayor que cero."}
        if amount > account["balance"]:
            return {"status": "error", "message": None, "detail": "Fondos insuficientes."}
        account["balance"] -= amount
        return {"status": "ok", "message": "Retiro aprobado.", "balance": account["balance"]}
    if action == "logout":
        session.pop("card", None)
        return {"status": "ok", "message": "Logout exitoso."}
    return {"status": "error", "message": None, "detail": "Accion bancaria desconocida."}


def handle_client(conn, addr):
    print(f"[SERVER] Conexion desde {addr}")
    session = {}
    accounts = {"221645": {"pin": "1234", "balance": 500.0}}
    with conn.makefile("rb") as reader:
        while True:
            request_data = {}
            try:
                request = recv_msg(reader)
                if request is None:
                    return
                if request.get("action") != "send_frame":
                    raise ValueError("La accion esperada es send_frame.")
                request_data = request.get("data") or {}
                message, status, details = decode_payload(request_data)
                if message is None:
                    response = {"status": "error", "message": None, "detail": "CRC32 invalido; trama descartada.", **details}
                else:
                    response = process_banking_message(message, session, accounts)
                    response.update(details)
                    response.setdefault("detail", "Trama recibida correctamente.")
                algorithm = str(request_data.get("algorithm", "")).upper()
                response["request_status"] = status
                response["algorithm"] = algorithm
                response_message = json.dumps(response, separators=(",", ":"), ensure_ascii=True)
                frame, length = encode_payload(response_message, algorithm)
                metadata = request_data.get("metadata") or {}
                response_mode = metadata.get("response_error_mode", "none")
                frame, response_flips = apply_noise(frame, response_mode, float(metadata.get("response_error_probability", 0.0)), int(metadata.get("response_forced_bit", 0)))
                send_msg(conn, "frame_result", {"algorithm": algorithm, "frame": frame, "original_bit_length": length,
                                                  "metadata": {"status_before_encoding": response["status"], "response_flipped_bits": response_flips}})
            except (UnicodeDecodeError, json.JSONDecodeError, ValueError, TypeError) as error:
                print(f"[SERVER] Solicitud invalida: {error}")
                # Incluso los errores de decodificacion mantienen el protocolo
                # protegido para que Go pueda procesarlos sin perder sincronizacion.
                algorithm = str(request_data.get("algorithm", "")).upper()
                if algorithm in ("HAMMING", "CRC32"):
                    error_payload = json.dumps({
                        "status": "error", "message": None,
                        "detail": str(error), "request_status": "error",
                        "algorithm": algorithm,
                    }, separators=(",", ":"), ensure_ascii=True)
                    frame, length = encode_payload(error_payload, algorithm)
                    send_msg(conn, "frame_result", {
                        "algorithm": algorithm, "frame": frame,
                        "original_bit_length": length,
                        "metadata": {"status_before_encoding": "error",
                                     "response_flipped_bits": []},
                    })
                else:
                    send_msg(conn, "frame_result", {
                        "algorithm": "", "frame": "", "original_bit_length": 0,
                        "metadata": {"status_before_encoding": "error",
                                     "detail": str(error)},
                    })
            except ConnectionError:
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

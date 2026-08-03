import socket
from typing import Any
import config
from link_layer import IntegrityError
from protocol import build_frame, parse_frame
from transport import (TransportError, receive_framed_json, receive_legacy_json,
                       send_framed_json, send_legacy_json)

def ask_algorithm():
    while True:
        value = input("Algoritmo [hamming/crc32]: ").strip().lower()
        if value in {"hamming", "crc32"}:
            return value
        print("Seleccione hamming o crc32.")

def ask_error_rate():
    while True:
        try:
            value = float(input("Probabilidad de flip por bit (ej. 0.01): ").strip())
            if 0 <= value <= 1:
                return value
        except ValueError:
            pass
        print("Ingrese un número entre 0 y 1.")

def send_message(sock, action: str, data: dict[str, Any], algorithm: str, error_rate: float):
    message = {"action": action, "data": data}
    if config.MODE == "legacy":
        send_legacy_json(sock, message)
        return
    outgoing = build_frame(message, algorithm, error_rate)
    send_framed_json(sock, outgoing.envelope)
    print(f"[ENLACE] original={outgoing.original_bit_length}, codificada={outgoing.encoded_bit_length}, overhead={outgoing.overhead_bits} bits")
    print(f"[RUIDO] flips aplicados: {outgoing.flip_count}")

def receive_message(sock):
    if config.MODE == "legacy":
        return receive_legacy_json(sock, config.BUFFER_SIZE)
    envelope = receive_framed_json(sock)
    if envelope is None:
        return None
    message, decoded = parse_frame(envelope)
    if decoded.error_corrected:
        print(f"[ENLACE] Hamming corrigió {decoded.correction_count} bloque(s).")
    return message

def login(sock, algorithm, error_rate):
    while True:
        card = input("Enter card number: ").strip()
        pin = input("Enter PIN: ").strip()
        send_message(sock, "login", {"card": card, "pin": pin}, algorithm, error_rate)
        response = receive_message(sock)
        if response is None:
            print("El servidor cerró la conexión.")
            return False
        if response.get("action") == "login_ok":
            print(">> " + response.get("data", {}).get("message", "Login correcto"))
            return True
        print(">> " + response.get("data", {}).get("message", "Login rechazado"))

def menu(sock, algorithm, error_rate):
    while True:
        print("\n--- MENU ---\n1) Withdraw money\n2) Logout")
        choice = input("Choose an option: ").strip()
        if choice == "1":
            try:
                amount = float(input("Amount to withdraw: ").strip())
            except ValueError:
                print(">> Invalid amount.")
                continue
            send_message(sock, "withdraw", {"amount": amount}, algorithm, error_rate)
            response = receive_message(sock)
            if response is None:
                return
            if response.get("action") == "withdraw_ok":
                d = response.get("data", {})
                print(f">> Please take your ${d['amount']:.2f}")
                print(f">> Remaining balance: ${d['balance']:.2f}")
            else:
                print(">> " + response.get("data", {}).get("message", "Error"))
        elif choice == "2":
            send_message(sock, "logout", {}, algorithm, error_rate)
            response = receive_message(sock)
            if response:
                print(">> " + response.get("data", {}).get("message", "Goodbye"))
            return
        else:
            print(">> Invalid option.")

def main():
    algorithm = config.DEFAULT_ALGORITHM
    error_rate = config.DEFAULT_ERROR_RATE
    print(f"[CLIENT] Modo: {config.MODE}")
    if config.MODE == "lab":
        algorithm = ask_algorithm()
        error_rate = ask_error_rate()
    try:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
            sock.connect((config.HOST, config.PORT))
            print(f"[CLIENT] Connected to {config.HOST}:{config.PORT}")
            if login(sock, algorithm, error_rate):
                menu(sock, algorithm, error_rate)
    except ConnectionRefusedError:
        print("No fue posible conectarse. Inicie primero el servidor.")
    except (TransportError, IntegrityError, ValueError) as exc:
        print(f"[ERROR] {exc}")
    except KeyboardInterrupt:
        print("\n[CLIENT] Ejecución cancelada.")

if __name__ == "__main__":
    main()

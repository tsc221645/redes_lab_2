# Archivos para trabajar `client.py`

La solución separa el cliente en las capas solicitadas:

- `client.py`: aplicación del cajero.
- `presentation.py`: JSON, UTF-8 y bits ASCII.
- `link_layer.py`: Hamming(12,8) y CRC-32.
- `noise.py`: flips probabilísticos.
- `transport.py`: sockets TCP.
- `protocol.py`: construcción de las tramas.
- `config.py`: host, puerto y modo.

## Probar con el `server.py` original

Deje en `config.py`:

```python
MODE = "legacy"
```

Ejecute primero el servidor original y después:

```bash
python client.py
```

## Activar las capas del laboratorio

Cambie a:

```python
MODE = "lab"
```

En este modo el servidor también debe implementar el mismo protocolo, Hamming/CRC, framing de 4 bytes y decodificación de la trama. El `server.py` original no entiende todavía estas tramas.

## Ejecutar pruebas

```bash
python -m unittest discover -s tests -v
```

No se requieren librerías externas.

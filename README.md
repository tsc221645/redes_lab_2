# Laboratorio 2 - Esquemas de detección y corrección de errores

Aplicación cliente-servidor para simular la transmisión de mensajes a través de un canal no confiable. El emisor está desarrollado en Go y el receptor en Python. Ambos se comunican por sockets TCP y emplean JSON delimitado por saltos de línea.

La aplicación implementa:

- **Hamming** con paridad par para corregir un error de un bit por palabra código.
- **CRC-32** para detectar alteraciones en una trama, usando el polinomio generador `0x04C11DB7`.
- Simulación de ruido mediante *bit flips* independientes con una probabilidad configurable.
- Pruebas manuales y una matriz automática de 800 transmisiones.
- Registro de resultados en CSV y generación de gráficas.

## Integrantes

- Ana Laura Tschen - 221645
- Karen Pineda - 231132

## Arquitectura

```text
Emisor / Cajero (Go)                                  Receptor / Servidor (Python)
--------------------                                 -----------------------------
Aplicación: solicita mensaje y opciones               Aplicación: muestra resultado
Presentación: texto ASCII -> bits                     Presentación: bits ASCII -> texto
Enlace: Hamming o CRC-32                              Enlace: verifica CRC-32 o corrige Hamming
Ruido: flips probabilísticos                          Transmisión: recibe y responde por TCP
Transmisión: envía trama por TCP
                    └──── JSON por línea / puerto 1025 ────┘
```

### Protocolo de comunicación

El cliente envía una solicitud JSON terminada con `\n`:

```json
{
  "action": "send_frame",
  "data": {
    "algorithm": "HAMMING",
    "frame": "010101..."
  }
}
```

Para CRC-32 se incluye además `original_bit_length`, que permite quitar el padding utilizado cuando la cadena original tiene 32 bits o menos:

```json
{
  "action": "send_frame",
  "data": {
    "algorithm": "CRC32",
    "frame": "010101...",
    "original_bit_length": 24
  }
}
```

El servidor responde un JSON con `status` igual a `ok`, `corrected` o `error`.

## Estructura sugerida

```text
redes_lab_2/
├── client_go/
│   ├── go.mod                  # Módulo de Go
│   ├── main.go                 # Emisor en Go
│   └── resultados.csv          # Generado por las pruebas
├── server/
│   └── server1.py              # Receptor en Python
├── tests_python/
│   ├── plot_results.py         # Script de pandas y matplotlib
│   ├── requirements.txt        # Dependencias del análisis
│   └── graficas/               # Imágenes generadas
└── README.md
```

## Requisitos

- Go 1.20 o posterior.
- Python 3.10 o posterior.
- Para las gráficas: `pandas` y `matplotlib`.

El cliente Go y el servidor Python no requieren librerías externas.

Instalación de dependencias para el análisis:

```bash
cd tests_python
python -m pip install -r requirements.txt
```

## Ejecución

> Los comandos deben ejecutarse desde las carpetas indicadas. El archivo `resultados.csv` se crea en el directorio actual del cliente Go.

### 1. Iniciar el servidor Python

En una primera terminal:

```bash
cd server
python server1.py
```

Salida esperada:

```text
[SERVER] Escuchando en 127.0.0.1:1025
```

El servidor debe permanecer en ejecución mientras se usa el cliente.

### 2. Iniciar el emisor Go

En una segunda terminal:

```bash
cd client_go
go run main.go
```

Salida esperada:

```text
[CLIENT] Conectado a 127.0.0.1:1025
```

Seleccione una opción:

```text
1) Prueba manual
2) Ejecutar matriz automática
3) Salir
```

Si se ejecuta el emisor en otra computadora, configure el servidor con `HOST = "0.0.0.0"` y reemplace `host = "127.0.0.1"` en Go por la dirección IP local de la computadora que ejecuta el servidor. También debe permitirse el puerto TCP `1025` en el firewall de Windows.

## Pruebas manuales

Seleccione la opción `1` en el cliente. Todas las pruebas se registran en `client_go/resultados.csv`.

| Caso | Parámetros sugeridos | Resultado esperado |
|---|---|---|
| Hamming sin ruido | Mensaje `Hola`, `HAMMING`, modo `1` | Estado `ok`; mensaje recuperado igual al original. |
| Hamming con un error | Mensaje `Hola`, `HAMMING`, modo `3`, bit `3` | Estado `corrected`; mensaje recuperado igual al original. |
| CRC-32 sin ruido | Mensaje `Hola`, `CRC32`, modo `1` | Estado `ok`; mensaje recuperado igual al original. |
| CRC-32 con error de datos | Mensaje `Hola`, `CRC32`, modo `3`, bit `1` | Estado `error`; el servidor descarta la trama. |
| CRC-32 con error de redundancia | Mensaje `Hola`, `CRC32`, modo `3`, bit `64` | Estado `error`; el servidor detecta el CRC alterado. |
| Hamming con varios errores | Mensaje largo, `HAMMING`, modo `2`, probabilidad `0.05` | Puede aparecer `corrected` sin recuperar el texto correcto; evidencia la limitación del código frente a errores múltiples. |

Para `Hola`, CRC-32 genera 32 bits de datos y 32 bits de CRC, por lo que la posición `64` altera el último bit de redundancia. No use esa posición con mensajes diferentes, porque la longitud codificada cambia.

## Pruebas automáticas y gráficas

Antes de volver a generar los resultados, elimine o renombre `client_go/resultados.csv`. El programa agrega filas al archivo existente; conservar ejecuciones previas alteraría las estadísticas.

1. Inicie el servidor.
2. Ejecute el cliente Go y seleccione `2`.
3. El cliente realiza **800 transmisiones**:

   - Algoritmos: Hamming y CRC-32.
   - Tamaños: 8, 32, 128 y 512 caracteres.
   - Probabilidades por bit: `0`, `0.0001`, `0.001`, `0.005` y `0.01`.
   - Repeticiones por combinación: 20.

   Cálculo: `2 algoritmos × 4 tamaños × 5 probabilidades × 20 repeticiones = 800`.

4. Desde la carpeta `tests_python`, ejecute:

```bash
python plot_results.py
```

Se crean las siguientes imágenes en `tests_python/graficas/`:

- `01_tasa_recuperacion.png`: porcentaje de mensajes recuperados correctamente según probabilidad de error.
- `02_overhead.png`: overhead porcentual de cada algoritmo según tamaño de mensaje.
- `03_flips_promedio.png`: promedio de bits alterados según probabilidad y tamaño.
- `04_estados.png`: cantidad de estados `ok`, `corrected` y `error` por algoritmo.

## Campos registrados en `resultados.csv`

| Campo | Descripción |
|---|---|
| `algorithm` | Algoritmo utilizado: `HAMMING` o `CRC32`. |
| `message_length_chars` | Tamaño del mensaje original, en caracteres ASCII. |
| `original_bits` | Bits del mensaje antes de añadir redundancia. |
| `encoded_bits` | Bits transmitidos, incluyendo Hamming, CRC o padding. |
| `overhead_bits` | Bits adicionales respecto al mensaje original. |
| `overhead_percentage` | Proporción de redundancia respecto a los bits originales. |
| `error_probability` | Probabilidad de flip aplicada a cada bit. |
| `flipped_bits` | Cantidad de bits alterados por el canal de ruido. |
| `status` | Respuesta del servidor: `ok`, `corrected` o `error`. |
| `message_recovered` | Indica si el texto recibido coincide exactamente con el texto original. |
| `elapsed_ms` | Tiempo de ida, procesamiento y respuesta, en milisegundos. |

## Consideraciones y limitaciones

- Hamming, en la forma implementada, corrige un único error por palabra código. Con múltiples alteraciones puede producir una corrección aparente que no recupera el mensaje original.
- CRC-32 detecta alteraciones, pero no las corrige. Para recuperar una trama descartada se requeriría retransmisión.
- El ruido se aplica antes del envío TCP y afecta tanto los datos como los bits de redundancia. Así se simula el canal no confiable solicitado por el laboratorio.
- TCP se utiliza para transportar las tramas entre procesos; las alteraciones evaluadas son las generadas artificialmente por la capa de ruido de la aplicación.
- Las pruebas emplean flips independientes y no modelan errores en ráfaga.

## Reproducibilidad

Las pruebas aleatorias usan una semilla basada en el tiempo actual. Por ello, los valores exactos pueden cambiar entre ejecuciones. Sin embargo, la configuración de tamaños, probabilidades, algoritmos y número de repeticiones se mantiene constante.

## Referencias

- Hamming, R. W. (1950). *Error detecting and error correcting codes*. Bell System Technical Journal, 29(2), 147-160. https://doi.org/10.1002/j.1538-7305.1950.tb00463.x
- Pfeiffer, S. (2003). *The Ogg encapsulation format version 0* (RFC 3533). RFC Editor. https://www.rfc-editor.org/rfc/rfc3533.html
- Eddy, W. (2022). *Transmission Control Protocol (TCP)* (RFC 9293). RFC Editor. https://doi.org/10.17487/RFC9293

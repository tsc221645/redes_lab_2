from pathlib import Path

import matplotlib.pyplot as plt
import pandas as pd

CSV_PATH = Path("../client_go/resultados.csv")
OUTPUT_DIR = Path("graficas")
OUTPUT_DIR.mkdir(exist_ok=True)

df = pd.read_csv(CSV_PATH)

# 1. Tasa de recuperación correcta por probabilidad
success = (
    df.groupby(["algorithm", "error_probability"], as_index=False)["message_recovered"]
      .mean()
)
success["success_percentage"] = success["message_recovered"] * 100

for algorithm, group in success.groupby("algorithm"):
    plt.plot(
        group["error_probability"],
        group["success_percentage"],
        marker="o",
        label=algorithm,
    )

plt.xlabel("Probabilidad de error por bit")
plt.ylabel("Mensajes recuperados correctamente (%)")
plt.title("Tasa de recuperación según probabilidad de error")
plt.legend()
plt.grid(True)
plt.tight_layout()
plt.savefig(OUTPUT_DIR / "01_tasa_recuperacion.png", dpi=180)
plt.close()

# 2. Overhead porcentual por tamaño
overhead = (
    df.groupby(["algorithm", "message_length_chars"], as_index=False)["overhead_percentage"]
      .mean()
)

for algorithm, group in overhead.groupby("algorithm"):
    plt.plot(
        group["message_length_chars"],
        group["overhead_percentage"],
        marker="o",
        label=algorithm,
    )

plt.xlabel("Tamaño del mensaje (caracteres)")
plt.ylabel("Overhead promedio (%)")
plt.title("Overhead por tamaño del mensaje")
plt.legend()
plt.grid(True)
plt.tight_layout()
plt.savefig(OUTPUT_DIR / "02_overhead.png", dpi=180)
plt.close()

# 3. Promedio de flips por probabilidad
flips = (
    df.groupby(["message_length_chars", "error_probability"], as_index=False)["flipped_bits"]
      .mean()
)

for size, group in flips.groupby("message_length_chars"):
    plt.plot(
        group["error_probability"],
        group["flipped_bits"],
        marker="o",
        label=f"{size} caracteres",
    )

plt.xlabel("Probabilidad de error por bit")
plt.ylabel("Bits alterados promedio")
plt.title("Bits alterados según probabilidad y tamaño")
plt.legend()
plt.grid(True)
plt.tight_layout()
plt.savefig(OUTPUT_DIR / "03_flips_promedio.png", dpi=180)
plt.close()

# 4. Distribución de estados
status_counts = pd.crosstab(df["algorithm"], df["status"])
status_counts.plot(kind="bar", stacked=True)
plt.xlabel("Algoritmo")
plt.ylabel("Cantidad de transmisiones")
plt.title("Estados reportados por algoritmo")
plt.tight_layout()
plt.savefig(OUTPUT_DIR / "04_estados.png", dpi=180)
plt.close()

print(f"Gráficas creadas en: {OUTPUT_DIR.resolve()}")

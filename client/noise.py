import random
from dataclasses import dataclass

@dataclass(frozen=True)
class NoiseResult:
    bits: str
    flipped_positions: tuple[int, ...]
    @property
    def flip_count(self) -> int:
        return len(self.flipped_positions)

def apply_noise(bits: str, error_rate: float, rng: random.Random | None = None) -> NoiseResult:
    if any(b not in "01" for b in bits):
        raise ValueError("La trama solo puede contener 0 y 1.")
    if not 0.0 <= error_rate <= 1.0:
        raise ValueError("La probabilidad debe estar entre 0 y 1.")
    generator = rng or random.Random()
    output = list(bits)
    flipped = []
    for i, bit in enumerate(output):
        if generator.random() < error_rate:
            output[i] = "1" if bit == "0" else "0"
            flipped.append(i)
    return NoiseResult("".join(output), tuple(flipped))

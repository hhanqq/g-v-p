"""Детектор дрейфа схемы — раздел 5, стадия 1.

parse_success_rate в скользящем окне; падение более чем на 5 процентных
пунктов относительно базовой линии — сигнал на откат версии парсера.
Сам откат (переключение на предыдущую версию connector-файла) — операция
админки (раздел 11.2), детектор только считает метрику и поднимает флаг.
"""
from __future__ import annotations

from collections import deque
from dataclasses import dataclass, field


@dataclass
class ParseSuccessTracker:
    window_size: int = 200
    drop_threshold_pp: float = 5.0
    _baseline: float | None = field(default=None, init=False, repr=False)
    _window: deque = field(default=None, init=False, repr=False)

    def __post_init__(self):
        self._window = deque(maxlen=self.window_size)

    def record(self, success: bool) -> None:
        self._window.append(success)
        if self._baseline is None and len(self._window) == self.window_size:
            self._baseline = self.success_rate()

    def success_rate(self) -> float:
        if not self._window:
            return 100.0
        return 100.0 * sum(self._window) / len(self._window)

    def is_drifting(self) -> bool:
        """True, если окно заполнено и просело более чем на drop_threshold_pp
        относительно базовой линии, зафиксированной по первому полному окну."""
        if self._baseline is None or len(self._window) < self.window_size:
            return False
        return (self._baseline - self.success_rate()) > self.drop_threshold_pp

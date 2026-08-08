"""Совместимая точка входа Python-адаптера TrueConf.

Vendor SDK, входящие команды и физическая отправка остаются на Python.
Планирование уведомлений выполняет отдельный Go-сервис через delivery_outbox.
"""
from __future__ import annotations

import asyncio

from services.delivery_trueconf.adapter import main


if __name__ == "__main__":
    asyncio.run(main())

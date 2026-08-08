"""Процесс тонкого Python-адаптера TrueConf.

Исходящие сообщения берутся только из delivery_outbox. Доменный planner
работает отдельным Go-сервисом.
"""
from __future__ import annotations

import asyncio
import os

from trueconf import Bot, Dispatcher
from trueconf.utils._ssl import _build_ssl_context
from trueconf.utils._token import _get_auth_token

from services.delivery_trueconf.handlers import on_health_check, router
from services.delivery_trueconf.outbox import delivery_loop


TRUECONF_SERVER = os.environ.get("TRUECONF_SERVER", "")
TRUECONF_BOT_USERNAME = os.environ.get("TRUECONF_BOT_USERNAME", "")
TRUECONF_BOT_PASSWORD = os.environ.get("TRUECONF_BOT_PASSWORD", "")
TRUECONF_HTTPS = os.environ.get("TRUECONF_HTTPS", "false").lower() == "true"
TRUECONF_PORT = int(os.environ.get("TRUECONF_PORT", "4309"))


def _require_settings() -> None:
    missing = [
        name
        for name, value in (
            ("TRUECONF_SERVER", TRUECONF_SERVER),
            ("TRUECONF_BOT_USERNAME", TRUECONF_BOT_USERNAME),
            ("TRUECONF_BOT_PASSWORD", TRUECONF_BOT_PASSWORD),
        )
        if not value
    ]
    if missing:
        raise RuntimeError(f"Не заданы обязательные настройки: {', '.join(missing)}")


async def main() -> None:
    _require_settings()
    dispatcher = Dispatcher()
    dispatcher.include_router(router)

    ssl_context = _build_ssl_context(True)
    token = _get_auth_token(
        TRUECONF_SERVER,
        TRUECONF_BOT_USERNAME,
        TRUECONF_BOT_PASSWORD,
        ssl_context=ssl_context,
        protocol="https" if TRUECONF_HTTPS else "http",
        port=TRUECONF_PORT,
    )
    if not token:
        raise RuntimeError(
            f"Не удалось получить токен для {TRUECONF_BOT_USERNAME}@{TRUECONF_SERVER}"
        )

    bot = Bot(
        TRUECONF_SERVER,
        token,
        https=TRUECONF_HTTPS,
        web_port=TRUECONF_PORT,
        verify_ssl=ssl_context,
        dispatcher=dispatcher,
        on_health_check=on_health_check,
    )
    run_task = asyncio.create_task(bot.run())
    await asyncio.wait_for(bot.authorized_event.wait(), timeout=30)
    domain = bot.me_id.split("@", 1)[1] if "@" in bot.me_id else None
    if not domain:
        raise RuntimeError(f"Не удалось определить домен из JID бота: {bot.me_id!r}")

    print(f"delivery_trueconf: adapter подключён как {bot.me_id}, домен={domain}")
    await asyncio.gather(run_task, delivery_loop(bot, domain))


if __name__ == "__main__":
    asyncio.run(main())

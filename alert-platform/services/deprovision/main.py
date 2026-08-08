"""Временный операционный worker автоотключения подписчиков.

После переноса доменного pipeline на Go LDAP-сверка больше не должна жить
побочным эффектом его TTL sweep. До переноса admin/security API она
запускается отдельно и сохраняет прежнюю fail-open семантику.
"""
from __future__ import annotations

import os
import signal
import time

from packages.common.db import get_session
from packages.common.deprovision import deactivate_departed_subscribers


INTERVAL_S = float(os.environ.get("DEPROVISION_INTERVAL_S", "30"))
_stop = False


def _handle_stop(*_args) -> None:
    global _stop
    _stop = True


def main() -> None:
    signal.signal(signal.SIGTERM, _handle_stop)
    signal.signal(signal.SIGINT, _handle_stop)
    print(f"deprovision-worker: started, interval={INTERVAL_S}s")
    while not _stop:
        with get_session() as session:
            deactivated = deactivate_departed_subscribers(session)
        if deactivated:
            print(f"deprovision-worker: deactivated={deactivated}")
        deadline = time.monotonic() + INTERVAL_S
        while not _stop and time.monotonic() < deadline:
            time.sleep(min(0.5, max(0.0, deadline - time.monotonic())))


if __name__ == "__main__":
    main()

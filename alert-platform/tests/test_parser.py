"""Тесты парсера — раздел 17.3: "Тесты обязательны для парсеров на образцах".

Образцы лежат в fixtures/parser_samples/ и получены из datagen (раздел 18.7:
генератор как источник эталона для регрессионных тестов), плюс несколько
специально сломанных сообщений для проверки деградации (И5).
"""
import json
from datetime import datetime
from pathlib import Path

import pytest

from packages.models.canonical import compute_dedup_key
from services.pipeline.parser import (ParseSuccessTracker, load_connectors,
                                       load_source_passports, parse)

ROOT = Path(__file__).parent.parent
CONNECTORS_DIR = ROOT / "connectors"
SAMPLES_DIR = ROOT / "fixtures" / "parser_samples"


@pytest.fixture(scope="module")
def connectors():
    return load_connectors(CONNECTORS_DIR)


@pytest.fixture(scope="module")
def passports():
    return load_source_passports(CONNECTORS_DIR / "sources.yaml")


def _load_jsonl(path: Path) -> list[dict]:
    return [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line]


@pytest.mark.parametrize("system", ["zabbix", "solarwinds"])
def test_valid_samples_parse_correctly(connectors, passports, system):
    samples = _load_jsonl(SAMPLES_DIR / f"{system}.jsonl")
    assert samples, f"нет образцов для {system}"
    connector = connectors[system]

    for sample in samples:
        passport = passports[sample["source_instance"]]
        result = parse(connector, sample["raw_body"], sample["source_instance"],
                        received_at=datetime(2026, 8, 6, 12, 0, 0), site=passport["site"])

        assert result.success, f"не разобралось: {sample['raw_body']!r} ({result.error})"
        expected = sample["expected"]
        assert result.event.state == expected["state"]
        assert result.event.symptom_class == expected["symptom_class"]
        assert result.event.entity.component == expected["component"]
        # dedup_key на этой стадии провизорный (по сырому имени, не по object_id CMDB),
        # но должен вычисляться, если известны site и сырое имя объекта.
        assert result.event.dedup_key is not None
        # И2: оригинал не перефразирован — raw_body передан дословно.
        assert result.event.body_raw == sample["raw_body"]


def test_malformed_messages_fail_without_losing_raw_body(connectors, passports):
    samples = _load_jsonl(SAMPLES_DIR / "malformed.jsonl")
    assert samples

    for sample in samples:
        passport = passports.get(sample["source_instance"])
        system = passport["system"] if passport else "zabbix"
        connector = connectors[system]
        result = parse(connector, sample["raw_body"], sample["source_instance"],
                        received_at=datetime(2026, 8, 6, 12, 0, 0),
                        site=passport["site"] if passport else None)

        assert not result.success
        # И5: раз не разобрали — сырое тело всё равно доступно для резервной доставки.
        assert result.raw_body == sample["raw_body"]
        assert result.event is None


def test_unknown_source_instance_still_parses_without_site(connectors):
    connector = connectors["zabbix"]
    raw = ("PROBLEM: ghost-77: Unavailable by ICMP ping\n"
           "Host: ghost-77 (10.250.1.2)\nSeverity: High\nTime: 2026.08.06 03:35:21")
    result = parse(connector, raw, "zbx-unregistered-01",
                    received_at=datetime(2026, 8, 6, 12, 0, 0), site=None)

    assert result.success
    assert result.event.entity.site is None
    # без площадки провизорный dedup_key не строится — карантинный случай (раздел 4.3 п.6)
    assert result.event.dedup_key is None


def test_dedup_key_matches_spec_formula():
    key = compute_dedup_key("brd-noyabrsk", "app-01", "C:", "disk_space")
    assert key == compute_dedup_key("brd-noyabrsk", "app-01", "C:", "disk_space")
    assert key != compute_dedup_key("brd-khantos", "app-01", "C:", "disk_space")
    assert compute_dedup_key(None, "app-01", "C:", "disk_space") is None
    assert compute_dedup_key("brd-noyabrsk", None, "C:", "disk_space") is None


def test_drift_tracker_flags_success_rate_drop():
    tracker = ParseSuccessTracker(window_size=50, drop_threshold_pp=5.0)
    for _ in range(50):
        tracker.record(True)
    assert not tracker.is_drifting()
    assert tracker.success_rate() == 100.0

    for _ in range(10):
        tracker.record(False)
    assert tracker.is_drifting()

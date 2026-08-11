# 1. Техническая реализация решения (прототип)

Вес критерия — 0.20. Честная самооценка — 5/5 (см. полное обоснование
и историю проверок в `../../alert-platform/COMPLIANCE_MATRIX.md`, раздел 1).

| Требование (планка 5) | Реализация | Доказательство | Тест | Показываем на защите | Пробел |
|---|---|---|---|---|---|
| Конфигурируемое подключение источников | `POST /api/v1/ingest/raw` + `connectors/*.yaml` + БД-реестр `/sources` | `go-platform/internal/gateway/`, `connectors/zabbix.yaml`, `connectors/solarwinds.yaml` | Живой аудит: реальный источник `zbx-brd-noyabrsk-01`, корректный `X-Source-Token` → `202`, без токена/с неверным → `401` | `/console/sources` — живые инстансы с токенами | нет |
| Self-service подписки | Личный кабинет `/me/{token}` — токен выдаёт бот по `/кабинет` | `internal/adminapi/` личный кабинет, `web/src/pages/EmployeeDetail.tsx` (подписки) | Изменение подписки без администратора, следующее событие маршрутизируется по новому правилу | Один непрерывный поток «подписался → пришло именно поэтому» | нет |
| Настраиваемая маршрутизация | Группы + зона ответственности по CMDB + сценарии (граф condition→notify→wait→ack_check→escalation) + SLA-каскад | `internal/scenario/engine.go`, `internal/planner/automation.go`, `web/src/pages/ScenarioEditor.tsx` | Реальная P0-проблема → 2 уведомления двум группам за 8с, `scenario_runs` корректно продвинулся | `/console/scenarios` — визуальный редактор графа, живой прогон в `/console/scenarios/:id/stats` | нет |
| Интеграция с TrueConf | Двусторонняя: outbox→`python-trueconf-bot` исходящие, `/старт /кабинет /алерты /сводка`, reply-ACK, `анализ` входящие | `services/delivery_trueconf/{outbox,handlers,adapter}.py` | Живая доставка `notifications.status='sent'` с реальным `chat_id`/`message_id` | Реальный TrueConf-чат с ботом на защите | нет |
| Дашборд администратора | React SPA: аналитика, инциденты, алерты, оборудование, сотрудники (дерево), сценарии, SLA, интеграции, аудит, ADP AI | `web/src/pages/`, `go-platform/internal/adminapi/` | `curl` со свежей LDAP-сессией admin1 по всем ключевым эндпоинтам — все `200` | Живой обход разделов консоли, включая провал от филиала до конкретного PLC/коммутатора/сервера | нет |
| Второй канал доставки (Email, опционально по кейсу, но конфигурируемо) | `delivery-email` (Go) — тот же тонкий принцип, что у TrueConf, свой claim/retry-луп на `delivery_outbox`; open-пиксель + click-редирект с сервер-side токеном | `go-platform/internal/deliveryemail/`, `internal/adminapi/email_tracking.go` | Живой E2E-тест: P0-алерт → TrueConf+Email → письмо в MailHog → tracking-URL → `hit_count` инкрементирован | `/console/integrations` — живая % доставки; MailHog UI на защите | нет |
| Оборудование как дерево + операционная аналитика | Раздел «Оборудование» — inline-раскрываемое дерево, лениво грузит уровни, поиск раскрывает путь; «Аналитика» — 7 backend-эндпоинтов, графики вместо сырых чисел | `web/src/pages/Equipment.tsx`, `Analytics.tsx`, `go-platform/internal/adminapi/analytics_*.go` | Поиск `dc-01` находит 3 объекта в разных филиалах с верным путём | `/console/equipment` — раскрыть филиал→категорию→объект | нет |
| Работа в реальном времени | Конвейер синхронный: приём→доставка — секунды | `go-platform/internal/pipeline/service.go` | Живой end-to-end тест: сигнал → проблема → `notification.status='sent'` за <10с одним прогоном | Тот же тест воспроизводится вживую при проверяющем | нет |
| Оценена производительность (дословно из планки) | Полный синтетический прогон 5302 события через реальный стек | `../../alert-platform/INFRASTRUCTURE.md` §1 | 2,5 события/с на 1 реплике воркера — 40× запас к целевым 5400/сутки | `/console/analytics` + `INFRASTRUCTURE.md` | нет |

## Что дополнительно появилось после первой версии матрицы

- **Сотрудники как дерево организации** (не карточки): `go-platform/internal/adminapi/org_units.go`,
  `web/src/pages/Employees.tsx` — произвольная глубина иерархии (филиал→отдел→сотрудник
  для инженеров, филиал→руководство→сотрудник для руководителей — не
  зафиксированные 4 уровня), агрегаты доступности на каждом узле.
- **Инциденты**: вкладки Активные/Завершённые/Все с живыми счётчиками
  и живой длительностью открытого инцидента — `web/src/pages/Incidents.tsx`,
  `go-platform/internal/adminapi/server.go::listIncidents`.
- **ADP AI** — см. папку `../3_Использование_инструментов_ИИ/` этого же навигатора.

## Ссылки

- Полная матрица с исходными доказательствами: `../../alert-platform/COMPLIANCE_MATRIX.md`
- Архитектура: `../../alert-platform/ARCHITECTURE.md`
- Спецификация продукта: `../../ALERT-PLATFORM-SPEC.md`

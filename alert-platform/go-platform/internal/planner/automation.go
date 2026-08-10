package planner

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/hhanqq/g-v-p/alert-platform/go-platform/internal/scenario"
	"github.com/jackc/pgx/v5"
)

type activeScenario struct {
	id        string
	name      string
	graphJSON string
	version   int
}

func (planner *Planner) planScenarios(ctx context.Context) error {
	rows, err := planner.pool.Query(ctx, `SELECT id::text,name,graph_json,version FROM scenarios WHERE status='active' ORDER BY id`)
	if err != nil {
		return err
	}
	var scenarios []activeScenario
	for rows.Next() {
		var item activeScenario
		if err := rows.Scan(&item.id, &item.name, &item.graphJSON, &item.version); err != nil {
			rows.Close()
			return err
		}
		scenarios = append(scenarios, item)
	}
	rows.Close()
	if len(scenarios) > 0 {
		rows, err = planner.pool.Query(ctx, `SELECT id FROM problems WHERE status IN ('OPEN','FLAPPING') ORDER BY id`)
		if err != nil {
			return err
		}
		var problemIDs []int64
		for rows.Next() {
			var id int64
			_ = rows.Scan(&id)
			problemIDs = append(problemIDs, id)
		}
		rows.Close()
		// Кэш разобранных графов версий на этот тик — избегает повторного
		// SELECT+Parse одной и той же исторической версии для каждого
		// проблемного ряда прогона, стоящего в wait на этой версии.
		versions := make(map[[2]int64]*scenario.Graph)
		for _, definition := range scenarios {
			currentGraph, ok := scenario.Parse(definition.graphJSON)
			if !ok {
				continue
			}
			scenarioID, _ := strconv.ParseInt(definition.id, 10, 64)
			versions[[2]int64{scenarioID, int64(definition.version)}] = currentGraph
			for _, problemID := range problemIDs {
				if err := planner.advanceScenario(ctx, scenarioID, definition.name, definition.version, currentGraph, versions, problemID); err != nil {
					return fmt.Errorf("scenario=%d problem=%d: %w", scenarioID, problemID, err)
				}
			}
		}
	}
	// Прогон, чья проблема перестала быть открытой (резолвнулась, пока
	// прогон стоял, например, в wait), больше никогда не попадает в
	// problemIDs выше — без этой подчистки он навсегда застревал бы в
	// status='running', даже не пытаясь дойти до терминального состояния.
	// Найдено живьём при аудите (Этап 0), не было в исходном плане.
	_, err = planner.pool.Exec(ctx, `
		UPDATE scenario_runs SET status='done'
		WHERE status='running' AND problem_id IN (SELECT id FROM problems WHERE status NOT IN ('OPEN','FLAPPING'))`)
	return err
}

// resolveScenarioGraph возвращает граф, под которым нужно продолжать
// УЖЕ существующий прогон — версию, закреплённую за ним при создании
// (run.scenario_version), а не текущий (возможно, с тех пор
// отредактированный) граф сценария. currentGraph/currentVersion —
// быстрый путь, когда прогон ещё не отстал от живой версии.
func (planner *Planner) resolveScenarioGraph(ctx context.Context, scenarioID int64, currentVersion int, currentGraph *scenario.Graph, versions map[[2]int64]*scenario.Graph, runVersion int) (*scenario.Graph, error) {
	if runVersion == currentVersion {
		return currentGraph, nil
	}
	key := [2]int64{scenarioID, int64(runVersion)}
	if cached, ok := versions[key]; ok {
		return cached, nil
	}
	var graphJSON string
	if err := planner.pool.QueryRow(ctx, `SELECT graph_json FROM scenario_versions WHERE scenario_id=$1 AND version=$2`, scenarioID, runVersion).Scan(&graphJSON); err != nil {
		return nil, err
	}
	graph, ok := scenario.Parse(graphJSON)
	if !ok {
		// Не должно происходить — снэпшот пишется из уже провалидированного
		// (Parse-проверенного на активации) графа. Явная ошибка, а не
		// тихое зависание прогона, если это всё же случится.
		return nil, fmt.Errorf("pinned graph_json for scenario=%d version=%d failed to parse", scenarioID, runVersion)
	}
	versions[key] = graph
	return graph, nil
}

// advanceScenario держит ОДНУ транзакцию на пару (scenarioID,problemID)
// от чтения строки прогона до записи её нового состояния и создания
// доставки включительно — SELECT ... FOR UPDATE SKIP LOCKED на этой
// строке закрывает гонку двух реплик планировщика: раньше SELECT и
// UPDATE были разделены, оба реплика читали один и тот же устаревший
// notified_count и оба пытались создать доставку с одним и тем же
// ключом идемпотентности. 0 строк после SKIP LOCKED — не ошибка, это
// значит, что другой реплика в этот самый момент уже держит строку;
// тихо откладываем до следующего тика (самоисцеляется, тот же принцип,
// что у остальных очередей проекта).
func (planner *Planner) advanceScenario(ctx context.Context, scenarioID int64, scenarioName string, currentVersion int, currentGraph *scenario.Graph, versions map[[2]int64]*scenario.Graph, problemID int64) error {
	problem, err := planner.loadProblem(ctx, problemID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()

	tx, err := planner.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var runID int64
	var current, status string
	var enteredAt time.Time
	var notified int
	var runVersion int
	err = tx.QueryRow(ctx, `SELECT id,current_node_id,status,step_entered_at,notified_count,scenario_version FROM scenario_runs WHERE scenario_id=$1 AND problem_id=$2 FOR UPDATE SKIP LOCKED`, scenarioID, problemID).Scan(&runID, &current, &status, &enteredAt, &notified, &runVersion)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	graph := currentGraph
	if errors.Is(err, pgx.ErrNoRows) {
		// 0 строк здесь значит ЛИБО прогона ещё нет, ЛИБО другой реплика
		// прямо сейчас держит его лок — оба случая безопасно сходятся к
		// одному пути: попытка создать (ON CONFLICT DO NOTHING — не
		// гонка сама с собой, если строка уже существует), затем то же
		// самое SKIP LOCKED ещё раз. Условие решает только создание
		// НОВОГО прогона — уже идущий прогон продолжается независимо от
		// того, матчится ли текущее (возможно отредактированное) условие,
		// он живёт по контракту своей закреплённой версии, не живой.
		condition := currentGraph.Nodes[currentGraph.RootID]
		if !planner.matchesScenarioCondition(ctx, problem, condition.Data) {
			return nil
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO scenario_runs(scenario_id,problem_id,current_node_id,status,step_entered_at,created_at,notified_count,scenario_version)
			VALUES($1,$2,$3,'running',$4,$4,0,$5) ON CONFLICT(scenario_id,problem_id) DO NOTHING`,
			scenarioID, problemID, currentGraph.RootID, now, currentVersion)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `SELECT id,current_node_id,status,step_entered_at,notified_count,scenario_version FROM scenario_runs WHERE scenario_id=$1 AND problem_id=$2 FOR UPDATE SKIP LOCKED`, scenarioID, problemID).Scan(&runID, &current, &status, &enteredAt, &notified, &runVersion)
		if errors.Is(err, pgx.ErrNoRows) {
			return tx.Commit(ctx)
		}
		if err != nil {
			return err
		}
	} else {
		graph, err = planner.resolveScenarioGraph(ctx, scenarioID, currentVersion, currentGraph, versions, runVersion)
		if err != nil {
			return err
		}
	}
	if status != "running" {
		return tx.Commit(ctx)
	}
	recipients, err := planner.resolveRecipients(ctx, problem)
	if err != nil {
		return err
	}
	facts := make(map[string]bool)
	for id, node := range graph.Nodes {
		switch node.Type {
		case "ack_check":
			facts[id] = problem.AcknowledgedAt != nil
		case "subscription_check":
			groupID := int64(number(node.Data["group_id"]))
			employeeID := int64(number(node.Data["employee_id"]))
			if groupID != 0 {
				members, err := planner.groupMemberUsernames(ctx, groupID)
				if err != nil {
					return err
				}
				facts[id] = len(members) > 0
			} else if employeeID == 0 {
				facts[id] = len(recipients) > 0
			} else {
				var username string
				err := tx.QueryRow(ctx, `SELECT trueconf_username FROM subscribers WHERE id=$1 AND active=TRUE`, employeeID).Scan(&username)
				if err == nil {
					facts[id] = contains(recipients, username)
				} else if !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
			}
		}
	}
	outcome := scenario.Advance(current, enteredAt, graph, problemStatus(problem), facts, now)
	if err := recordScenarioSteps(ctx, tx, runID, scenarioID, runVersion, problemID, outcome.Trace, now); err != nil {
		return err
	}
	switch outcome.Kind {
	case "wait":
		if _, err = tx.Exec(ctx, `UPDATE scenario_runs SET current_node_id=$2,step_entered_at=$3 WHERE id=$1`, runID, outcome.CurrentNodeID, outcome.EnteredAt); err != nil {
			return err
		}
		return tx.Commit(ctx)
	case "done":
		if _, err = tx.Exec(ctx, `UPDATE scenario_runs SET status='done',current_node_id=$2 WHERE id=$1`, runID, outcome.CurrentNodeID); err != nil {
			return err
		}
		return tx.Commit(ctx)
	case "notify":
		nextStatus := "running"
		if outcome.CurrentNodeID == "" {
			nextStatus = "done"
		}
		groupID := int64(number(outcome.Step.Data["group_id"]))
		var usernames []string
		if groupID != 0 {
			usernames, err = planner.groupMemberUsernames(ctx, groupID)
			if err != nil {
				return err
			}
		} else if employeeID := int64(number(outcome.Step.Data["employee_id"])); employeeID != 0 {
			var username string
			if err := tx.QueryRow(ctx, `SELECT trueconf_username FROM subscribers WHERE id=$1 AND active=TRUE`, employeeID).Scan(&username); err == nil {
				usernames = []string{username}
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		if len(usernames) == 0 {
			// Раньше состояние продвигалось (и notified_count рос) ещё до
			// резолвинга получателя, так что отсутствие подписчика тонуло
			// молча — прогон выглядел так, будто уведомил, хотя ни одной
			// доставки не создавалось. Явную ветку графа для этого случая
			// добавляет отдельный узел (см. следующий этап); здесь —
			// минимум: не считать это отправкой и оставить видимый след.
			if _, err = tx.Exec(ctx, `UPDATE scenario_runs SET status=$2,current_node_id=$3,step_entered_at=$4 WHERE id=$1`, runID, nextStatus, outcome.CurrentNodeID, outcome.EnteredAt); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO audit_log(actor,action,target,detail,created_at) VALUES('scheduler','scenario_no_recipient',$1,$2,$3)`,
				fmt.Sprintf("scenario:%d:problem:%d", scenarioID, problemID), fmt.Sprintf("node=%s", outcome.Step.ID), now); err != nil {
				return err
			}
			return tx.Commit(ctx)
		}
		if _, err = tx.Exec(ctx, `UPDATE scenario_runs SET status=$2,current_node_id=$3,step_entered_at=$4,notified_count=notified_count+1 WHERE id=$1`, runID, nextStatus, outcome.CurrentNodeID, outcome.EnteredAt); err != nil {
			return err
		}
		for _, username := range usernames {
			if _, err = execCreateDelivery(ctx, tx, delivery{
				ProblemID: problemID, Type: "SCENARIO", Recipient: username,
				ChatID:            fmt.Sprintf("scenario:%d:%d:%s", scenarioID, notified+1, username),
				Text:              RenderScenario(problem, scenarioName, notified > 0),
				IdempotencySuffix: fmt.Sprintf("scenario:%d:notification:%d", scenarioID, notified+1),
			}); err != nil {
				return err
			}
		}
		return tx.Commit(ctx)
	}
	return tx.Commit(ctx)
}

// recordScenarioSteps пишет трассу реально пройденных узлов (пусто для
// тиков, где прогон просто ещё ждёт) — раздел «Аналитика исполнения
// сценариев»: без этой таблицы у прогона переживает только текущий
// узел, восстановить путь целиком нельзя.
func recordScenarioSteps(ctx context.Context, dbc dbTx, runID, scenarioID int64, version int, problemID int64, trace []scenario.StepTrace, now time.Time) error {
	for _, step := range trace {
		if _, err := dbc.Exec(ctx, `
			INSERT INTO scenario_run_steps(run_id,scenario_id,scenario_version,problem_id,node_id,node_type,branch,entered_at,created_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			runID, scenarioID, version, problemID, step.NodeID, step.NodeType, step.Branch, now, now); err != nil {
			return err
		}
	}
	return nil
}

func (planner *Planner) matchesScenarioCondition(ctx context.Context, problem ProblemData, data map[string]any) bool {
	if priority, _ := data["priority_min"].(string); priority != "" {
		if rank, ok := priorityRank(problem.Priority); !ok {
			return false
		} else if minRank, ok := priorityRank(&priority); !ok || rank > minRank {
			return false
		}
	}
	if symptom, _ := data["symptom_class"].(string); symptom != "" && symptom != problem.SymptomClass {
		return false
	}
	if objectID, _ := data["object_id"].(string); objectID != "" {
		if problem.ObjectID == nil || *problem.ObjectID != objectID {
			return false
		}
	}
	if equipmentType, _ := data["equipment_type"].(string); equipmentType != "" {
		if problem.EquipmentType == nil || *problem.EquipmentType != equipmentType {
			return false
		}
	}
	if subsidiary, _ := data["subsidiary"].(string); subsidiary != "" {
		if problem.ObjectID == nil {
			return false
		}
		var exists bool
		_ = planner.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM cmdb_ownership WHERE object_id=$1 AND subsidiary=$2 UNION ALL SELECT 1 FROM cmdb_ownership ownership JOIN cmdb_service_objects service_object ON service_object.service_id=ownership.service_id WHERE service_object.object_id=$1 AND ownership.subsidiary=$2)`, *problem.ObjectID, subsidiary).Scan(&exists)
		return exists
	}
	return true
}

func (planner *Planner) planSLABreaches(ctx context.Context) error {
	rows, err := planner.pool.Query(ctx, `SELECT id FROM problems WHERE status IN ('OPEN','FLAPPING') AND priority IS NOT NULL AND NOT EXISTS(SELECT 1 FROM sla_breach_notices notice WHERE notice.problem_id=problems.id)`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		problem, err := planner.loadProblem(ctx, id)
		if err != nil {
			return err
		}
		var ruleID int64
		var ruleName string
		var threshold int
		err = planner.pool.QueryRow(ctx, `
			SELECT rule.id,rule.name,rule.response_minutes FROM sla_rules rule
			WHERE rule.priority=$1
			AND (rule.subsidiary IS NULL OR EXISTS(SELECT 1 FROM cmdb_ownership ownership WHERE ownership.object_id=$2 AND ownership.subsidiary=rule.subsidiary UNION ALL SELECT 1 FROM cmdb_ownership ownership JOIN cmdb_service_objects service_object ON service_object.service_id=ownership.service_id WHERE service_object.object_id=$2 AND ownership.subsidiary=rule.subsidiary))
			AND (rule.service_id IS NULL OR EXISTS(SELECT 1 FROM cmdb_service_objects WHERE object_id=$2 AND service_id=rule.service_id))
			ORDER BY ((rule.subsidiary IS NOT NULL)::int+(rule.service_id IS NOT NULL)::int) DESC,rule.id LIMIT 1`, problem.Priority, problem.ObjectID).Scan(&ruleID, &ruleName, &threshold)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		age := int(time.Since(problem.OpenedAt).Minutes())
		if age < threshold {
			continue
		}
		var noticeID int64
		err = planner.pool.QueryRow(ctx, `INSERT INTO sla_breach_notices(problem_id,sla_rule_id,created_at) VALUES($1,$2,$3) ON CONFLICT(problem_id) DO NOTHING RETURNING id`, id, ruleID, time.Now().UTC()).Scan(&noticeID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		recipients, err := planner.resolveRecipients(ctx, problem)
		if err != nil {
			return err
		}
		for _, recipient := range recipients {
			_, err = planner.createDelivery(ctx, delivery{ProblemID: id, Type: "SLA_BREACH", Recipient: recipient, ChatID: "sla:" + recipient, Text: RenderSLABreach(problem, ruleName, age, threshold), IdempotencySuffix: fmt.Sprintf("notice:%d", noticeID)})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func number(value any) float64 {
	switch item := value.(type) {
	case float64:
		return item
	case int:
		return float64(item)
	default:
		return 0
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func problemStatus(problem ProblemData) string {
	if problem.ResolvedAt.IsZero() {
		return "OPEN"
	}
	return "RESOLVED"
}

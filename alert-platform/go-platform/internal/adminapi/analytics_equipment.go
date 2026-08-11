package adminapi

import (
	"database/sql"
	"net/http"
)

// analyticsEquipmentTop — GET /api/analytics/equipment-top?period=.
// Раздел VIII.34-35 ТЗ: топ проблемного оборудования (со ссылкой на
// дерево — object_id уже несёт site/equipment_type, фронтенд строит
// путь) и топ symptom_class, оба на реальных Problem за период.
func (server *Server) analyticsEquipmentTop(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	ctx := request.Context()
	rng := parseAnalyticsRange(request)

	topObjects := make([]map[string]any, 0)
	rows, err := server.pool.Query(ctx, `
		SELECT problem.object_id, COUNT(*), COALESCE(object.name,problem.object_id), object.site, object.equipment_type
		FROM problems problem LEFT JOIN cmdb_objects object ON object.id=problem.object_id
		WHERE problem.object_id IS NOT NULL AND problem.opened_at >= $1 AND problem.opened_at < $2
		GROUP BY problem.object_id, object.name, object.site, object.equipment_type
		ORDER BY COUNT(*) DESC LIMIT 10`, rng.From, rng.To)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	for rows.Next() {
		var objectID, name string
		var count int64
		var site, equipmentType sql.NullString
		if err := rows.Scan(&objectID, &count, &name, &site, &equipmentType); err != nil {
			rows.Close()
			writeError(response, http.StatusInternalServerError, "scan top equipment")
			return
		}
		topObjects = append(topObjects, map[string]any{
			"object_id": objectID, "count": count, "name": name,
			"site": nullableString(site), "equipment_type": nullableString(equipmentType),
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeError(response, http.StatusInternalServerError, "load top equipment")
		return
	}
	rows.Close()

	topSymptoms := make([]map[string]any, 0)
	rows, err = server.pool.Query(ctx, `
		SELECT symptom_class, count(*) FROM problems
		WHERE opened_at >= $1 AND opened_at < $2
		GROUP BY symptom_class ORDER BY count(*) DESC LIMIT 8`, rng.From, rng.To)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	for rows.Next() {
		var symptom string
		var count int64
		if err := rows.Scan(&symptom, &count); err != nil {
			rows.Close()
			writeError(response, http.StatusInternalServerError, "scan top symptoms")
			return
		}
		topSymptoms = append(topSymptoms, map[string]any{"symptom_class": symptom, "count": count})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeError(response, http.StatusInternalServerError, "load top symptoms")
		return
	}
	rows.Close()

	writeJSON(response, http.StatusOK, map[string]any{"top_objects": topObjects, "top_symptoms": topSymptoms})
}

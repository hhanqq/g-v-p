package adminapi

import (
	"database/sql"
	"net/http"
	"strings"
)

// equipmentSearchResult — раздел I.6 ТЗ: поиск должен раскрывать путь до
// найденного объекта независимо от вложенности, а lazy-tree по построению
// не имеет всего дерева на клиенте — поэтому путь (site/equipment_type)
// возвращает сервер, а не вычисляется на фронтенде.
type equipmentSearchResult struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Site          string  `json:"site"`
	SiteLabel     string  `json:"site_label"`
	EquipmentType *string `json:"equipment_type"`
	CategoryLabel *string `json:"category_label"`
}

// equipmentSearch — GET /api/equipment/search?q=. Ищет по имени, id, FQDN
// и IP, регистронезависимо, подстрокой. Лимит 20 — это подсказка для
// раскрытия пути в дереве, не полноценный листинг.
func (server *Server) equipmentSearch(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	query := strings.TrimSpace(request.URL.Query().Get("q"))
	if query == "" {
		writeJSON(response, http.StatusOK, []equipmentSearchResult{})
		return
	}
	pattern := "%" + query + "%"
	rows, err := server.pool.Query(request.Context(), `
		SELECT id, name, site, equipment_type
		FROM cmdb_objects
		WHERE id ILIKE $1 OR name ILIKE $1 OR fqdn ILIKE $1 OR ip ILIKE $1
		ORDER BY name LIMIT 20`, pattern)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	defer rows.Close()
	results := make([]equipmentSearchResult, 0)
	for rows.Next() {
		var id, name, site string
		var equipmentType sql.NullString
		if err := rows.Scan(&id, &name, &site, &equipmentType); err != nil {
			writeError(response, http.StatusInternalServerError, "scan search results")
			return
		}
		result := equipmentSearchResult{ID: id, Name: name, Site: site, SiteLabel: groupLabel(siteLabels, site)}
		if equipmentType.Valid {
			result.EquipmentType = &equipmentType.String
			label := groupLabel(equipmentTypeLabels, equipmentType.String)
			result.CategoryLabel = &label
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		writeError(response, http.StatusInternalServerError, "load search results")
		return
	}
	writeJSON(response, http.StatusOK, results)
}

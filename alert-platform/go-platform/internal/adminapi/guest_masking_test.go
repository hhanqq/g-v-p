package adminapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMaskPersonName(t *testing.T) {
	if got := maskPersonName("Алексей Иванов"); got != "Алексей И*****" {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(maskPersonName("Алексей Иванов"), "Иванов") {
		t.Fatal("surname must not leak in full")
	}
}

func TestMaskUsername(t *testing.T) {
	got := maskUsername("ivanov.an")
	if !strings.HasSuffix(got, ".an") {
		t.Fatalf("initials suffix must survive, got %q", got)
	}
	if strings.Contains(got, "ivanov") {
		t.Fatal("surname must not leak in full")
	}
}

func TestMaskEmail(t *testing.T) {
	got := maskEmail("ivanov@company.local")
	if !strings.HasSuffix(got, "@company.local") {
		t.Fatalf("domain must survive, got %q", got)
	}
	if strings.Contains(got, "ivanov") {
		t.Fatal("local part must not leak in full")
	}
}

func TestMaskIP(t *testing.T) {
	if got := maskIP("10.42.8.17"); got != "10.42.*.*" {
		t.Fatalf("got %q", got)
	}
}

func TestMaskEquipmentIdentifier(t *testing.T) {
	got := maskEquipmentIdentifier("sw-brd-noyabrsk-acc-02")
	if !strings.HasPrefix(got, "sw-") {
		t.Fatalf("device-type prefix must survive, got %q", got)
	}
	if strings.Contains(got, "noyabrsk") {
		t.Fatal("site name must not leak in full")
	}
}

func TestMaskFQDN(t *testing.T) {
	got := maskFQDN("sw-brd-04.company.local")
	if !strings.HasSuffix(got, ".local") {
		t.Fatalf("tld must survive, got %q", got)
	}
	if strings.Contains(got, "company") {
		t.Fatal("domain label must not leak in full")
	}
}

// TestMaskGuestJSONHidesEmployeeFields — раздел «Маскирование данных в
// Guest Mode»: даже без знания, какой хендлер произвёл JSON, ключи
// full_name/trueconf_username/email маскируются.
func TestMaskGuestJSONHidesEmployeeFields(t *testing.T) {
	raw := `[{"id":8,"full_name":"Алексей Иванов","trueconf_username":"ivanov.an","email":"ivanov@company.local","position":"Инженер"}]`
	masked := maskGuestJSON([]byte(raw))
	var items []map[string]any
	if err := json.Unmarshal(masked, &items); err != nil {
		t.Fatalf("masked output is not valid JSON: %v", err)
	}
	if strings.Contains(string(masked), "Иванов") || strings.Contains(string(masked), "ivanov") {
		t.Fatalf("raw surname leaked into masked response: %s", masked)
	}
	// numeric id/position (не в списке маскируемых полей и не запись CMDB) должны выжить как есть
	if items[0]["id"] != float64(8) {
		t.Fatalf("non-sensitive numeric id must survive untouched, got %v", items[0]["id"])
	}
	if items[0]["position"] != "Инженер" {
		t.Fatalf("generic role/position must remain visible, got %v", items[0]["position"])
	}
}

// TestMaskGuestJSONHidesEquipmentIdentityButKeepsAggregates — раздел
// «Маскирование данных в Guest Mode»: id/name оборудования маскируются
// ТОЛЬКО когда запись структурно похожа на CMDB-объект (соседний
// equipment_type/site+fqdn) — priority/status/count у incident-подобных
// записей не задевает эту эвристику и остаётся видимым.
func TestMaskGuestJSONHidesEquipmentIdentityButKeepsAggregates(t *testing.T) {
	raw := `{
		"id": "sw-brd-noyabrsk-acc-02", "name": "sw-brd-noyabrsk-acc-02",
		"site": "brd-noyabrsk", "equipment_type": "network",
		"fqdn": "sw-brd-04.company.local", "ip": "10.42.8.17"
	}`
	masked := maskGuestJSON([]byte(raw))
	var record map[string]any
	if err := json.Unmarshal(masked, &record); err != nil {
		t.Fatalf("masked output is not valid JSON: %v", err)
	}
	if record["id"] == "sw-brd-noyabrsk-acc-02" {
		t.Fatal("equipment id must be masked when structurally an equipment record")
	}
	if record["site"] != "brd-noyabrsk" {
		t.Fatalf("site (aggregate-level info) must remain visible, got %v", record["site"])
	}

	incidentRaw := `{"id": 231, "priority": "P1", "status": "OPEN", "member_count": 3}`
	maskedIncident := maskGuestJSON([]byte(incidentRaw))
	var incident map[string]any
	if err := json.Unmarshal(maskedIncident, &incident); err != nil {
		t.Fatalf("masked incident output is not valid JSON: %v", err)
	}
	if incident["id"] != float64(231) || incident["status"] != "OPEN" {
		t.Fatalf("incident record (not equipment-shaped) must survive untouched, got %v", incident)
	}
}

func TestMaskGuestJSONPassesThroughNonJSONBody(t *testing.T) {
	if got := maskGuestJSON([]byte("")); string(got) != "" {
		t.Fatalf("empty body must pass through unchanged, got %q", got)
	}
	if got := maskGuestJSON([]byte("not json")); string(got) != "not json" {
		t.Fatalf("non-JSON body must pass through unchanged, got %q", got)
	}
}

// TestGuestMaskingWriterAppliesOnFlush — конец-в-конец на уровне
// http.ResponseWriter: хендлер пишет тело как обычно (writeJSON), а то,
// что реально долетает до клиента после flush(), уже маскировано.
func TestGuestMaskingWriterAppliesOnFlush(t *testing.T) {
	recorder := httptest.NewRecorder()
	masking := newGuestMaskingWriter(recorder)
	writeJSON(masking, http.StatusOK, map[string]any{
		"full_name": "Алексей Иванов", "email": "ivanov@company.local",
	})
	masking.flush()

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "Иванов") {
		t.Fatalf("raw value must never reach the underlying ResponseWriter: %s", body)
	}
	if !strings.Contains(body, "И*") {
		t.Fatalf("expected masked marker in response body: %s", body)
	}
}

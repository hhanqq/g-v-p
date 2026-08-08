package planner

import (
	"strings"
	"testing"
	"time"
)

func ptr[T any](value T) *T { return &value }

func TestRenderNewPreservesOriginal(t *testing.T) {
	original := "PROBLEM: Well-4 PLC unreachable\nHost: well4-plc-01 (10.42.8.17)\nSeverity: Disaster"
	incidentID := int64(42)
	text := RenderNew(ProblemData{
		ID:           1,
		IncidentID:   &incidentID,
		Priority:     ptr("P0"),
		ObjectName:   "well4-plc-01",
		Site:         ptr("БРД-Ноябрьск"),
		ServiceName:  ptr("АСУ ТП куста №4"),
		SymptomClass: "host_unreachable",
		SourceSystem: "zabbix",
		OriginalBody: original,
	})
	if !strings.Contains(text, original) {
		t.Fatalf("original body was changed: %s", text)
	}
	if !strings.Contains(text, "🔴 <b>P0 · INC-0042</b>") {
		t.Fatalf("missing compatible header: %s", text)
	}
}

func TestRenderClosureAndDurationParity(t *testing.T) {
	opened := time.Date(2026, 8, 6, 3, 39, 3, 0, time.UTC)
	resolved := time.Date(2026, 8, 6, 3, 41, 8, 0, time.UTC)
	text := RenderClosure(ProblemData{
		ID:                     1,
		OpenedAt:               opened,
		ResolvedAt:             resolved,
		ClosedByReconciliation: true,
	})
	if !strings.Contains(text, "2 мин 5 с") || !strings.Contains(text, "автоматически по таймауту") {
		t.Fatalf("unexpected closure: %s", text)
	}
	if FormatDuration(3725*time.Second) != "1 ч 2 мин" {
		t.Fatalf("duration parity failed")
	}
}

func TestResolveRecipientsMatchesPythonRules(t *testing.T) {
	subsidiaries := map[string]struct{}{"gpn-noyabrsk": {}}
	services := map[string]struct{}{"svc-drilling": {}}
	subscriptions := []Subscription{
		{Username: "noc"},
		{Username: "owner", Subsidiary: ptr("gpn-noyabrsk")},
		{Username: "stranger", Subsidiary: ptr("gpn-khantos")},
		{Username: "p2", PriorityThreshold: ptr("P2")},
	}
	if got := ResolveRecipients(subsidiaries, services, ptr("P3"), subscriptions); strings.Join(got, ",") != "noc,owner" {
		t.Fatalf("P3 recipients = %v", got)
	}
	if got := ResolveRecipients(subsidiaries, services, ptr("P0"), subscriptions); strings.Join(got, ",") != "noc,owner,p2" {
		t.Fatalf("P0 recipients = %v", got)
	}
}

func TestResolveRecipientsCombinesFiltersAndDeduplicates(t *testing.T) {
	subsidiaries := map[string]struct{}{"owner-a": {}}
	services := map[string]struct{}{"svc-a": {}}
	rows := []Subscription{
		{Username: "same", Subsidiary: ptr("owner-a")},
		{Username: "same", ServiceID: ptr("svc-a")},
		{Username: "both", Subsidiary: ptr("owner-a"), ServiceID: ptr("svc-a")},
		{Username: "wrong-service", ServiceID: ptr("svc-b")},
	}
	if got := ResolveRecipients(subsidiaries, services, ptr("P1"), rows); strings.Join(got, ",") != "both,same" {
		t.Fatalf("combined recipients = %v", got)
	}
}

func TestSupplementKeepsFactsWithoutAI(t *testing.T) {
	text := RenderSupplement(SupplementData{
		ProblemID:     1,
		IncidentID:    7,
		RootObject:    "sw-acc-04",
		RootSymptom:   "node_down",
		OpenedAt:      time.Date(2026, 8, 6, 11, 0, 1, 0, time.UTC),
		SymptomsCount: 3,
		ServicesCount: 2,
		Checklist:     []string{"Проверить питание."},
	})
	if !strings.Contains(text, "Основание: правило не определено") || strings.Contains(text, "Сводка (гипотеза") {
		t.Fatalf("unexpected supplement: %s", text)
	}
	if !strings.Contains(text, "• Проверить питание.") {
		t.Fatalf("missing fallback checklist: %s", text)
	}
}

func TestTemplatesMarkHypothesesAndDuplicates(t *testing.T) {
	hypothesis := "Причина, вероятно, в питании."
	text := RenderNew(ProblemData{
		ID:                    9,
		Priority:              ptr("P1"),
		ObjectName:            "sw-01",
		Site:                  ptr("brd"),
		SymptomClass:          "node_down",
		SourceSystem:          "solarwinds",
		OriginalBody:          "original",
		AIRootCauseHypothesis: &hypothesis,
	})
	if strings.Index(text, "Симптом") > strings.Index(text, "гипотеза") || !strings.Contains(text, hypothesis) {
		t.Fatalf("hypothesis placement drifted: %s", text)
	}
	incidentID := int64(7)
	duplicate := RenderDuplicate(42, 10, &incidentID, "solarwinds")
	if !strings.Contains(duplicate, "PRB-0042") || !strings.Contains(duplicate, "INC-0007") || !strings.Contains(duplicate, "solarwinds") {
		t.Fatalf("duplicate template drifted: %s", duplicate)
	}
}

func TestSupplementPlacesAIUnderFacts(t *testing.T) {
	summary := "Коммутатор недоступен."
	recommendation := "Проверьте питание."
	text := RenderSupplement(SupplementData{
		ProblemID:        1,
		IncidentID:       7,
		RootObject:       "sw-acc-04",
		RootSymptom:      "node_down",
		OpenedAt:         time.Date(2026, 8, 6, 11, 0, 1, 0, time.UTC),
		SymptomsCount:    3,
		ServicesCount:    2,
		RuleNames:        []string{"corr-114"},
		AISummary:        &summary,
		AIRecommendation: &recommendation,
	})
	if strings.Index(text, "Основание") > strings.Index(text, "гипотеза") {
		t.Fatalf("AI appeared before deterministic facts: %s", text)
	}
	if !strings.Contains(text, recommendation) || strings.Contains(text, "чек-лист из базы знаний") {
		t.Fatalf("AI recommendation drifted: %s", text)
	}
}

func TestPromptsPreservePythonContract(t *testing.T) {
	prompt := BuildSummaryPrompt(
		"node_down", "sw-01", "brd", time.Date(2026, 8, 6, 1, 2, 3, 0, time.UTC),
		[]Symptom{{ObjectName: "host-1", Class: "host_unreachable"}}, []string{"corr-114"},
	)
	if !strings.Contains(prompt, "Связанные симптомы (1):") || !strings.Contains(prompt, "Не упоминай приоритет") {
		t.Fatalf("summary prompt drifted: %s", prompt)
	}
	recommendation := BuildRecommendationPrompt("node_down", "sw-01", "brd", 1, []string{"Проверить питание."})
	if recommendation == nil || !strings.Contains(*recommendation, "ТОЛЬКО пункты чек-листа") {
		t.Fatalf("recommendation prompt drifted")
	}
}

func TestOnDemandAnalysisPromptListsRelatedOrSaysStandalone(t *testing.T) {
	withRelated := BuildOnDemandAnalysisPrompt(
		"sw-01", "brd", "node_down", time.Date(2026, 8, 6, 1, 2, 3, 0, time.UTC),
		[]Symptom{{ObjectName: "host-1", Class: "host_unreachable"}},
	)
	if !strings.Contains(withRelated, "host-1 (host_unreachable)") {
		t.Fatalf("on-demand prompt dropped related symptom: %s", withRelated)
	}
	standalone := BuildOnDemandAnalysisPrompt("sw-01", "brd", "node_down", time.Date(2026, 8, 6, 1, 2, 3, 0, time.UTC), nil)
	if !strings.Contains(standalone, "инцидент не сформирован") {
		t.Fatalf("on-demand prompt should admit no incident when standalone: %s", standalone)
	}
}

func TestRenderAIAnalysisFactsBeforeHypothesisAndDegradesGracefully(t *testing.T) {
	incidentID := int64(3)
	analysis := "Вероятно, отказ питания коммутатора."
	withAI := RenderAIAnalysis(AIAnalysisData{
		ProblemID: 5, IncidentID: &incidentID, ObjectName: "sw-01", SymptomClass: "node_down",
		Site: ptr("brd"), Priority: ptr("P1"), RelatedCount: 2, AIText: &analysis,
	})
	if strings.Index(withAI, "Связанных алертов") > strings.Index(withAI, "гипотеза") {
		t.Fatalf("AI text appeared before deterministic facts: %s", withAI)
	}
	if !strings.Contains(withAI, analysis) || !strings.Contains(withAI, "INC-0003") {
		t.Fatalf("rendered analysis drifted: %s", withAI)
	}

	degraded := RenderAIAnalysis(AIAnalysisData{ProblemID: 5, ObjectName: "sw-01", SymptomClass: "node_down"})
	if !strings.Contains(degraded, "ИИ временно недоступна") || !strings.Contains(degraded, "Инцидент не сформирован") {
		t.Fatalf("degraded analysis should stay honest about missing AI and incident: %s", degraded)
	}
}

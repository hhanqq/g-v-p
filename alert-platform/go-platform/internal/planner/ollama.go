package planner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type OllamaClient struct {
	baseURL string
	model   string
	client  *http.Client
}

func NewOllamaClient(baseURL, model string, timeout time.Duration) *OllamaClient {
	return &OllamaClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		client:  &http.Client{Timeout: timeout},
	}
}

func (client *OllamaClient) Ask(ctx context.Context, prompt string, numPredict int) *string {
	payload := map[string]any{
		"model":      client.model,
		"messages":   []map[string]string{{"role": "user", "content": prompt}},
		"stream":     false,
		"think":      false,
		"keep_alive": "30m",
		"options":    map[string]int{"num_predict": numPredict},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.client.Do(request)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil
	}
	var decoded struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return nil
	}
	content := strings.TrimSpace(decoded.Message.Content)
	if content == "" {
		return nil
	}
	return &content
}

func BuildSummaryPrompt(rootSymptom, rootObject, rootSite string, openedAt time.Time, symptoms []Symptom, rules []string) string {
	lines := make([]string, 0, len(symptoms))
	for _, symptom := range symptoms {
		lines = append(lines, fmt.Sprintf("- %s (%s)", symptom.ObjectName, symptom.Class))
	}
	ruleText := "не определено"
	if len(rules) > 0 {
		ruleText = strings.Join(rules, ", ")
	}
	return fmt.Sprintf(
		"Инцидент мониторинга промышленного предприятия.\nПервопричина (кандидат): %s на площадке %s, симптом %s, зафиксировано %s.\nСвязанные симптомы (%d):\n%s\nПравило корреляции: %s.\n\nДай короткую сводку для дежурного инженера: 2-4 предложения, только факты из данных выше, без домыслов. Не используй списки и заголовки — сплошной текст. Не упоминай приоритет и не оценивай критичность — это не входит в переданные данные.",
		rootObject, rootSite, rootSymptom, openedAt.Format("2006-01-02 15:04:05"),
		len(symptoms), strings.Join(lines, "\n"), ruleText,
	)
}

func BuildOnDemandAnalysisPrompt(objectName, site, symptomClass string, openedAt time.Time, symptoms []Symptom) string {
	lines := make([]string, 0, len(symptoms))
	for _, symptom := range symptoms {
		lines = append(lines, fmt.Sprintf("- %s (%s)", symptom.ObjectName, symptom.Class))
	}
	related := "нет данных о связанных алертах — инцидент не сформирован, разбор по одиночному алерту"
	if len(lines) > 0 {
		related = strings.Join(lines, "\n")
	}
	return fmt.Sprintf(
		"Инцидент мониторинга промышленного предприятия. Дежурный инженер попросил разбор конкретного алерта.\nАлерт: %s на площадке %s, симптом %s, зафиксирован %s.\nСвязанные алерты в том же инциденте:\n%s\n\nДай развёрнутый разбор для дежурного инженера на русском (4-6 предложений): что вероятно произошло, как это связано с остальными алертами (если они есть), с чего начать диагностику. Используй только факты из данных выше, без домыслов. Не используй списки и заголовки — сплошной текст.",
		objectName, site, symptomClass, openedAt.Format("2006-01-02 15:04:05"), related,
	)
}

func BuildRecommendationPrompt(symptomClass, objectName, site string, related int, checklist []string) *string {
	if len(checklist) == 0 {
		return nil
	}
	items := make([]string, 0, len(checklist))
	for _, step := range checklist {
		items = append(items, "- "+step)
	}
	prompt := fmt.Sprintf(
		"Инцидент мониторинга: %s на площадке %s, симптом %s, связанных алертов в каскаде: %d.\n\nЧек-лист устранения из базы знаний для этого класса симптома:\n%s\n\nВыбери и коротко (1-3 предложения, без списка и заголовка) сформулируй для дежурного инженера, с чего начать именно в этом случае — используй ТОЛЬКО пункты чек-листа выше, ничего не добавляй от себя и не упоминай факты, которых нет в условии.",
		objectName, site, symptomClass, related, strings.Join(items, "\n"),
	)
	return &prompt
}

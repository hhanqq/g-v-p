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
	baseURL    string
	model      string
	embedModel string
	client     *http.Client
}

func NewOllamaClient(baseURL, model, embedModel string, timeout time.Duration) *OllamaClient {
	return &OllamaClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		model:      model,
		embedModel: embedModel,
		client:     &http.Client{Timeout: timeout},
	}
}

// Embed вызывает локальную embedding-модель (nomic-embed-text) для RAG
// по базе знаний (internal/planner/knowledge_base.go). Деградирует к
// nil при любой ошибке — тот же контракт, что у Ask (раздел И5):
// отсутствие контекста из базы знаний не должно блокировать разбор.
func (client *OllamaClient) Embed(ctx context.Context, text string) []float32 {
	payload := map[string]any{"model": client.embedModel, "prompt": text}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.client.Do(request)
	if err != nil {
		return nil
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil
	}
	var decoded struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return nil
	}
	if len(decoded.Embedding) == 0 {
		return nil
	}
	return decoded.Embedding
}

// Reachable — легковесная проверка живости Ollama для «Состояние
// системы» (раздел 24 доп. ТЗ): GET /api/tags ничего не грузит и не
// занимает GPU, в отличие от Ask/Embed.
func (client *OllamaClient) Reachable(ctx context.Context) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+"/api/tags", nil)
	if err != nil {
		return false
	}
	response, err := client.client.Do(request)
	if err != nil {
		return false
	}
	defer func() { _ = response.Body.Close() }()
	return response.StatusCode >= 200 && response.StatusCode < 300
}

type RunningModel struct {
	Name     string
	SizeVRAM int64
}

// RunningModels — GET /api/ps: модели, реально загруженные в GPU/VRAM
// прямо сейчас, с SizeVRAM в байтах. Единственный источник настоящих
// данных о занятой VRAM без доступа к nvidia-smi с хоста (см.
// platform_health.go — общая емкость GPU конфигурируется отдельно,
// потому что Ollama API её не отдаёт).
func (client *OllamaClient) RunningModels(ctx context.Context) ([]RunningModel, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+"/api/ps", nil)
	if err != nil {
		return nil, err
	}
	response, err := client.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("ollama /api/ps returned %d", response.StatusCode)
	}
	var decoded struct {
		Models []struct {
			Name     string `json:"name"`
			SizeVRAM int64  `json:"size_vram"`
		} `json:"models"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	out := make([]RunningModel, 0, len(decoded.Models))
	for _, m := range decoded.Models {
		out = append(out, RunningModel{Name: m.Name, SizeVRAM: m.SizeVRAM})
	}
	return out, nil
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
	defer func() { _ = response.Body.Close() }()
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

func BuildOnDemandAnalysisPrompt(objectName, site, symptomClass string, openedAt time.Time, symptoms []Symptom, kbChunks []KBChunk) string {
	lines := make([]string, 0, len(symptoms))
	for _, symptom := range symptoms {
		lines = append(lines, fmt.Sprintf("- %s (%s)", symptom.ObjectName, symptom.Class))
	}
	related := "нет данных о связанных алертах — инцидент не сформирован, разбор по одиночному алерту"
	if len(lines) > 0 {
		related = strings.Join(lines, "\n")
	}
	kbSection := ""
	if len(kbChunks) > 0 {
		kbLines := make([]string, 0, len(kbChunks))
		for _, chunk := range kbChunks {
			kbLines = append(kbLines, fmt.Sprintf("### %s\n%s", chunk.Title, chunk.Content))
		}
		kbSection = "\n\nВыдержки из базы знаний компании по управлению инцидентами (используй их как основной источник для рекомендаций, не противоречь им):\n" + strings.Join(kbLines, "\n\n")
	}
	return fmt.Sprintf(
		"Инцидент мониторинга промышленного предприятия. Дежурный инженер попросил разбор конкретного алерта.\nАлерт: %s на площадке %s, симптом %s, зафиксирован %s.\nСвязанные алерты в том же инциденте:\n%s%s\n\nДай развёрнутый разбор для дежурного инженера на русском (4-6 предложений): что вероятно произошло, как это связано с остальными алертами (если они есть), с чего начать диагностику согласно базе знаний компании (если выдержки выше даны). Используй только факты из данных выше и базы знаний, без домыслов. Не используй списки и заголовки — сплошной текст.",
		objectName, site, symptomClass, openedAt.Format("2006-01-02 15:04:05"), related, kbSection,
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

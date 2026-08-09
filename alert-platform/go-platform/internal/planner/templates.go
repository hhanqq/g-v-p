package planner

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// esc экранирует динамический текст (сырое тело алерта от источника
// мониторинга, вывод ИИ, имена объектов/сценариев/правил) перед
// вставкой в HTML-размеченное сообщение TrueConf (parse_mode: HTML).
// Не применяется к литеральной разметке (<b>/<i>), которую пишет сам
// шаблон — только к значениям из данных. Без этого скомпрометированный
// или ошибочно настроенный источник мониторинга мог бы протащить
// произвольную HTML-разметку в исходящее сообщение через текст алерта.
func esc(value string) string {
	return html.EscapeString(value)
}

var priorityEmoji = map[string]string{
	"P0": "🔴",
	"P1": "🟠",
	"P2": "🟡",
	"P3": "⚪",
}

func DisplayID(problemID int64, incidentID *int64) string {
	if incidentID != nil {
		return fmt.Sprintf("INC-%04d", *incidentID)
	}
	return fmt.Sprintf("PRB-%04d", problemID)
}

func RenderNew(data ProblemData) string {
	priority := valueOr(data.Priority, "?")
	emoji, ok := priorityEmoji[priority]
	if !ok {
		emoji = "⚪"
	}
	serviceLine := "Сервис: не определён"
	if data.ServiceName != nil {
		serviceLine = "Сервис: " + esc(*data.ServiceName)
	}
	text := fmt.Sprintf(
		"%s <b>%s · %s</b> · %s · %s\n─────── оригинал %s ───────\n%s\n───────────────────────────────\n%s · Симптом: %s",
		emoji, priority, DisplayID(data.ID, data.IncidentID), esc(data.ObjectName),
		esc(valueOr(data.Site, "?")), esc(data.SourceSystem), esc(data.OriginalBody),
		serviceLine, esc(data.SymptomClass),
	)
	if data.AIRootCauseHypothesis != nil && *data.AIRootCauseHypothesis != "" {
		text += "\n\n<i>Вероятная первопричина (гипотеза, требует проверки, сформирована ИИ):</i>\n" +
			esc(*data.AIRootCauseHypothesis)
	}
	return text
}

func RenderClosure(data ProblemData) string {
	note := ""
	if data.ClosedByReconciliation {
		note = " (закрыто автоматически по таймауту, без сообщения о восстановлении)"
	}
	return fmt.Sprintf(
		"🟢 <b>ЗАКРЫТО · %s</b>\nВосстановлено %s · Длительность %s%s",
		DisplayID(data.ID, data.IncidentID),
		data.ResolvedAt.Format("2006-01-02 15:04:05"),
		FormatDuration(data.ResolvedAt.Sub(data.OpenedAt)), note,
	)
}

func RenderDuplicate(duplicateProblemID, originalProblemID int64, incidentID *int64, source string) string {
	return fmt.Sprintf(
		"🔗 <b>ДУБЛЬ</b> · PRB-%04d\nПохоже на то же событие, что и %s — подтверждение от %s (определено ИИ, раздел 4.1). Отдельное уведомление не отправлено.",
		duplicateProblemID, DisplayID(originalProblemID, incidentID), esc(source),
	)
}

func RenderScenario(problem ProblemData, scenarioName string, escalation bool) string {
	action := "Уведомление"
	if escalation {
		action = "Эскалация"
	}
	return fmt.Sprintf("🧩 <b>%s · %s</b>\nСценарий: %s\nОбъект: %s · Приоритет: %s", action, DisplayID(problem.ID, problem.IncidentID), esc(scenarioName), esc(problem.ObjectName), valueOr(problem.Priority, "?"))
}

func RenderSLABreach(problem ProblemData, ruleName string, ageMinutes, thresholdMinutes int) string {
	return fmt.Sprintf("⏱ <b>НАРУШЕНИЕ SLA · %s</b>\nОбъект: %s · Приоритет: %s\nНет реакции %d мин (порог %d мин) · правило %s", DisplayID(problem.ID, problem.IncidentID), esc(problem.ObjectName), valueOr(problem.Priority, "?"), ageMinutes, thresholdMinutes, esc(ruleName))
}

type SupplementData struct {
	ProblemID        int64
	IncidentID       int64
	RootObject       string
	RootSymptom      string
	OpenedAt         time.Time
	SymptomsCount    int
	ServicesCount    int
	RuleNames        []string
	AISummary        *string
	AIRecommendation *string
	Checklist        []string
}

func RenderSupplement(data SupplementData) string {
	rules := "не определено"
	if len(data.RuleNames) > 0 {
		rules = esc(strings.Join(data.RuleNames, ", "))
	}
	lines := []string{
		fmt.Sprintf("🔵 <b>ДОПОЛНЕНИЕ к %s</b>", DisplayID(data.ProblemID, &data.IncidentID)),
		fmt.Sprintf("Первопричина: %s (%s), с %s", esc(data.RootObject), esc(data.RootSymptom), data.OpenedAt.Format("2006-01-02 15:04:05")),
		fmt.Sprintf("Связано алертов: %d · Затронуто сервисов: %d", data.SymptomsCount, data.ServicesCount),
		fmt.Sprintf("Основание: правило %s", rules),
	}
	if data.AISummary != nil && *data.AISummary != "" {
		lines = append(lines, "", "<i>Сводка (гипотеза, сформирована ИИ):</i>", esc(*data.AISummary))
	}
	if data.AIRecommendation != nil && *data.AIRecommendation != "" {
		lines = append(lines, "", "<i>Рекомендация (на основе базы знаний, сформулирована ИИ):</i>", esc(*data.AIRecommendation))
	} else if len(data.Checklist) > 0 {
		lines = append(lines, "", "<i>Рекомендация (чек-лист из базы знаний):</i>")
		for _, step := range data.Checklist {
			lines = append(lines, "• "+esc(step))
		}
	}
	return strings.Join(lines, "\n")
}

type AIAnalysisData struct {
	ProblemID    int64
	IncidentID   *int64
	ObjectName   string
	SymptomClass string
	Site         *string
	Priority     *string
	RelatedCount int
	AIText       *string
}

func RenderAIAnalysis(data AIAnalysisData) string {
	lines := []string{
		fmt.Sprintf("🧠 <b>РАЗБОР ПО ЗАПРОСУ · %s</b>", DisplayID(data.ProblemID, data.IncidentID)),
		fmt.Sprintf("Объект: %s · Симптом: %s · Площадка: %s · Приоритет: %s",
			esc(data.ObjectName), esc(data.SymptomClass), esc(valueOr(data.Site, "?")), valueOr(data.Priority, "?")),
	}
	if data.IncidentID != nil {
		lines = append(lines, fmt.Sprintf("Связанных алертов в инциденте: %d", data.RelatedCount))
	} else {
		lines = append(lines, "Инцидент не сформирован — разбор по одиночному алерту")
	}
	if data.AIText != nil && *data.AIText != "" {
		lines = append(lines, "", "<i>Разбор (гипотеза, требует проверки, сформирована ИИ):</i>", esc(*data.AIText))
	} else {
		lines = append(lines, "", "ИИ временно недоступна — попробуйте попросить разбор ещё раз чуть позже.")
	}
	return strings.Join(lines, "\n")
}

func FormatDuration(duration time.Duration) string {
	seconds := int64(duration.Seconds())
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	remainder := seconds % 60
	if hours > 0 {
		return fmt.Sprintf("%d ч %d мин", hours, minutes)
	}
	if minutes > 0 {
		return fmt.Sprintf("%d мин %d с", minutes, remainder)
	}
	return fmt.Sprintf("%d с", remainder)
}

func valueOr(value *string, fallback string) string {
	if value == nil || *value == "" {
		return fallback
	}
	return *value
}

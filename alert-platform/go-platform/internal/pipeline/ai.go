package pipeline

import (
	"context"
	"fmt"
	"strings"
)

type AIClient interface {
	Ask(context.Context, string, int) *string
}

var knownSymptomClasses = []string{
	"host_unreachable", "node_down", "interface_down",
	"disk_space", "service_down", "power_lost",
}

func classifySymptom(ctx context.Context, client AIClient, text string) *string {
	if client == nil {
		return nil
	}
	prompt := fmt.Sprintf(
		"Текст алерта системы мониторинга промышленного предприятия:\n\"\"\"\n%s\n\"\"\"\n\nОпредели класс симптома СТРОГО из списка: %s.\nЕсли ни один класс уверенно не подходит по смыслу — ответь ровно одним словом: unknown.\nОтветь ТОЛЬКО одним словом из списка, без пояснений и знаков препинания.",
		truncate(strings.TrimSpace(text), 1000), strings.Join(knownSymptomClasses, ", "),
	)
	reply := client.Ask(ctx, prompt, 10)
	if reply == nil {
		return nil
	}
	candidate := firstWord(*reply)
	for _, class := range knownSymptomClasses {
		if candidate == class {
			return &class
		}
	}
	return nil
}

func isDuplicate(ctx context.Context, client AIClient, first, second string) *bool {
	if client == nil {
		return nil
	}
	prompt := fmt.Sprintf(
		"Два алерта системы мониторинга промышленного предприятия по одному и тому же объекту инфраструктуры, пришли почти одновременно от РАЗНЫХ систем мониторинга.\n\nАлерт 1:\n\"\"\"\n%s\n\"\"\"\n\nАлерт 2:\n\"\"\"\n%s\n\"\"\"\n\nЭто одно и то же реальное событие, описанное двумя системами разными словами, или два разных события на одном объекте? Ответь ТОЛЬКО одним словом: same — если одно и то же событие, different — если разные.",
		truncate(strings.TrimSpace(first), 600), truncate(strings.TrimSpace(second), 600),
	)
	reply := client.Ask(ctx, prompt, 5)
	if reply == nil {
		return nil
	}
	verdict := firstWord(*reply)
	if verdict == "same" {
		value := true
		return &value
	}
	if verdict == "different" {
		value := false
		return &value
	}
	return nil
}

func suggestRootCause(ctx context.Context, client AIClient, site, first, second string) *string {
	if client == nil {
		return nil
	}
	prompt := fmt.Sprintf(
		"На площадке %s промышленного предприятия почти одновременно зафиксированы два независимых алерта, и правило автоматической корреляции не смогло однозначно связать их причинно-следственной связью:\n\nСобытие А:\n\"\"\"\n%s\n\"\"\"\n\nСобытие Б:\n\"\"\"\n%s\n\"\"\"\n\nКакое из двух вероятнее является первопричиной, а какое — следствием? Ответь 2-3 предложениями, используя ТОЛЬКО факты из текстов выше, без домыслов, и явно укажи, что это предположение, требующее проверки инженером, а не установленный факт.",
		site, truncate(strings.TrimSpace(first), 600), truncate(strings.TrimSpace(second), 600),
	)
	return client.Ask(ctx, prompt, 200)
}

func firstWord(reply string) string {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(reply)))
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[0], ".,!\"'")
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

package planner

import (
	"context"
	"strconv"
	"strings"
)

type KBChunk struct {
	Title   string
	Content string
}

// searchKnowledgeBase — RAG-поиск по базе знаний компании (knowledge_base/,
// проиндексирована cmd/kb-index в kb_chunks). Эмбеддинг запроса —
// та же локальная модель, что использовалась при индексации; поиск —
// косинусная близость через pgvector (<=>). Деградирует к пустому
// результату при недоступности embedding-модели или БД — раздел И5,
// отсутствие базы знаний не должно останавливать разбор алерта.
func (planner *Planner) searchKnowledgeBase(ctx context.Context, queryText string, limit int) []KBChunk {
	vector := planner.ollama.Embed(ctx, queryText)
	if vector == nil {
		return nil
	}
	rows, err := planner.pool.Query(ctx,
		`SELECT title, content FROM kb_chunks ORDER BY embedding <=> $1::vector LIMIT $2`,
		VectorLiteral(vector), limit,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var chunks []KBChunk
	for rows.Next() {
		var chunk KBChunk
		if err := rows.Scan(&chunk.Title, &chunk.Content); err != nil {
			return nil
		}
		chunks = append(chunks, chunk)
	}
	if rows.Err() != nil {
		return nil
	}
	return chunks
}

// VectorLiteral форматирует эмбеддинг в текстовый литерал pgvector
// ("[0.1,0.2,...]") — без отдельной зависимости pgvector-go, тот же
// минимализм, что и у остального Go-кода этого проекта. Экспортирован,
// чтобы cmd/kb-index переиспользовал ровно ту же сериализацию, что и
// поиск в searchKnowledgeBase — не дублировал её по цепочке.
func VectorLiteral(vector []float32) string {
	parts := make([]string, len(vector))
	for i, value := range vector {
		parts[i] = strconv.FormatFloat(float64(value), 'f', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

type MarkdownChunk struct {
	Title   string
	Content string
}

const maxChunkChars = 900

// ChunkMarkdown режет документ базы знаний (cmd/kb-index) на чанки по
// заголовкам второго уровня (## ...), с заголовком первого уровня
// (# ...) как общим контекстом названия. Слишком длинные секции
// дополнительно делятся по абзацам (разделены пустой строкой), жадно
// собирая абзацы до maxChunkChars — без внешней библиотеки чанкинга,
// тот же минимализм, что и у остального проекта.
func ChunkMarkdown(markdown string) []MarkdownChunk {
	lines := strings.Split(markdown, "\n")
	docTitle := ""
	type section struct {
		title string
		body  []string
	}
	var sections []section
	var current *section
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t\r")
		if strings.HasPrefix(trimmed, "# ") && !strings.HasPrefix(trimmed, "## ") {
			if docTitle == "" {
				docTitle = strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
			}
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			sections = append(sections, section{title: strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))})
			current = &sections[len(sections)-1]
			continue
		}
		if current == nil {
			sections = append(sections, section{title: docTitle})
			current = &sections[len(sections)-1]
		}
		current.body = append(current.body, line)
	}

	var chunks []MarkdownChunk
	for _, sec := range sections {
		title := sec.title
		if docTitle != "" && title != docTitle {
			title = docTitle + " — " + title
		}
		body := strings.TrimSpace(strings.Join(sec.body, "\n"))
		if body == "" {
			continue
		}
		if len(body) <= maxChunkChars {
			chunks = append(chunks, MarkdownChunk{Title: title, Content: body})
			continue
		}
		paragraphs := strings.Split(body, "\n\n")
		var buffer strings.Builder
		for _, paragraph := range paragraphs {
			paragraph = strings.TrimSpace(paragraph)
			if paragraph == "" {
				continue
			}
			if buffer.Len() > 0 && buffer.Len()+len(paragraph)+2 > maxChunkChars {
				chunks = append(chunks, MarkdownChunk{Title: title, Content: strings.TrimSpace(buffer.String())})
				buffer.Reset()
			}
			if buffer.Len() > 0 {
				buffer.WriteString("\n\n")
			}
			buffer.WriteString(paragraph)
		}
		if buffer.Len() > 0 {
			chunks = append(chunks, MarkdownChunk{Title: title, Content: strings.TrimSpace(buffer.String())})
		}
	}
	return chunks
}

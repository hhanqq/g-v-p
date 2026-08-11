package adminapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

// Раздел «Маскирование данных в Guest Mode» доп. ТЗ: маскирование
// применяется НА BACKEND, до отправки JSON клиенту — не заменой текста
// в React (реальное значение тогда было бы видно через DevTools/
// Network). Единая точка применения — guestMaskingWriter, подключаемая
// в withAuth для КАЖДОГО guest-запроса, а не точечная правка отдельных
// хендлеров: гость физически не может получить raw-значение ни одним
// путём, обслуживаемым withAuth/withPermission (что покрывает все
// /api/* эндпоинты консоли, включая поиск и BI — см. ниже).
//
// Поля маскируются по ИМЕНИ ключа JSON, без знания, какой хендлер его
// произвёл — это и даёт гарантию покрытия всех текущих и будущих
// эндпоинтов без необходимости помнить добавить маскирование в каждый
// новый хендлер. Два поля («id», «name») намеренно неоднозначны в схеме
// (используются и для оборудования, и для инцидентов/сценариев/групп),
// поэтому маскируются только при структурном признаке «это запись CMDB»
// (соседний ключ equipment_type, либо одновременно site+fqdn) —
// guestLooksLikeEquipmentRecord ниже.

var guestMaskedFields = map[string]func(string) string{
	"full_name":         maskPersonName,
	"trueconf_username": maskUsername,
	"email":             maskEmail,
	"phone":             maskPhone,
	"ip":                maskIP,
	"fqdn":              maskFQDN,
}

func guestLooksLikeEquipmentRecord(record map[string]any) bool {
	if _, ok := record["equipment_type"]; ok {
		return true
	}
	_, hasSite := record["site"]
	_, hasFQDN := record["fqdn"]
	return hasSite && hasFQDN
}

// maskGuestJSON — рекурсивно маскирует чувствительные поля в произвольном
// JSON-дереве ответа. Возвращает исходные байты без изменений, если тело
// не является валидным JSON (пустой ответ, 204 No Content и т.п.) — не
// пытается "исправить" то, что не понимает.
func maskGuestJSON(raw []byte) []byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return raw
	}
	var data any
	if err := json.Unmarshal(trimmed, &data); err != nil {
		return raw
	}
	masked := guestMaskValue(data)
	encoded, err := json.Marshal(masked)
	if err != nil {
		return raw
	}
	return encoded
}

func guestMaskValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		return guestMaskObject(val)
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = guestMaskValue(item)
		}
		return out
	default:
		return val
	}
}

func guestMaskObject(record map[string]any) map[string]any {
	isEquipment := guestLooksLikeEquipmentRecord(record)
	out := make(map[string]any, len(record))
	for key, value := range record {
		switch {
		case guestMaskedFields[key] != nil:
			out[key] = guestMaskLeaf(value, guestMaskedFields[key])
		case isEquipment && (key == "id" || key == "object_id" || key == "name"):
			out[key] = guestMaskLeaf(value, maskEquipmentIdentifier)
		default:
			out[key] = guestMaskValue(value)
		}
	}
	return out
}

// guestMaskLeaf применяет маску только к строковым листьям — не
// пытается угадать формат для чисел/bool/null под тем же именем ключа
// (например integer id инцидента никогда не попадёт сюда: это не
// equipment-запись).
func guestMaskLeaf(v any, mask func(string) string) any {
	switch val := v.(type) {
	case string:
		return mask(val)
	case nil:
		return nil
	default:
		return v
	}
}

// maskWord — «Алексей» остаётся, «Иванов» → «И*****»: первая буква,
// остальное звёздами, длина сохраняется (не даёт угадать длину по
// количеству символов в реальном значении сильнее, чем в примере ТЗ,
// но и не выдаёт "Иванов" целиком).
func maskWord(word string) string {
	runes := []rune(word)
	if len(runes) == 0 {
		return word
	}
	if len(runes) == 1 {
		return "*"
	}
	return string(runes[0]) + strings.Repeat("*", len(runes)-1)
}

// maskPersonName — "Иванов Алексей Николаевич" → "И***** Алексей
// Николаевич": реальные full_name в этом проекте хранятся в порядке
// "Фамилия Имя Отчество" (проверено на живых данных VM, не угадано —
// первая версия маскировала наоборот и оставляла фамилию, самую
// идентифицирующую часть, полностью открытой). Маскируется первое
// слово (фамилия), остальные (имя/отчество) остаются — сама по себе
// комбинация имя+отчество без фамилии не идентифицирует конкретного
// человека в масштабе организации. Один непонятный формат ("Иванов"
// без имени) — то же правило, что и одно слово: маскируется как
// maskWord.
func maskPersonName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return name
	}
	words := strings.Fields(trimmed)
	if len(words) == 1 {
		return maskWord(words[0])
	}
	out := make([]string, len(words))
	out[0] = maskWord(words[0])
	for i := 1; i < len(words); i++ {
		out[i] = words[i]
	}
	return strings.Join(out, " ")
}

// maskUsername — "ivanov.an" → "i*****.an": TrueConf-логины в этом
// проекте по конвенции "фамилия.инициалы" (см. seed_employees.py);
// маскируется фамилия, инициалы после точки остаются (сами по себе не
// идентифицируют человека без фамилии).
func maskUsername(username string) string {
	dot := strings.IndexByte(username, '.')
	if dot <= 0 {
		return maskWord(username)
	}
	return maskWord(username[:dot]) + username[dot:]
}

// maskEmail — "ivanov@company.local" → "i*****@company.local": первая
// буква локальной части + звёзды, домен остаётся (сам по себе не
// идентифицирует конкретного человека).
func maskEmail(email string) string {
	at := strings.IndexByte(email, '@')
	if at <= 0 {
		return maskWord(email)
	}
	return maskWord(email[:at]) + email[at:]
}

// maskPhone — оставляет код страны/оператора (первые 3 символа),
// маскирует остальное: "+7 916 123-45-67" → "+7 *** ***-**-**"-стиль,
// без точного повторения разделителей (не критично для целей маски).
func maskPhone(phone string) string {
	digitsAndPlus := 0
	for _, r := range phone {
		if r == '+' || (r >= '0' && r <= '9') {
			digitsAndPlus++
		}
	}
	if digitsAndPlus == 0 {
		return phone
	}
	visible := 3
	runes := []rune(phone)
	if len(runes) <= visible {
		return strings.Repeat("*", len(runes))
	}
	return string(runes[:visible]) + strings.Repeat("*", len(runes)-visible)
}

// maskIP — "10.42.8.17" → "10.42.*.*": сеть видна (нужна для агрегированной
// аналитики по филиалам/подсетям), хост скрыт.
func maskIP(ip string) string {
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return maskWord(ip)
	}
	return parts[0] + "." + parts[1] + ".*.*"
}

// maskEquipmentIdentifier — "sw-brd-noyabrsk-acc-02" → "sw-****-****-**-**"-
// стиль: первый дефис-сегмент (тип устройства, например "sw"/"switch")
// остаётся, остальные маскируются посегментно — сохраняет структуру
// (видно, что это коммутатор), но не позволяет опознать конкретный
// филиал/стойку/номер.
func maskEquipmentIdentifier(id string) string {
	segments := strings.Split(id, "-")
	if len(segments) <= 1 {
		return maskWord(id)
	}
	out := make([]string, len(segments))
	out[0] = segments[0]
	for i := 1; i < len(segments); i++ {
		out[i] = strings.Repeat("*", len(segments[i]))
	}
	return strings.Join(out, "-")
}

// maskFQDN — "sw-brd-04.company.local" → "sw-***-**.*******.local":
// первая метка через maskEquipmentIdentifier, промежуточные метки домена
// маскируются целиком, последняя (TLD/локальный суффикс) остаётся —
// сам по себе ".local"/".ru" не идентифицирует конкретную компанию.
func maskFQDN(fqdn string) string {
	labels := strings.Split(fqdn, ".")
	if len(labels) == 0 {
		return fqdn
	}
	labels[0] = maskEquipmentIdentifier(labels[0])
	for i := 1; i < len(labels)-1; i++ {
		labels[i] = strings.Repeat("*", len(labels[i]))
	}
	return strings.Join(labels, ".")
}

// guestMaskingWriter — раздел «Маскирование данных в Guest Mode»:
// буферизует весь ответ хендлера и маскирует его целиком перед тем, как
// он реально уходит клиенту. Подключается в withAuth, поэтому покрывает
// ВСЕ /api/* эндпоинты, обслуживаемые через сессию (включая поиск/
// Query Builder) без точечных правок каждого хендлера. BI-экспорт
// (CSV) не проходит через withAuth вовсе — отдельная токен-авторизация
// bi_service_accounts, не принимает guest-сессионную cookie, поэтому не
// является обходом маскирования (другой механизм авторизации, не тот
// же самый путь).
type guestMaskingWriter struct {
	http.ResponseWriter
	buf         bytes.Buffer
	statusCode  int
	wroteHeader bool
}

func newGuestMaskingWriter(w http.ResponseWriter) *guestMaskingWriter {
	return &guestMaskingWriter{ResponseWriter: w}
}

func (w *guestMaskingWriter) WriteHeader(status int) {
	w.statusCode = status
	w.wroteHeader = true
}

func (w *guestMaskingWriter) Write(p []byte) (int, error) {
	return w.buf.Write(p)
}

// flush маскирует накопленное тело и отправляет его в реальный
// ResponseWriter. Вызывается один раз, после того как хендлер полностью
// отработал (см. withAuth) — маскирование требует всего JSON целиком,
// не построчного стриминга.
func (w *guestMaskingWriter) flush() {
	status := w.statusCode
	if !w.wroteHeader {
		status = http.StatusOK
	}
	masked := maskGuestJSON(w.buf.Bytes())
	w.ResponseWriter.WriteHeader(status)
	_, _ = w.ResponseWriter.Write(masked)
}

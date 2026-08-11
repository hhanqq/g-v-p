package gateway

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

const maxBodyBytes = 1 << 20

type HTTPHandler struct {
	store Store
	now   func() time.Time
}

func NewHTTPHandler(store Store) http.Handler {
	h := &HTTPHandler{store: store, now: func() time.Time { return time.Now().UTC() }}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/ingest/raw", h.ingestRaw)
	mux.HandleFunc("GET /api/v1/health", h.health)
	mux.HandleFunc("GET /openapi.json", h.openapi)
	mux.HandleFunc("GET /docs", h.docs)
	return withRecovery(mux)
}

func (h *HTTPHandler) ingestRaw(response http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(response, request.Body, maxBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var payload IngestRequest
	if err := decoder.Decode(&payload); err != nil {
		writeError(response, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := validate(payload); err != nil {
		writeError(response, http.StatusUnprocessableEntity, err.Error())
		return
	}
	// Принятый риск демо-стенда (SECURITY.md) закрывается по мере
	// регистрации источников с токеном: если для этого source_instance
	// администратор задал api_token (раздел «Источники»), запрос обязан
	// предъявить точно такой же X-Source-Token. Источники без токена
	// (в т.ч. ещё не мигрированные старые записи) остаются как раньше —
	// это осознанная обратная совместимость, а не дыра по недосмотру.
	expectedToken, err := h.store.SourceToken(request.Context(), payload.SourceInstance)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "ingest unavailable")
		return
	}
	if expectedToken != nil {
		provided := request.Header.Get("X-Source-Token")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(*expectedToken)) != 1 {
			writeError(response, http.StatusUnauthorized, "invalid or missing X-Source-Token")
			return
		}
	}
	result, err := h.store.Ingest(request.Context(), payload, BodyHash(payload.RawBody), h.now())
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "ingest unavailable")
		return
	}
	writeJSON(response, http.StatusAccepted, result)
}

func validate(payload IngestRequest) error {
	if utf8.RuneCountInString(payload.SourceSystem) < 1 || utf8.RuneCountInString(payload.SourceSystem) > 64 {
		return fmt.Errorf("source_system: length must be between 1 and 64")
	}
	if utf8.RuneCountInString(payload.SourceInstance) < 1 || utf8.RuneCountInString(payload.SourceInstance) > 128 {
		return fmt.Errorf("source_instance: length must be between 1 and 128")
	}
	if payload.RawBody == "" {
		return fmt.Errorf("raw_body: must not be empty")
	}
	if strings.ContainsRune(payload.RawBody, '\x00') {
		return ErrInvalidNUL
	}
	return nil
}

func (h *HTTPHandler) health(response http.ResponseWriter, request *http.Request) {
	depth, err := h.store.Health(request.Context())
	if err != nil {
		writeJSON(response, http.StatusServiceUnavailable, map[string]string{"detail": "db unavailable"})
		return
	}
	writeJSON(response, http.StatusOK, Health{Status: "ok", DB: "ok", QueueDepth: &depth})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeError(response http.ResponseWriter, status int, detail string) {
	writeJSON(response, status, map[string]string{"detail": detail})
}

func withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		defer func() {
			if recover() != nil {
				writeError(response, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(response, request)
	})
}

func (h *HTTPHandler) openapi(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = response.Write([]byte(openAPISpec))
}

func (h *HTTPHandler) docs(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = response.Write([]byte(docsHTML))
}

const openAPISpec = `{
  "openapi":"3.1.0",
  "info":{"title":"ADP — шлюз приёма (Go)","version":"1.0.0"},
  "paths":{
    "/api/v1/ingest/raw":{"post":{
      "summary":"Приём сырого сигнала",
      "description":"Если для source_instance в разделе «Источники» задан api_token, запрос обязан передать заголовок X-Source-Token с этим значением.",
      "parameters":[{"name":"X-Source-Token","in":"header","required":false,"schema":{"type":"string"},"description":"Обязателен, только если источник зарегистрирован с токеном"}],
      "requestBody":{"required":true,"content":{"application/json":{"schema":{"$ref":"#/components/schemas/RawIngestRequest"}}}},
      "responses":{
        "202":{"description":"Сигнал записан в WORM и очередь","content":{"application/json":{"schema":{"$ref":"#/components/schemas/IngestAck"}}}},
        "400":{"description":"Некорректный JSON"},
        "401":{"description":"Отсутствует/неверен X-Source-Token для источника с заданным токеном"},
        "422":{"description":"Ошибка валидации"},
        "503":{"description":"БД недоступна"}
      }
    }},
    "/api/v1/health":{"get":{"summary":"Самодиагностика","responses":{"200":{"description":"Сервис и БД доступны","content":{"application/json":{"schema":{"$ref":"#/components/schemas/Health"}}}},"503":{"description":"БД недоступна"}}}}
  },
  "components":{"schemas":{
    "RawIngestRequest":{"type":"object","additionalProperties":false,"required":["source_system","source_instance","raw_body"],"properties":{"source_system":{"type":"string","minLength":1,"maxLength":64},"source_instance":{"type":"string","minLength":1,"maxLength":128},"raw_body":{"type":"string","minLength":1},"external_id":{"type":["string","null"]}}},
    "IngestAck":{"type":"object","required":["signal_id","status"],"properties":{"signal_id":{"type":"integer"},"status":{"enum":["queued","duplicate"]}}},
    "Health":{"type":"object","required":["status","db","queue_depth"],"properties":{"status":{"const":"ok"},"db":{"const":"ok"},"queue_depth":{"type":"integer","minimum":0}}}
  }}
}`

const docsHTML = `<!doctype html><html lang="ru"><meta charset="utf-8"><title>Dispatcher Gateway API</title>
<style>body{font:16px system-ui;max-width:920px;margin:40px auto;padding:0 20px}textarea,input{width:100%;box-sizing:border-box;margin:4px 0 12px;padding:8px}textarea{height:160px}button{padding:10px 16px}pre{background:#f4f4f5;padding:16px;white-space:pre-wrap}</style>
<h1>ADP — шлюз приёма (Go)</h1><p><a href="/openapi.json">OpenAPI 3.1</a></p>
<h2>POST /api/v1/ingest/raw</h2><form id="f"><label>source_system</label><input id="s" value="zabbix"><label>source_instance</label><input id="i" value="zbx-demo"><label>X-Source-Token (только если задан в разделе «Источники»)</label><input id="tok" value=""><label>raw_body</label><textarea id="b">PROBLEM: demo</textarea><button>Отправить</button></form><pre id="o"></pre>
<script>f.onsubmit=async(e)=>{e.preventDefault();let h={'content-type':'application/json'};if(tok.value)h['X-Source-Token']=tok.value;let r=await fetch('/api/v1/ingest/raw',{method:'POST',headers:h,body:JSON.stringify({source_system:s.value,source_instance:i.value,raw_body:b.value})});o.textContent=r.status+'\n'+await r.text()}</script></html>`

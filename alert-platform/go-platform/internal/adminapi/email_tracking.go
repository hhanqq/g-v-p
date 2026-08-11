package adminapi

import (
	"net/http"
	"strings"
	"time"
)

// 1x1 прозрачный GIF — стандартный tracking pixel.
var transparentGIF = []byte{
	0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0x01, 0x00, 0x01, 0x00, 0x80, 0x00, 0x00,
	0x00, 0x00, 0x00, 0xff, 0xff, 0xff, 0x21, 0xf9, 0x04, 0x01, 0x00, 0x00, 0x00,
	0x00, 0x2c, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x02, 0x02,
	0x44, 0x01, 0x00, 0x3b,
}

// emailTrackOpen — GET /email-track/open/{token}, без аутентификации
// (получатель письма не залогинен в консоль). Раздел VI.25 ТЗ: пиксель
// фиксирует факт загрузки картинки почтовым клиентом — это метрика
// взаимодействия, не юридическое доказательство прочтения (клиент может
// блокировать картинки, корпоративный прокси — предзагружать их).
// Пиксель отдаётся ВСЕГДА, даже если токен не найден — сломанная
// картинка в письме выглядела бы подозрительно и ничего не даёт атакующему.
func (server *Server) emailTrackOpen(response http.ResponseWriter, request *http.Request, path string) {
	token := strings.TrimPrefix(path, "/email-track/open/")
	if token != "" {
		_, _ = server.pool.Exec(request.Context(), `
			UPDATE email_tracking_links
			SET hit_count = hit_count + 1, first_hit_at = COALESCE(first_hit_at, $2)
			WHERE token = $1 AND kind = 'open'`, token, time.Now().UTC())
	}
	response.Header().Set("Content-Type", "image/gif")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(transparentGIF)
}

// emailTrackClick — GET /email-track/click/{token}. Раздел VI.26 ТЗ:
// destination берётся ИСКЛЮЧИТЕЛЬНО из email_tracking_links.target_url,
// записанного на сервере в момент отправки письма (internal/deliveryemail)
// — токен из URL никогда не интерпретируется как сам redirect-адрес и
// сама ссылка не читается из query-параметра запроса, поэтому подменить
// destination, меняя запрос, невозможно (защита от open redirect).
func (server *Server) emailTrackClick(response http.ResponseWriter, request *http.Request, path string) {
	token := strings.TrimPrefix(path, "/email-track/click/")
	var targetURL string
	err := server.pool.QueryRow(request.Context(), `
		UPDATE email_tracking_links
		SET hit_count = hit_count + 1, first_hit_at = COALESCE(first_hit_at, $2)
		WHERE token = $1 AND kind = 'click'
		RETURNING target_url`, token, time.Now().UTC(),
	).Scan(&targetURL)
	if err != nil || targetURL == "" {
		writeError(response, http.StatusNotFound, "Ссылка недействительна")
		return
	}
	http.Redirect(response, request, targetURL, http.StatusFound)
}

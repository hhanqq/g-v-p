package adminapi

import (
	"net/http"
	"time"
)

// analyticsRange — общий разбор фильтра периода для всех /api/analytics/*
// эндпоинтов (раздел III.12 ТЗ: единые фильтры на всю страницу). custom
// принимает from/to как YYYY-MM-DD; по умолчанию — 14 дней (тот же
// период, что уже был у прежнего "алерты за 14 дней").
type analyticsRange struct {
	From, To time.Time
	Days     int
}

func parseAnalyticsRange(request *http.Request) analyticsRange {
	to := time.Now().UTC()
	query := request.URL.Query()
	if query.Get("period") == "custom" {
		from, errFrom := time.Parse("2006-01-02", query.Get("from"))
		toParsed, errTo := time.Parse("2006-01-02", query.Get("to"))
		if errFrom == nil && errTo == nil && toParsed.After(from) {
			toParsed = toParsed.Add(24 * time.Hour)
			return analyticsRange{From: from, To: toParsed, Days: int(toParsed.Sub(from).Hours()/24) + 1}
		}
	}
	days := 14
	switch query.Get("period") {
	case "24h":
		return analyticsRange{From: to.Add(-24 * time.Hour), To: to, Days: 1}
	case "7d":
		days = 7
	case "30d":
		days = 30
	case "90d":
		days = 90
	}
	return analyticsRange{From: to.AddDate(0, 0, -days), To: to, Days: days}
}

// siteFilter — раздел III.12: фильтр «Филиал», применим везде, где
// запрос уже так или иначе идёт через cmdb_objects.site.
func siteFilter(request *http.Request) string {
	return request.URL.Query().Get("site")
}

func priorityFilter(request *http.Request) string {
	return request.URL.Query().Get("priority")
}

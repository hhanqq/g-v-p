package adminapi

import (
	"context"
	"net/http"
)

// analyticsDelivery — GET /api/analytics/delivery?period=. Раздел V/VI/VII
// ТЗ в одном ответе: TrueConf (sent/success/ack/MTTA/эскалации), Email
// (sent/accepted/open/click — раздел VI, реальный tracking, не
// придуманные проценты), сравнение каналов, ACK rate по приоритету,
// распределение MTTA по бакетам.
func (server *Server) analyticsDelivery(response http.ResponseWriter, request *http.Request, _ map[string]any) {
	ctx := request.Context()
	rng := parseAnalyticsRange(request)

	trueconf, err := server.trueconfDeliveryStats(ctx, rng)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	email, err := server.emailDeliveryStats(ctx, rng)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	ackByPriority, err := server.ackRateByPriority(ctx, rng)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	mttaBuckets, err := server.mttaDistribution(ctx, rng)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	ackSeries, err := server.ackRateTimeseries(ctx, rng)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	writeJSON(response, http.StatusOK, map[string]any{
		"trueconf": trueconf, "email": email,
		"ack_rate_by_priority": ackByPriority, "mtta_distribution": mttaBuckets,
		"ack_rate_series": ackSeries,
	})
}

func (server *Server) trueconfDeliveryStats(ctx context.Context, rng analyticsRange) (map[string]any, error) {
	var created, sent, failed int
	if err := server.pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE status='sent'), count(*) FILTER (WHERE status='failed')
		FROM delivery_outbox WHERE channel='trueconf' AND created_at >= $1 AND created_at < $2`,
		rng.From, rng.To,
	).Scan(&created, &sent, &failed); err != nil {
		return nil, err
	}
	var requiringAck, acknowledged, escalations int
	if err := server.pool.QueryRow(ctx, `
		SELECT count(DISTINCT problem.id), count(DISTINCT problem.id) FILTER (WHERE problem.acknowledged_at IS NOT NULL)
		FROM notifications n JOIN problems problem ON problem.id = n.problem_id
		WHERE n.type='NEW' AND n.created_at >= $1 AND n.created_at < $2`,
		rng.From, rng.To,
	).Scan(&requiringAck, &acknowledged); err != nil {
		return nil, err
	}
	if err := server.pool.QueryRow(ctx, `
		SELECT count(*) FROM sla_breach_notices WHERE created_at >= $1 AND created_at < $2`,
		rng.From, rng.To,
	).Scan(&escalations); err != nil {
		return nil, err
	}
	mtta, err := queryNullableFloat(ctx, server.pool, `
		SELECT AVG(EXTRACT(EPOCH FROM (problem.acknowledged_at - n.created_at)))
		FROM notifications n JOIN problems problem ON problem.id = n.problem_id
		WHERE n.type='NEW' AND problem.acknowledged_at IS NOT NULL AND problem.acknowledged_at >= n.created_at
		  AND n.created_at >= $1 AND n.created_at < $2`, rng.From, rng.To)
	if err != nil {
		return nil, err
	}
	var ackRate *float64
	if requiringAck > 0 {
		value := round(float64(acknowledged)*100/float64(requiringAck), 1)
		ackRate = &value
	}
	var successRate *float64
	if created > 0 {
		value := round(float64(sent)*100/float64(created), 1)
		successRate = &value
	}
	return map[string]any{
		"created": created, "sent": sent, "failed": failed, "success_rate_pct": successRate,
		"requiring_ack": requiringAck, "acknowledged": acknowledged, "ack_rate_pct": ackRate,
		"mtta_seconds": mtta, "escalations": escalations,
	}, nil
}

func (server *Server) emailDeliveryStats(ctx context.Context, rng analyticsRange) (map[string]any, error) {
	var created, sent, failed int
	if err := server.pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE status='sent'), count(*) FILTER (WHERE status='failed')
		FROM delivery_outbox WHERE channel='email' AND created_at >= $1 AND created_at < $2`,
		rng.From, rng.To,
	).Scan(&created, &sent, &failed); err != nil {
		return nil, err
	}
	var opened, clicked int
	if err := server.pool.QueryRow(ctx, `
		SELECT count(DISTINCT n.id) FILTER (WHERE link.kind='open' AND link.hit_count>0),
			count(DISTINCT n.id) FILTER (WHERE link.kind='click' AND link.hit_count>0)
		FROM notifications n
		JOIN delivery_outbox o ON o.notification_id = n.id AND o.channel='email'
		LEFT JOIN email_tracking_links link ON link.notification_id = n.id
		WHERE o.created_at >= $1 AND o.created_at < $2`,
		rng.From, rng.To,
	).Scan(&opened, &clicked); err != nil {
		return nil, err
	}
	pct := func(part, total int) *float64 {
		if total == 0 {
			return nil
		}
		value := round(float64(part)*100/float64(total), 1)
		return &value
	}
	return map[string]any{
		"created": created, "sent": sent, "failed": failed,
		"opened": opened, "clicked": clicked,
		"open_rate_pct": pct(opened, sent), "ctr_pct": pct(clicked, sent), "ctor_pct": pct(clicked, opened),
	}, nil
}

func (server *Server) ackRateByPriority(ctx context.Context, rng analyticsRange) ([]map[string]any, error) {
	rows, err := server.pool.Query(ctx, `
		SELECT problem.priority,
			count(DISTINCT problem.id) AS requiring_ack,
			count(DISTINCT problem.id) FILTER (WHERE problem.acknowledged_at IS NOT NULL) AS acknowledged
		FROM notifications n JOIN problems problem ON problem.id = n.problem_id
		WHERE n.type='NEW' AND problem.priority IS NOT NULL AND n.created_at >= $1 AND n.created_at < $2
		GROUP BY problem.priority ORDER BY problem.priority`, rng.From, rng.To)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]map[string]any, 0)
	for rows.Next() {
		var priority string
		var requiring, acked int
		if err := rows.Scan(&priority, &requiring, &acked); err != nil {
			return nil, err
		}
		var rate *float64
		if requiring > 0 {
			value := round(float64(acked)*100/float64(requiring), 1)
			rate = &value
		}
		result = append(result, map[string]any{"priority": priority, "ack_rate_pct": rate, "total": requiring})
	}
	return result, rows.Err()
}

// mttaDistribution — раздел V.22 ТЗ: гистограмма, не только среднее —
// среднее само по себе прячет выбросы.
func (server *Server) mttaDistribution(ctx context.Context, rng analyticsRange) ([]map[string]any, error) {
	buckets := []struct {
		label        string
		lower, upper float64 // seconds; upper<0 = без верхней границы
	}{
		{"< 1 минуты", 0, 60},
		{"1–2 минуты", 60, 120},
		{"2–5 минут", 120, 300},
		{"5–10 минут", 300, 600},
		{"> 10 минут", 600, -1},
	}
	result := make([]map[string]any, 0, len(buckets))
	for _, bucket := range buckets {
		var count int
		var err error
		if bucket.upper < 0 {
			err = server.pool.QueryRow(ctx, `
				SELECT count(*) FROM problems
				WHERE acknowledged_at IS NOT NULL AND acknowledged_at >= opened_at
				  AND opened_at >= $1 AND opened_at < $2
				  AND EXTRACT(EPOCH FROM (acknowledged_at-opened_at)) >= $3`,
				rng.From, rng.To, bucket.lower).Scan(&count)
		} else {
			err = server.pool.QueryRow(ctx, `
				SELECT count(*) FROM problems
				WHERE acknowledged_at IS NOT NULL AND acknowledged_at >= opened_at
				  AND opened_at >= $1 AND opened_at < $2
				  AND EXTRACT(EPOCH FROM (acknowledged_at-opened_at)) >= $3
				  AND EXTRACT(EPOCH FROM (acknowledged_at-opened_at)) < $4`,
				rng.From, rng.To, bucket.lower, bucket.upper).Scan(&count)
		}
		if err != nil {
			return nil, err
		}
		result = append(result, map[string]any{"bucket": bucket.label, "count": count})
	}
	return result, nil
}

func (server *Server) ackRateTimeseries(ctx context.Context, rng analyticsRange) ([]map[string]any, error) {
	rows, err := server.pool.Query(ctx, `
		SELECT TO_CHAR(day,'YYYY-MM-DD'),
			count(DISTINCT problem.id) AS requiring_ack,
			count(DISTINCT problem.id) FILTER (WHERE problem.acknowledged_at IS NOT NULL) AS acknowledged
		FROM GENERATE_SERIES(date_trunc('day',$1::timestamp), date_trunc('day',$2::timestamp), INTERVAL '1 day') day
		LEFT JOIN notifications n ON n.type='NEW' AND n.created_at>=day AND n.created_at<day+INTERVAL '1 day'
		LEFT JOIN problems problem ON problem.id = n.problem_id
		GROUP BY day ORDER BY day`, rng.From, rng.To)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]map[string]any, 0)
	for rows.Next() {
		var day string
		var requiring, acked int
		if err := rows.Scan(&day, &requiring, &acked); err != nil {
			return nil, err
		}
		var rate *float64
		if requiring > 0 {
			value := round(float64(acked)*100/float64(requiring), 1)
			rate = &value
		}
		result = append(result, map[string]any{"day": day, "ack_rate_pct": rate})
	}
	return result, rows.Err()
}

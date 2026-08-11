package pipeline

import (
	"context"
	"errors"
	"math"
	"strings"

	"github.com/jackc/pgx/v5"
)

const fuzzyCutoff = 0.75

func resolve(ctx context.Context, tx pgx.Tx, site, rawName, ip *string) (Resolution, error) {
	if site == nil || rawName == nil || *site == "" || *rawName == "" {
		return Resolution{Method: "quarantine"}, nil
	}
	if objectID, found, err := aliasLookup(ctx, tx, *site, *rawName); err != nil {
		return Resolution{}, err
	} else if found {
		confidence := 1.0
		return Resolution{ObjectID: &objectID, Method: "alias", Confidence: &confidence}, nil
	}
	if dot := strings.Index(*rawName, "."); dot > 0 {
		if objectID, found, err := aliasLookup(ctx, tx, *site, (*rawName)[:dot]); err != nil {
			return Resolution{}, err
		} else if found {
			confidence := 0.95
			return Resolution{ObjectID: &objectID, Method: "fqdn", Confidence: &confidence}, nil
		}
	}
	if ip != nil && *ip != "" {
		var objectID string
		err := tx.QueryRow(ctx, `SELECT id FROM cmdb_objects WHERE site=$1 AND ip=$2 LIMIT 1`, *site, *ip).Scan(&objectID)
		if err == nil {
			confidence := 0.9
			return Resolution{ObjectID: &objectID, Method: "ip", Confidence: &confidence}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return Resolution{}, err
		}
	}
	rows, err := tx.Query(ctx, `SELECT id, name FROM cmdb_objects WHERE site=$1`, *site)
	if err != nil {
		return Resolution{}, err
	}
	defer rows.Close()
	bestRatio := 0.0
	bestID := ""
	for rows.Next() {
		var objectID, name string
		if err := rows.Scan(&objectID, &name); err != nil {
			return Resolution{}, err
		}
		ratio := similarityRatio(*rawName, name)
		if ratio > bestRatio {
			bestRatio, bestID = ratio, objectID
		}
	}
	if err := rows.Err(); err != nil {
		return Resolution{}, err
	}
	if bestRatio >= fuzzyCutoff {
		confidence := math.Round(bestRatio*1000) / 1000
		return Resolution{ObjectID: &bestID, Method: "fuzzy", Confidence: &confidence}, nil
	}
	return Resolution{Method: "quarantine"}, nil
}

func aliasLookup(ctx context.Context, tx pgx.Tx, site, rawName string) (string, bool, error) {
	var objectID string
	err := tx.QueryRow(ctx, `SELECT object_id FROM cmdb_aliases WHERE site=$1 AND raw_name=$2 LIMIT 1`, site, rawName).Scan(&objectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return objectID, err == nil, err
}

func similarityRatio(first, second string) float64 {
	a, b := []rune(first), []rune(second)
	maximum := len(a)
	if len(b) > maximum {
		maximum = len(b)
	}
	if maximum == 0 {
		return 1
	}
	previous := make([]int, len(b)+1)
	for index := range previous {
		previous[index] = index
	}
	for i, left := range a {
		current := make([]int, len(b)+1)
		current[0] = i + 1
		for j, right := range b {
			cost := 1
			if left == right {
				cost = 0
			}
			current[j+1] = min(current[j]+1, previous[j+1]+1, previous[j]+cost)
		}
		previous = current
	}
	return 1 - float64(previous[len(b)])/float64(maximum)
}

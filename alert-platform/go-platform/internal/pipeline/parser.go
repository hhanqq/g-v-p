package pipeline

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
)

func Parse(connector Connector, rawBody, sourceInstance string, receivedAt time.Time, site *string) ParseResult {
	result := ParseResult{RawBody: rawBody}
	var missing []string
	for _, name := range connector.RequiredFields {
		if extract(connector, name, rawBody) == nil {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		result.Error = fmt.Sprintf("обязательные поля не найдены: %v", missing)
		return result
	}
	statePrefix := extract(connector, "state_prefix", rawBody)
	state, ok := connector.StateMap[value(statePrefix)]
	if !ok {
		result.Error = fmt.Sprintf("неизвестный префикс состояния: %s", value(statePrefix))
		return result
	}
	title := extract(connector, "title", rawBody)
	objectName := extract(connector, "host_raw", rawBody)
	if objectName == nil {
		objectName = extract(connector, "node_raw", rawBody)
	}
	ip := extract(connector, "ip_raw", rawBody)
	severity := extract(connector, "severity_raw", rawBody)
	timeRaw := extract(connector, "time_raw", rawBody)
	occurredAt, err := time.ParseInLocation(goTimeLayout(connector.TimeFormat), value(timeRaw), time.UTC)
	if err != nil {
		occurredAt = receivedAt
	}
	symptomClass, component := matchSymptom(connector, value(title))
	result.Success = true
	result.Event = &Event{
		OccurredAt: occurredAt, IngestTS: receivedAt, State: state,
		SymptomClass: symptomClass, SeverityRaw: severity, Title: value(title), BodyRaw: rawBody,
		Site: site, ObjectNameRaw: objectName, IPRaw: ip, Component: component,
		ParserVersion: connector.Version,
	}
	return result
}

func extract(connector Connector, name, text string) *string {
	pattern, ok := connector.FieldPatterns[name]
	if !ok {
		return nil
	}
	match := pattern.FindStringSubmatch(text)
	if len(match) < 2 {
		return nil
	}
	return stringPointer(match[1])
}

func matchSymptom(connector Connector, title string) (string, *string) {
	for _, rule := range connector.SymptomRules {
		match := rule.Pattern.FindStringSubmatch(title)
		if match == nil {
			continue
		}
		if rule.ComponentGroup > 0 && rule.ComponentGroup < len(match) {
			return rule.SymptomClass, stringPointer(match[rule.ComponentGroup])
		}
		return rule.SymptomClass, nil
	}
	return "unknown", nil
}

func ComputeDedupKey(site, objectID, component *string, symptomClass string) *string {
	if site == nil || *site == "" || objectID == nil || *objectID == "" {
		return nil
	}
	raw := *site + "|" + *objectID + "|" + value(component) + "|" + symptomClass
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))
	return &hash
}

func goTimeLayout(pythonLayout string) string {
	replacer := strings.NewReplacer(
		"%Y", "2006", "%m", "01", "%d", "02", "%I", "03",
		"%H", "15", "%M", "04", "%S", "05", "%p", "PM",
	)
	return replacer.Replace(pythonLayout)
}

func value(pointer *string) string {
	if pointer == nil {
		return ""
	}
	return *pointer
}

func stringPointer(value string) *string { return &value }

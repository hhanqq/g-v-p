package pipeline

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

type parserFixture struct {
	SourceInstance string `json:"source_instance"`
	RawBody        string `json:"raw_body"`
	Expected       struct {
		State        string  `json:"state"`
		SymptomClass string  `json:"symptom_class"`
		Component    *string `json:"component"`
	} `json:"expected"`
}

func TestConnectorSamples(t *testing.T) {
	connectors, err := LoadConnectors(projectPath(t, "connectors"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		system, body, state, symptom, component string
	}{
		{"zabbix", "PROBLEM: Free disk space is less than 70% on volume C:\nHost: db-03 (10.42.50.5)\nSeverity: Warning\nTime: 2026.08.06 03:35:21", "firing", "disk_space", "C:"},
		{"solarwinds", "Reset: Interface Gi0/1 on sw-acc-05 is Up\nNode: sw-acc-05 (10.55.57.3)\nTriggered: 08/06/2026 05:22:25 PM", "resolved", "interface_down", "Gi0/1"},
	}
	site := "brd-noyabrsk"
	for _, test := range tests {
		result := Parse(connectors[test.system], test.body, "test-instance", time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC), &site)
		if !result.Success {
			t.Fatalf("%s did not parse: %s", test.system, result.Error)
		}
		if result.Event.State != test.state || result.Event.SymptomClass != test.symptom || value(result.Event.Component) != test.component {
			t.Fatalf("unexpected event: %#v", result.Event)
		}
	}
}

func TestAllPythonGoldenFixtures(t *testing.T) {
	connectors, err := LoadConnectors(projectPath(t, "connectors"))
	if err != nil {
		t.Fatal(err)
	}
	site := "fixture-site"
	for _, system := range []string{"zabbix", "solarwinds"} {
		path := projectPath(t, "fixtures", "parser_samples", system+".jsonl")
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(file)
		line := 0
		for scanner.Scan() {
			line++
			var fixture parserFixture
			if err := json.Unmarshal(scanner.Bytes(), &fixture); err != nil {
				file.Close()
				t.Fatalf("%s:%d: %v", path, line, err)
			}
			result := Parse(connectors[system], fixture.RawBody, fixture.SourceInstance, time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC), &site)
			if !result.Success {
				file.Close()
				t.Fatalf("%s:%d: %s", path, line, result.Error)
			}
			if result.Event.State != fixture.Expected.State || result.Event.SymptomClass != fixture.Expected.SymptomClass || value(result.Event.Component) != value(fixture.Expected.Component) {
				file.Close()
				t.Fatalf("%s:%d: unexpected event %#v", path, line, result.Event)
			}
		}
		if err := scanner.Err(); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMalformedPreservesRawBody(t *testing.T) {
	connectors, err := LoadConnectors(projectPath(t, "connectors"))
	if err != nil {
		t.Fatal(err)
	}
	body := "PROBLEM: something broke\nSeverity: High"
	result := Parse(connectors["zabbix"], body, "test", time.Now(), nil)
	if result.Success || result.RawBody != body || result.Event != nil {
		t.Fatalf("unexpected malformed result: %#v", result)
	}
}

func TestDedupKeyMatchesCanonicalFormula(t *testing.T) {
	site, object, component := "brd-noyabrsk", "app-01", "C:"
	key := ComputeDedupKey(&site, &object, &component, "disk_space")
	if key == nil || *key != "699da717dc9bd083b557b570d31066d33954092cfb4140bab7a465338d4d9ef9" {
		t.Fatalf("unexpected dedup key: %v", key)
	}
	if ComputeDedupKey(nil, &object, nil, "disk_space") != nil {
		t.Fatal("dedup key without site must be nil")
	}
}

func projectPath(t *testing.T, parts ...string) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
	return filepath.Join(append([]string{root}, parts...)...)
}

package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

type connectorYAML struct {
	SourceSystem   string            `yaml:"source_system"`
	Version        int               `yaml:"version"`
	StateMap       map[string]string `yaml:"state_map"`
	SeverityMap    map[string]int    `yaml:"severity_map"`
	TimeFormat     string            `yaml:"time_format"`
	Fields         map[string]string `yaml:"fields"`
	RequiredFields []string          `yaml:"required_fields"`
	SymptomRules   []struct {
		Match          string `yaml:"match"`
		SymptomClass   string `yaml:"symptom_class"`
		ComponentGroup int    `yaml:"component_group"`
	} `yaml:"symptom_rules"`
}

type sourceSeed struct {
	Sources []struct {
		Instance string `yaml:"instance"`
		System   string `yaml:"system"`
		Site     string `yaml:"site"`
	} `yaml:"sources"`
}

func LoadConnectors(dir string) (map[string]Connector, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	connectors := make(map[string]Connector)
	for _, path := range paths {
		if filepath.Base(path) == "sources.yaml" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var raw connectorYAML
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("decode connector %s: %w", path, err)
		}
		if raw.SourceSystem == "" || raw.Version == 0 || len(raw.StateMap) == 0 || len(raw.Fields) == 0 || len(raw.RequiredFields) == 0 {
			return nil, fmt.Errorf("connector %s misses required fields", path)
		}
		connector := Connector{
			SourceSystem: raw.SourceSystem, Version: raw.Version, StateMap: raw.StateMap,
			SeverityMap: raw.SeverityMap, TimeFormat: raw.TimeFormat,
			FieldPatterns: make(map[string]fieldPattern), RequiredFields: raw.RequiredFields,
		}
		for name, expression := range raw.Fields {
			compiled, err := regexp.Compile(expression)
			if err != nil {
				return nil, fmt.Errorf("connector %s field %s: %w", path, name, err)
			}
			connector.FieldPatterns[name] = compiled
		}
		for _, rule := range raw.SymptomRules {
			compiled, err := regexp.Compile(rule.Match)
			if err != nil {
				return nil, fmt.Errorf("connector %s symptom %s: %w", path, rule.SymptomClass, err)
			}
			connector.SymptomRules = append(connector.SymptomRules, symptomRule{
				Pattern: compiled, SymptomClass: rule.SymptomClass, ComponentGroup: rule.ComponentGroup,
			})
		}
		connectors[connector.SourceSystem] = connector
	}
	if len(connectors) == 0 {
		return nil, fmt.Errorf("no connectors found in %s", dir)
	}
	return connectors, nil
}

func loadSourceSeed(path string) (sourceSeed, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return sourceSeed{}, err
	}
	var seed sourceSeed
	if err := yaml.Unmarshal(data, &seed); err != nil {
		return sourceSeed{}, err
	}
	return seed, nil
}

func LoadPriorityConfig(path string) (priorityConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return priorityConfig{}, err
	}
	var cfg priorityConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return priorityConfig{}, err
	}
	if len(cfg.Matrix) != 6 {
		return priorityConfig{}, fmt.Errorf("priority matrix must contain 6 rows")
	}
	for i, row := range cfg.Matrix {
		if len(row) != 6 {
			return priorityConfig{}, fmt.Errorf("priority matrix row %d must contain 6 columns", i)
		}
	}
	return cfg, nil
}

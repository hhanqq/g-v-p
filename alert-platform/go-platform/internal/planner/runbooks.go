package planner

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

func LoadRunbooks(path string) (map[string][]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read runbooks: %w", err)
	}
	var runbooks map[string][]string
	if err := yaml.Unmarshal(body, &runbooks); err != nil {
		return nil, fmt.Errorf("parse runbooks: %w", err)
	}
	return runbooks, nil
}

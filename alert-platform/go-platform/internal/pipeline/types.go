package pipeline

import "time"

const (
	defaultTechnicalSeverity = 2
	defaultFlapThreshold     = 6
	defaultFlapWindow        = 180 * time.Second
)

var activeStatuses = []string{"OPEN", "ACKNOWLEDGED", "FLAPPING"}

type Connector struct {
	SourceSystem   string
	Version        int
	StateMap       map[string]string
	SeverityMap    map[string]int
	TimeFormat     string
	FieldPatterns  map[string]fieldPattern
	RequiredFields []string
	SymptomRules   []symptomRule
}

type fieldPattern interface {
	FindStringSubmatch(string) []string
}

type symptomRule struct {
	Pattern        fieldPattern
	SymptomClass   string
	ComponentGroup int
}

type Event struct {
	OccurredAt    time.Time
	IngestTS      time.Time
	State         string
	SymptomClass  string
	SeverityRaw   *string
	Title         string
	BodyRaw       string
	Site          *string
	ObjectNameRaw *string
	IPRaw         *string
	Component     *string
	ParserVersion int
}

type ParseResult struct {
	Success bool
	Event   *Event
	Error   string
	RawBody string
}

type Resolution struct {
	ObjectID   *string
	Method     string
	Confidence *float64
}

type problem struct {
	ID                   int64
	DedupKey             string
	Status               string
	ObjectID             *string
	Component            *string
	SymptomClass         string
	Site                 *string
	OpenedAt             time.Time
	ResolvedAt           *time.Time
	LastSeenAt           time.Time
	RepeatCount          int
	ToggleCount          int
	Priority             *string
	IncidentID           *int64
	DuplicateOfProblemID *int64
}

type priorityConfig struct {
	Matrix                      [][]string       `yaml:"matrix"`
	BusinessImpactByCriticality map[string]int   `yaml:"business_impact_by_criticality"`
	Modifiers                   priorityModifier `yaml:"modifiers"`
}

type priorityModifier struct {
	RepeatThreshold         int   `yaml:"repeat_threshold"`
	RepeatBump              int   `yaml:"repeat_bump"`
	NightHours              []int `yaml:"night_hours"`
	NightBump               int   `yaml:"night_bump"`
	MaintenanceActiveDebump int   `yaml:"maintenance_active_debump"`
}

type PriorityBreakdown struct {
	TechnicalSeverity int      `json:"technical_severity"`
	BusinessImpact    int      `json:"business_impact"`
	MatrixCell        string   `json:"matrix_cell"`
	ModifiersApplied  []string `json:"modifiers_applied"`
	FinalPriority     string   `json:"final_priority"`
}

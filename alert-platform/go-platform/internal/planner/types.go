package planner

import "time"

type ProblemData struct {
	ID                     int64
	IncidentID             *int64
	Priority               *string
	ObjectID               *string
	ObjectName             string
	EquipmentType          *string
	Site                   *string
	ServiceName            *string
	SymptomClass           string
	AIRootCauseHypothesis  *string
	OriginalBody           string
	SourceSystem           string
	OpenedAt               time.Time
	ResolvedAt             time.Time
	ClosedByReconciliation bool
	AcknowledgedAt         *time.Time
}

type NotificationTarget struct {
	ParentNotificationID int64
	ChatID               string
	Recipient            string
}

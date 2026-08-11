// Package rbac — модель доступа ADP: role presets + индивидуальные
// grant/deny + scope данных. Роли и атомарные permissions — фиксированный
// список (раздел 8-16 доп. ТЗ), не редактируются администратором как
// произвольные записи; индивидуальные исключения и scope — per-user,
// хранятся в БД (см. миграцию 0019_rbac.sql) и грузятся в internal/adminapi.
package rbac

type Permission string

const (
	DashboardRead      Permission = "dashboard.read"
	AlertsRead         Permission = "alerts.read"
	AlertsAck          Permission = "alerts.ack"
	IncidentsRead      Permission = "incidents.read"
	IncidentsManage    Permission = "incidents.manage"
	EquipmentRead      Permission = "equipment.read"
	EquipmentManage    Permission = "equipment.manage"
	EmployeesRead      Permission = "employees.read"
	EmployeesManage    Permission = "employees.manage"
	AvailabilityRead   Permission = "availability.read"
	AvailabilityManage Permission = "availability.manage"
	CoverageRead       Permission = "coverage.read"
	CoverageManage     Permission = "coverage.manage"
	ScenariosRead      Permission = "scenarios.read"
	ScenariosManage    Permission = "scenarios.manage"
	ScenariosActivate  Permission = "scenarios.activate"
	SLARead            Permission = "sla.read"
	SLAManage          Permission = "sla.manage"
	AnalyticsRead      Permission = "analytics.read"
	AnalyticsExport    Permission = "analytics.export"
	IntegrationsRead   Permission = "integrations.read"
	IntegrationsManage Permission = "integrations.manage"
	SourcesRead        Permission = "sources.read"
	SourcesManage      Permission = "sources.manage"
	UsersRead          Permission = "users.read"
	UsersManage        Permission = "users.manage"
	AuditRead          Permission = "audit.read"
	PlatformHealthRead Permission = "platform_health.read"
	HelpRead           Permission = "help.read"
)

// AllPermissions — порядок фиксирован для стабильного отображения в
// карточке пользователя (раздел 12 доп. ТЗ).
var AllPermissions = []Permission{
	DashboardRead,
	AlertsRead, AlertsAck,
	IncidentsRead, IncidentsManage,
	EquipmentRead, EquipmentManage,
	EmployeesRead, EmployeesManage,
	AvailabilityRead, AvailabilityManage,
	CoverageRead, CoverageManage,
	ScenariosRead, ScenariosManage, ScenariosActivate,
	SLARead, SLAManage,
	AnalyticsRead, AnalyticsExport,
	IntegrationsRead, IntegrationsManage,
	SourcesRead, SourcesManage,
	UsersRead, UsersManage,
	AuditRead,
	PlatformHealthRead,
	HelpRead,
}

var PermissionLabels = map[Permission]string{
	DashboardRead:      "Главная",
	AlertsRead:         "Алерты — просмотр",
	AlertsAck:          "Алерты — подтверждение (ACK)",
	IncidentsRead:      "Инциденты — просмотр",
	IncidentsManage:    "Инциденты — управление",
	EquipmentRead:      "Оборудование — просмотр",
	EquipmentManage:    "Оборудование — управление",
	EmployeesRead:      "Сотрудники — просмотр",
	EmployeesManage:    "Сотрудники — управление",
	AvailabilityRead:   "Доступность — просмотр",
	AvailabilityManage: "Доступность — управление",
	CoverageRead:       "Покрытие — просмотр",
	CoverageManage:     "Покрытие — управление",
	ScenariosRead:      "Сценарии — просмотр",
	ScenariosManage:    "Сценарии — редактирование",
	ScenariosActivate:  "Сценарии — активация",
	SLARead:            "SLA — просмотр",
	SLAManage:          "SLA — редактирование",
	AnalyticsRead:      "Аналитика — просмотр",
	AnalyticsExport:    "Аналитика — экспорт/BI",
	IntegrationsRead:   "Интеграции — просмотр",
	IntegrationsManage: "Интеграции — управление",
	SourcesRead:        "Источники — просмотр",
	SourcesManage:      "Источники — управление",
	UsersRead:          "Пользователи и права — просмотр",
	UsersManage:        "Пользователи и права — управление",
	AuditRead:          "Аудит / история изменений",
	PlatformHealthRead: "Состояние системы",
	HelpRead:           "Справка",
}

type Role string

const (
	RolePlatformAdmin     Role = "platform_admin"
	RoleDispatcher        Role = "dispatcher"
	RoleEngineer          Role = "engineer"
	RoleServiceOwner      Role = "service_owner"
	RoleAutomationManager Role = "automation_manager"
	RoleAuditor           Role = "auditor"
	RoleGuest             Role = "guest"
)

var AllRoles = []Role{
	RolePlatformAdmin, RoleDispatcher, RoleEngineer, RoleServiceOwner,
	RoleAutomationManager, RoleAuditor, RoleGuest,
}

var RoleLabels = map[Role]string{
	RolePlatformAdmin:     "Разработчик / администратор платформы",
	RoleDispatcher:        "Диспетчер",
	RoleEngineer:          "Инженер",
	RoleServiceOwner:      "Владелец сервиса",
	RoleAutomationManager: "Менеджер автоматизации",
	RoleAuditor:           "Аудитор",
	RoleGuest:             "Гость",
}

func perms(list ...Permission) map[Permission]bool {
	out := make(map[Permission]bool, len(list))
	for _, p := range list {
		out[p] = true
	}
	return out
}

// RolePermissions — стартовые наборы прав по ролям (раздел 10 доп. ТЗ).
// Administrator получает всё; для dispatcher намеренно включено
// sla.manage — раздел 14 явно приводит пример «диспетчер видит всё по
// роли, кроме редактирования SLA», что подразумевает sla.manage в
// базовой роли, отключаемый индивидуальным deny у конкретного человека.
var RolePermissions = map[Role]map[Permission]bool{
	RolePlatformAdmin: perms(AllPermissions...),
	RoleDispatcher: perms(
		DashboardRead, AlertsRead, AlertsAck,
		IncidentsRead, IncidentsManage,
		EmployeesRead, EmployeesManage,
		AvailabilityRead, AvailabilityManage,
		CoverageRead, CoverageManage,
		SLARead, SLAManage,
		AnalyticsRead, HelpRead,
	),
	RoleEngineer: perms(
		DashboardRead, AlertsRead, AlertsAck,
		IncidentsRead, EquipmentRead, HelpRead,
	),
	RoleServiceOwner: perms(
		DashboardRead, EquipmentRead, IncidentsRead,
		SLARead, CoverageRead, AnalyticsRead, HelpRead,
	),
	RoleAutomationManager: perms(
		DashboardRead, ScenariosRead, ScenariosManage, ScenariosActivate,
		SLARead, SLAManage, AnalyticsRead, HelpRead,
	),
	RoleAuditor: perms(
		DashboardRead, AuditRead, AnalyticsRead, IncidentsRead, HelpRead,
	),
	RoleGuest: perms(
		DashboardRead, AlertsRead, IncidentsRead, EquipmentRead,
		EmployeesRead, CoverageRead, ScenariosRead, SLARead,
		AnalyticsRead, PlatformHealthRead, HelpRead,
	),
}

type ScopeType string

const (
	ScopeSite          ScopeType = "site"
	ScopeSubsidiary    ScopeType = "subsidiary"
	ScopeService       ScopeType = "service"
	ScopeEquipmentType ScopeType = "equipment_type"
	ScopeObject        ScopeType = "object_id"
)

type Scope struct {
	Type  ScopeType `json:"type"`
	Value string    `json:"value"`
}

// Grant — вычисленный доступ конкретной сессии: роль + индивидуальные
// grant/deny + scope. Effective() — то самое объединение из раздела 14
// доп. ТЗ («Role permissions + Individual grants - Individual denies»).
type Grant struct {
	Role      Role
	Overrides map[Permission]bool // true = grant, false = deny
	Scopes    []Scope
}

func (g Grant) Effective() map[Permission]bool {
	base := RolePermissions[g.Role]
	out := make(map[Permission]bool, len(base)+len(g.Overrides))
	for p, v := range base {
		out[p] = v
	}
	for p, grant := range g.Overrides {
		out[p] = grant
	}
	return out
}

func (g Grant) Has(permission Permission) bool {
	return g.Effective()[permission]
}

// HasScope — пусто (нет ни одной записи scope у пользователя) означает
// «без ограничения» (раздел 13 доп. ТЗ не требует scope по умолчанию —
// это дополнительное сужение, а не обязательное условие доступа).
func (g Grant) HasScope(scopeType ScopeType) bool {
	for _, s := range g.Scopes {
		if s.Type == scopeType {
			return true
		}
	}
	return false
}

func (g Grant) ScopeValues(scopeType ScopeType) []string {
	values := make([]string, 0)
	for _, s := range g.Scopes {
		if s.Type == scopeType {
			values = append(values, s.Value)
		}
	}
	return values
}

// AllowsSite — true если у пользователя нет scope-ограничения по
// филиалу вовсе, или указанный филиал входит в список разрешённых.
func (g Grant) AllowsSite(site string) bool {
	if !g.HasScope(ScopeSite) {
		return true
	}
	for _, v := range g.ScopeValues(ScopeSite) {
		if v == site {
			return true
		}
	}
	return false
}

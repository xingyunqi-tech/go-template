package entity

// AlertLevel is the nestjs AlertLevel.
type AlertLevel string

// Alert levels.
const (
	AlertInfo     AlertLevel = "INFO"
	AlertWarn     AlertLevel = "WARN"
	AlertCritical AlertLevel = "CRITICAL"
)

// Alert is an admin_alert row to raise.
type Alert struct {
	Type     string
	Level    AlertLevel
	Message  string
	Payload  map[string]any
	DedupKey string
}

// Notification is an in-app notification row.
type Notification struct {
	UserID   int
	Type     string
	Category string
	Title    string
	Content  string
	Metadata map[string]any
}

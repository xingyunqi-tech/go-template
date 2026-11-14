package entity

// Queue names, as the nestjs BullMQ queues.
const (
	QueueMail           = "mail"
	QueueNotification   = "notification"
	QueueAccountCleanup = "account-cleanup"
	QueueScheduler      = "scheduler"
)

// MailJob is a templated mail rendered by the worker in Language.
type MailJob struct {
	Template string         `json:"template"`
	To       string         `json:"to"`
	Data     map[string]any `json:"data"`
	Language string         `json:"language"`
}

// Kind is the nestjs job name.
func (MailJob) Kind() string { return "send-mail" }

// AccountCleanupJob cleans the data of a removed account.
type AccountCleanupJob struct {
	UserID int `json:"userId"`
}

// Kind is the nestjs job name.
func (AccountCleanupJob) Kind() string { return "clean-account" }

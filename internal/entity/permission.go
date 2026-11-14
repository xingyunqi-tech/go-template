package entity

// Permissions are the nestjs PermissionEnum values.
const (
	PermUserRead       = "user:read"
	PermUserCreate     = "user:create"
	PermUserUpdate     = "user:update"
	PermUserDelete     = "user:delete"
	PermUserList       = "user:list"
	PermAddressRead    = "address:read"
	PermAddressCreate  = "address:create"
	PermAddressUpdate  = "address:update"
	PermAddressDelete  = "address:delete"
	PermFavoriteRead   = "favorite:read"
	PermFavoriteCreate = "favorite:create"
	PermFavoriteDelete = "favorite:delete"
	PermFeedbackRead   = "feedback:read"
	PermFeedbackCreate = "feedback:create"
	PermFeedbackList   = "feedback:list"
	PermFileUpload     = "file:upload"
	PermFileRead       = "file:read"
	PermFileDelete     = "file:delete"
	PermSessionRead    = "session:read"
	PermSessionDelete  = "session:delete"
	PermSystemConfig   = "system:config"
	PermSystemMetrics  = "system:metrics"
	PermPaymentRefund  = "payment:refund"
	PermAuditRead      = "audit:read"
	PermAuditList      = "audit:list"
	PermPracticeWrite  = "practice:write"
	PermAIChat         = "ai:chat"
	PermAIGrading      = "ai:grading"
	PermContentManage  = "content:manage"
	PermContentReview  = "content:review"
	PermAIGenerate     = "ai:generate"
	PermAIAdmin        = "ai:admin"
)

// AllPermissions lists every permission in enum order.
func AllPermissions() []string {
	return []string{
		PermUserRead, PermUserCreate, PermUserUpdate, PermUserDelete, PermUserList,
		PermAddressRead, PermAddressCreate, PermAddressUpdate, PermAddressDelete,
		PermFavoriteRead, PermFavoriteCreate, PermFavoriteDelete,
		PermFeedbackRead, PermFeedbackCreate, PermFeedbackList,
		PermFileUpload, PermFileRead, PermFileDelete,
		PermSessionRead, PermSessionDelete,
		PermSystemConfig, PermSystemMetrics, PermPaymentRefund,
		PermAuditRead, PermAuditList, PermPracticeWrite,
		PermAIChat, PermAIGrading, PermContentManage, PermContentReview, PermAIGenerate, PermAIAdmin,
	}
}

// DefaultRolePermissions is the nestjs default mapping used when role_permission is empty.
func DefaultRolePermissions() map[int][]string {
	return map[int][]string{
		RoleAdmin: AllPermissions(),
		RoleUser: {
			PermAddressRead, PermAddressCreate, PermAddressUpdate, PermAddressDelete,
			PermFavoriteRead, PermFavoriteCreate, PermFavoriteDelete,
			PermFeedbackRead, PermFeedbackCreate, PermFileUpload, PermFileRead, PermSessionRead,
			PermPracticeWrite, PermAIChat, PermAIGrading,
		},
		RoleEditor:   {PermFileUpload, PermFileRead, PermContentManage, PermAIGenerate},
		RoleReviewer: {PermFileUpload, PermFileRead, PermContentManage, PermContentReview},
	}
}

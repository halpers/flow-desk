package logic

// Explicit states shared by the action, approval, input and ticket workflows.
const (
	statePrepared    = "prepared"
	statePending     = "pending"
	stateApproved    = "approved"
	stateRejected    = "rejected"
	stateExpired     = "expired"
	stateInvalidated = "invalidated"
	stateCancelled   = "cancelled"
	stateExecuted    = "executed"
	stateAnswered    = "answered"
	stateOpen        = "open"
	stateInProgress  = "in_progress"
	stateResolved    = "resolved"
	stateClosed      = "closed"
)

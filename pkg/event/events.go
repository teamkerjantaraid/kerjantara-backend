package event

const (
	EventJobMatched       = "job.matched"       // matching → notification
	EventJobAccepted      = "job.accepted"      // job → notification
	EventJobRejected      = "job.rejected"      // job → notification
	EventJobDayCompleted  = "job.day.completed"  // job → payment + notification
	EventJobCompleted     = "job.completed"     // job → score + payment
	EventJobRated         = "job.rated"         // job → score
	EventPaymentReleased  = "job.payment.released" // payment → notification
	EventKTPUploaded      = "ktp.uploaded"      // auth → notification (admin)
)

type Event struct {
	Type    string
	Payload interface{}
}

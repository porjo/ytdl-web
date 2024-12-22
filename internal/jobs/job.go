package jobs

// Job represents an interface of a job that can be enqueued into a dispatcher.
type Job struct {
	Payload string
}

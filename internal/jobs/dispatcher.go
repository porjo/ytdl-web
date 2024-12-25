// credit: https://medium.com/@keigokida51/implementation-of-job-queue-model-using-goroutine-and-channel-092e08925bef

package jobs

import (
	"context"
	"sync"
)

type Worker interface {
	Work(j *Job) // Work method defines the behavior of processing a job.
}

// Dispatcher represents a job dispatcher.
type Dispatcher struct {
	workerPool chan struct{} // Semaphore for limiting concurrent worker goroutines.
	jobQueue   chan *Job     // Channel for queuing incoming jobs.
	worker     Worker        // Worker interface for processing jobs.
}

// NewDispatcher creates a new instance of a job dispatcher with the given parameters.
func NewDispatcher(worker Worker, maxWorkers int) *Dispatcher {
	return &Dispatcher{
		workerPool: make(chan struct{}, maxWorkers), // Buffered channel acting as a workerPool. Use empty struct to minimize the memory allocation
		jobQueue:   make(chan *Job),
		worker:     worker,
	}
}

// Start initiates the dispatcher to begin processing jobs.
// The dispatcher stops when it receives a value from `ctx.Done`.
func (d *Dispatcher) Start(ctx context.Context) {

	var wg sync.WaitGroup

	// Main loop for processing jobs.
	for {
		select {
		case <-ctx.Done():
			// Block until all currently processing jobs have finished.
			wg.Wait()
			// When the loop exits (due to context cancellation), stop decrements the wait group counter to indicate that the dispatcher has stopped processing jobs
			return
		case job := <-d.jobQueue:
			// Increment the local wait group to track the processing of this job.
			wg.Add(1)
			// Push to the workerPool to control the number of concurrent workers.
			d.workerPool <- struct{}{}
			// Process the job concurrently.
			go func(job *Job) {
				d.worker.Work(job)
				wg.Done()
				// After the job finishes, release the slot in the workerPool.
				<-d.workerPool
			}(job)
		}
	}
}

// Enqueue puts a job into the queue.
// If the number of enqueued jobs has already reached the maximum size,
// This will block until space becomes available in the queue to accept a new job.
func (d *Dispatcher) Enqueue(job *Job) {
	d.jobQueue <- job
}

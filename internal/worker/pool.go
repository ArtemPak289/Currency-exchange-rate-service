package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/domain"
	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/exchange"
	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/repository"
	"github.com/google/uuid"
)

// Job represents a background task to fetch and update an exchange quote.
type Job struct {
	RequestID  uuid.UUID
	Currency   string
	RetryCount int
}

// Pool manages a pool of background worker goroutines that process quote updates.
type Pool struct {
	repo        repository.QuoteRepository
	fetcher     exchange.Fetcher
	jobs        chan Job
	workerCount int
	maxRetries  int
	retryDelay  time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	stopped bool
}

// NewPool initializes a new worker pool.
func NewPool(
	repo repository.QuoteRepository,
	fetcher exchange.Fetcher,
	workerCount int,
	queueSize int,
	maxRetries int,
	retryDelay time.Duration,
) *Pool {
	if workerCount <= 0 {
		workerCount = 3
	}
	if queueSize <= 0 {
		queueSize = 1000
	}
	if maxRetries < 0 {
		maxRetries = 3
	}
	if retryDelay <= 0 {
		retryDelay = 1 * time.Second
	}

	return &Pool{
		repo:        repo,
		fetcher:     fetcher,
		jobs:        make(chan Job, queueSize),
		workerCount: workerCount,
		maxRetries:  maxRetries,
		retryDelay:  retryDelay,
	}
}

// Start launches worker goroutines and recovers any pending requests from the database.
func (p *Pool) Start(ctx context.Context) {
	p.mu.Lock()
	p.ctx, p.cancel = context.WithCancel(ctx)
	p.stopped = false
	p.mu.Unlock()

	slog.Info("Starting background worker pool", "workers", p.workerCount, "queue_size", cap(p.jobs))

	for i := 1; i <= p.workerCount; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}

	// Startup recovery: resume pending/processing requests from database
	go p.recoverPendingJobs()
}

// Enqueue adds a job to the background processing queue. Returns false if the queue is full or stopped.
func (p *Pool) Enqueue(job Job) bool {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return false
	}
	p.mu.Unlock()

	select {
	case p.jobs <- job:
		return true
	default:
		slog.Warn("Worker queue is full, dropping job", "request_id", job.RequestID, "currency", job.Currency)
		return false
	}
}

// Stop initiates graceful shutdown of the worker pool, waiting for in-flight tasks to finish.
func (p *Pool) Stop() {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.stopped = true
	p.mu.Unlock()

	slog.Info("Stopping worker pool, waiting for active jobs to complete...")
	close(p.jobs)
	p.cancel()
	p.wg.Wait()
	slog.Info("Worker pool gracefully stopped")
}

func (p *Pool) worker(id int) {
	defer p.wg.Done()
	slog.Debug("Worker started", "worker_id", id)

	for job := range p.jobs {
		p.processJob(job, id)
	}

	slog.Debug("Worker stopped", "worker_id", id)
}

func (p *Pool) processJob(job Job, workerID int) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	slog.Info("Processing quote update job",
		"worker_id", workerID,
		"request_id", job.RequestID,
		"currency", job.Currency,
		"retry", job.RetryCount,
	)

	// 1. Mark as PROCESSING in database
	_ = p.repo.UpdateRequestStatus(ctx, job.RequestID, domain.StatusProcessing, nil, nil)

	// 2. Parse currency pair
	pair, err := domain.ParseCurrencyPair(job.Currency)
	if err != nil {
		slog.Error("Invalid currency pair in job", "request_id", job.RequestID, "currency", job.Currency, "err", err)
		_ = p.repo.FailRequest(ctx, job.RequestID, fmt.Sprintf("invalid currency pair: %s", err.Error()))
		return
	}

	// 3. Fetch rate from exchange provider
	rate, err := p.fetcher.FetchRate(ctx, pair.Base, pair.Quote)
	if err != nil {
		slog.Warn("Failed to fetch exchange rate",
			"request_id", job.RequestID,
			"currency", job.Currency,
			"retry", job.RetryCount,
			"err", err,
		)

		// Retry with backoff if retries left
		if job.RetryCount < p.maxRetries {
			backoff := p.retryDelay * (1 << job.RetryCount)
			time.Sleep(backoff)
			job.RetryCount++
			if !p.Enqueue(job) {
				_ = p.repo.FailRequest(ctx, job.RequestID, fmt.Sprintf("queue full on retry: %s", err.Error()))
			}
			return
		}

		// Max retries exceeded
		slog.Error("Max retries exceeded for quote update", "request_id", job.RequestID, "currency", job.Currency)
		_ = p.repo.FailRequest(ctx, job.RequestID, fmt.Sprintf("failed after %d attempts: %s", p.maxRetries+1, err.Error()))
		return
	}

	// 4. Atomically mark as COMPLETED and upsert latest quote
	if err := p.repo.CompleteRequestAndSaveLatest(ctx, job.RequestID, pair.String(), rate); err != nil {
		slog.Error("Failed to save completed quote update", "request_id", job.RequestID, "err", err)
		_ = p.repo.FailRequest(ctx, job.RequestID, fmt.Sprintf("failed to save quote: %s", err.Error()))
		return
	}

	slog.Info("Successfully completed quote update",
		"request_id", job.RequestID,
		"currency", pair.String(),
		"price", rate,
	)
}

func (p *Pool) recoverPendingJobs() {
	// Give database a short grace period on startup
	time.Sleep(500 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pending, err := p.repo.ListPendingRequests(ctx, 100)
	if err != nil {
		slog.Warn("Failed to recover pending requests on startup", "err", err)
		return
	}

	if len(pending) > 0 {
		slog.Info("Recovering pending quote requests", "count", len(pending))
		for _, req := range pending {
			p.Enqueue(Job{
				RequestID:  req.ID,
				Currency:   req.Currency,
				RetryCount: 0,
			})
		}
	}
}

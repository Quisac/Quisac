// Package worker drains the durable job queue: it claims jobs, runs them through
// the indexer, and records the outcome. Multiple workers run concurrently and,
// together with the per-repo serialisation enforced by the queue, give at-most-
// once-per-repo processing across replicas. A maintenance loop reaps jobs left
// running by a crashed worker and evicts old terminal jobs.
package worker

import (
	"context"
	"encoding/json"
	"expvar"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"quisac/internal/core"
	"quisac/internal/db"
)

// Exported process counters, published at /debug/vars.
var (
	jobsDone   = expvar.NewInt("quisac_jobs_done")
	jobsFailed = expvar.NewInt("quisac_jobs_failed")
)

// Queue is the durable job store the pool drains. Satisfied by *db.Store.
type Queue interface {
	ClaimJob(ctx context.Context) (db.Job, bool, error)
	CompleteJob(ctx context.Context, id string, graph []byte) error
	FailJob(ctx context.Context, id, errMsg string) error
	ReapStuckJobs(ctx context.Context, olderThan time.Duration) (int64, error)
	DeleteFinishedJobs(ctx context.Context, olderThan time.Duration) (int64, error)
}

// Indexer performs the graph work for a job. Satisfied by *ingest.Indexer.
type Indexer interface {
	FullIndex(ctx context.Context, installationID, repo, headSHA string) (*core.Graph, error)
	ProcessPush(ctx context.Context, installationID, repo, before, after string, changed []string, complete bool) error
}

// PushPayload is the JSON stored in a push job's payload column.
type PushPayload struct {
	Before   string   `json:"before"`
	After    string   `json:"after"`
	Changed  []string `json:"changed"`
	Complete bool     `json:"complete"`
}

// Config tunes the pool. Zero values fall back to sensible defaults.
type Config struct {
	Workers             int           // concurrent workers
	PollInterval        time.Duration // idle poll cadence
	JobTimeout          time.Duration // per-job processing deadline
	ReapAfter           time.Duration // a job running longer than this is presumed dead
	RetentionTTL        time.Duration // terminal jobs older than this are evicted
	MaintenanceInterval time.Duration // reap + evict cadence
}

func (c Config) withDefaults() Config {
	if c.Workers <= 0 {
		c.Workers = 4
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 5 * time.Second
	}
	if c.JobTimeout <= 0 {
		c.JobTimeout = 30 * time.Minute
	}
	// ReapAfter must exceed JobTimeout so a legitimately long job is never reaped
	// while it is still running.
	if c.ReapAfter <= 0 {
		c.ReapAfter = 45 * time.Minute
	}
	if c.RetentionTTL <= 0 {
		c.RetentionTTL = 24 * time.Hour
	}
	if c.MaintenanceInterval <= 0 {
		c.MaintenanceInterval = 10 * time.Minute
	}
	return c
}

// Pool drains the queue with a fixed set of workers plus a maintenance loop.
type Pool struct {
	q    Queue
	ix   Indexer
	log  *slog.Logger
	cfg  Config
	wake chan struct{}
}

// New builds a Pool. The logger and indexer must be non-nil.
func New(q Queue, ix Indexer, log *slog.Logger, cfg Config) *Pool {
	return &Pool{
		q:    q,
		ix:   ix,
		log:  log,
		cfg:  cfg.withDefaults(),
		wake: make(chan struct{}, 1),
	}
}

// Wake nudges an idle worker to poll immediately. It is a latency optimisation
// for work enqueued in this process; cross-replica work is still found by the
// idle poll. Non-blocking: a pending wake coalesces.
func (p *Pool) Wake() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Run starts the workers and maintenance loop, blocking until ctx is cancelled
// and every goroutine has drained. In-flight jobs are allowed to finish (bounded
// by JobTimeout); no new jobs are claimed once ctx is cancelled.
func (p *Pool) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < p.cfg.Workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			p.worker(ctx, id)
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.maintain(ctx)
	}()
	p.log.Info("worker pool started", "workers", p.cfg.Workers)
	wg.Wait()
	p.log.Info("worker pool stopped")
}

func (p *Pool) worker(ctx context.Context, id int) {
	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()
	for {
		// Drain everything currently claimable before parking.
		for {
			if ctx.Err() != nil {
				return
			}
			claimed, err := p.claimAndRun(ctx)
			if err != nil {
				p.log.Error("claim failed", "worker", id, "err", err)
				break
			}
			if !claimed {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-p.wake:
		}
	}
}

func (p *Pool) claimAndRun(ctx context.Context) (bool, error) {
	job, ok, err := p.q.ClaimJob(ctx)
	if err != nil || !ok {
		return false, err
	}
	p.process(job)
	return true, nil
}

// process runs a claimed job and records its outcome. It uses a fresh,
// timeout-bounded context (not the shutdown context) so a job that has already
// started runs to completion during a graceful shutdown; a worker that dies
// mid-job leaves the row 'running' for the reaper to recover.
func (p *Pool) process(job db.Job) {
	ctx, cancel := context.WithTimeout(context.Background(), p.cfg.JobTimeout)
	defer cancel()

	log := p.log.With("job", job.ID, "kind", string(job.Kind), "repo", job.Repo, "attempt", job.Attempts)
	start := time.Now()
	log.Info("job started")

	graph, err := p.run(ctx, job)
	if err != nil {
		log.Error("job failed", "err", err, "dur", time.Since(start))
		if ferr := p.q.FailJob(context.Background(), job.ID, err.Error()); ferr != nil {
			log.Error("recording failure", "err", ferr)
		}
		jobsFailed.Add(1)
		return
	}
	if err := p.q.CompleteJob(context.Background(), job.ID, graph); err != nil {
		log.Error("recording completion", "err", err)
		return
	}
	log.Info("job done", "dur", time.Since(start))
	jobsDone.Add(1)
}

func (p *Pool) run(ctx context.Context, job db.Job) ([]byte, error) {
	switch job.Kind {
	case db.JobFullIndex:
		g, err := p.ix.FullIndex(ctx, job.InstallationID, job.Repo, "")
		if err != nil {
			return nil, err
		}
		js, err := g.ToJSON()
		if err != nil {
			return nil, fmt.Errorf("serialise graph: %w", err)
		}
		return []byte(js), nil
	case db.JobPush:
		var pl PushPayload
		if err := json.Unmarshal(job.Payload, &pl); err != nil {
			return nil, fmt.Errorf("decode push payload: %w", err)
		}
		return nil, p.ix.ProcessPush(ctx, job.InstallationID, job.Repo, pl.Before, pl.After, pl.Changed, pl.Complete)
	default:
		return nil, fmt.Errorf("unknown job kind %q", job.Kind)
	}
}

func (p *Pool) maintain(ctx context.Context) {
	// Run once promptly on startup to recover jobs a previous crash left running.
	p.runMaintenance(ctx)
	ticker := time.NewTicker(p.cfg.MaintenanceInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.runMaintenance(ctx)
		}
	}
}

func (p *Pool) runMaintenance(ctx context.Context) {
	if n, err := p.q.ReapStuckJobs(ctx, p.cfg.ReapAfter); err != nil {
		p.log.Error("reap stuck jobs", "err", err)
	} else if n > 0 {
		p.log.Warn("recovered stuck jobs", "count", n)
		p.Wake() // recovered jobs are pending again — process them now
	}
	if n, err := p.q.DeleteFinishedJobs(ctx, p.cfg.RetentionTTL); err != nil {
		p.log.Error("evict finished jobs", "err", err)
	} else if n > 0 {
		p.log.Info("evicted finished jobs", "count", n)
	}
}

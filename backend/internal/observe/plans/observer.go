// Package plans owns the cadence of the task plan scheduler. Scheduling
// decisions stay in the plan service.
package plans

import (
	"context"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/observe"
)

// DefaultTickInterval is the scheduler cadence when Config.Tick is unset.
const DefaultTickInterval = 15 * time.Second

// Scheduler advances task plans at a point in time and signals when a
// worker report or a plan change wants a pass sooner.
type Scheduler interface {
	Tick(context.Context, time.Time) error
	WakeC() <-chan struct{}
}

// Config controls the observer's cadence, clock, and logging.
type Config struct {
	Tick   time.Duration
	Clock  func() time.Time
	Logger *slog.Logger
}

// Observer ticks the plan scheduler on an interval and on every wake.
type Observer struct {
	scheduler Scheduler
	tick      time.Duration
	clock     func() time.Time
	logger    *slog.Logger
}

// New constructs a plan observer with defaults for omitted configuration.
func New(scheduler Scheduler, cfg Config) *Observer {
	o := &Observer{scheduler: scheduler, tick: cfg.Tick, clock: cfg.Clock, logger: cfg.Logger}
	if o.tick <= 0 {
		o.tick = DefaultTickInterval
	}
	if o.clock == nil {
		o.clock = time.Now
	}
	if o.logger == nil {
		o.logger = slog.Default()
	}
	return o
}

// Start runs the loop until ctx is canceled and returns its completion channel.
func (o *Observer) Start(ctx context.Context) <-chan struct{} {
	return observe.StartTriggeredPollLoop(ctx, o.tick, o.scheduler.WakeC(), o.Poll, o.logger, "plans")
}

// Poll runs one scheduler pass.
func (o *Observer) Poll(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return o.scheduler.Tick(ctx, o.clock().UTC())
}

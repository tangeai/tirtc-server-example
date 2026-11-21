// Package presence owns bounded processing of device presence observations.
package presence

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const TTL = 150 * time.Second
const QueueSize = 16384
const BatchSize = 256

// MaxPendingDevices bounds the per-device merge buffer kept while Redis is
// unavailable. It is sized above the supported fleet so an outage only costs
// memory, not observations.
const MaxPendingDevices = 262144

// maxFlushBatches keeps a large backlog from starving the receive queue.
const maxFlushBatches = 16

// Observation uses server receipt time; device-supplied clocks are not trusted.
// ExpiresAt is assigned by the Projector when the observation is accepted.
type Observation struct {
	IP          string
	ConnectedAt time.Time
	ClientID    string
	At          time.Time
	ExpiresAt   time.Time
	Offline     bool
}

// ProjectionStore applies observations idempotently: an online expiry only
// moves forward and a connection snapshot only replaces an older one, so
// retries, reordering and concurrent Device instances are safe. Its queue
// methods operate on that same durable projection.
type ProjectionStore interface {
	ConnectionQueue
	Project(context.Context, []Observation) error
}

// ConnectionStore persists newer connection metadata idempotently.
type ConnectionStore interface {
	SaveConnections(context.Context, []Observation) error
}

// BrokerClients lists the device connections the broker currently holds.
// Observations carry ClientID and, for formal connections with a reliable
// broker timestamp, IP and ConnectedAt.
type BrokerClients interface {
	ListConnected(ctx context.Context, visit func([]Observation) error) error
}

// Projector merges observations per device and writes them in batches. A
// failed write keeps the merged state and keeps receiving; nothing is
// discarded unless the merge buffer exceeds MaxPendingDevices.
type Projector struct {
	connections *connectionRecorder
	tempTTL     func() time.Duration
	store       ProjectionStore
	queue       chan Observation

	// Loss times are unix milliseconds. A loss is resolved once a broker
	// listing that started after it completes.
	lostAt         atomic.Int64 // any dropped observation
	lostIPAt       atomic.Int64 // dropped observation carrying connection metadata
	gapPersisted   atomic.Int64
	reconciledFrom atomic.Int64

	pendingDevices atomic.Int64
	lastSuccess    atomic.Int64
	rejected       atomic.Uint64
	failures       atomic.Uint64

	reconcileLast        atomic.Int64
	reconcileClients     atomic.Int64
	reconcileFailures    atomic.Uint64
	subscriptionGaps     atomic.Uint64
	projectedSamples     atomic.Int64
	projectionAgeMS      atomic.Int64
	projectionAgeBuckets [7]atomic.Int64
}

// New creates a Projector. tempTTL returns the current pairing-code lifetime. Temporary connections send
// no heartbeat, so their single connected event must stay online until the code
// expires. A nil tempTTL keeps the heartbeat TTL for every connection.
func New(store ProjectionStore, connections ConnectionStore, tempTTL func() time.Duration) *Projector {
	p := &Projector{store: store, tempTTL: tempTTL, queue: make(chan Observation, QueueSize)}
	if connections != nil {
		p.connections = &connectionRecorder{store: connections, queue: store}
	}
	return p
}

func (p *Projector) ttl(clientID string) time.Duration {
	if p.tempTTL != nil && strings.HasPrefix(clientID, "tmp_") {
		return max(TTL, p.tempTTL())
	}
	return TTL
}

// ValidClientID accepts formal (sn_) and temporary (tmp_) device connections.
func ValidClientID(id string) bool {
	return (strings.HasPrefix(id, "sn_") && len(id) > 3 && len(id) <= 67) || (strings.HasPrefix(id, "tmp_") && len(id) > 4 && len(id) <= 68)
}

// Connected builds a broker-reported connection. The IP is kept only for
// formal devices whose broker connection time is present and not in the
// future, so it can be ordered against other connection snapshots.
func Connected(clientID, ip string, connectedAt, now time.Time) (Observation, bool) {
	o := Observation{ClientID: clientID, At: now}
	if !ValidClientID(clientID) {
		return o, false
	}
	if strings.HasPrefix(clientID, "sn_") && connectedAt.UnixMilli() > 0 && !connectedAt.After(now.Add(time.Minute)) {
		if addr, err := netip.ParseAddr(ip); err == nil {
			o.IP = addr.WithZone("").Unmap().String()
		}
		o.ConnectedAt = connectedAt.UTC()
	}
	return o, true
}

// Observe never blocks the MQTT callback or creates an unbounded goroutine.
func (p *Projector) Observe(o Observation) {
	o.ExpiresAt = o.At.Add(p.ttl(o.ClientID))
	select {
	case p.queue <- o:
	default:
		p.lose(o)
	}
}

func (p *Projector) lose(o Observation) {
	if p.rejected.Add(1)%4096 == 1 {
		slog.Error("presence observation dropped", "rejected", p.rejected.Load())
	}
	now := time.Now().UnixMilli()
	p.lostAt.Store(now)
	if o.IP != "" {
		p.lostIPAt.Store(now)
	}
}

// SubscriptionGap records that the broker subscription was re-established.
// Events published while no Device instance was subscribed are gone; the
// next broker listing restores them and heartbeats refresh within TTL.
func (p *Projector) SubscriptionGap() {
	p.subscriptionGaps.Add(1)
	p.lostAt.Store(time.Now().UnixMilli())
	slog.Warn("presence subscription re-established; reconciliation requested")
}

// unresolved reports a loss that no completed broker listing has covered.
func (p *Projector) unresolved(lost int64) bool {
	return lost != 0 && lost >= p.reconciledFrom.Load()
}

// NeedsReconcile reports whether a broker listing would restore lost state.
func (p *Projector) NeedsReconcile() bool {
	return p.unresolved(p.lostAt.Load()) || (p.connections != nil && p.connections.gap.Load())
}

func (p *Projector) Ping(ctx context.Context) error {
	if p.connections != nil {
		if err := p.connections.Ping(ctx); err != nil {
			return err
		}
	}
	if p.unresolved(p.lostIPAt.Load()) {
		return errors.New("presence: connection event lost; reconciliation required")
	}
	now := time.Now()
	if lost := p.lostAt.Load(); p.unresolved(lost) && now.Sub(time.UnixMilli(lost)) < TTL {
		return errors.New("presence: observations lost; awaiting refresh")
	}
	if last := p.lastSuccess.Load(); last == 0 || now.Sub(time.Unix(0, last)) > 5*time.Second {
		return errors.New("presence: projection is not progressing")
	}
	return nil
}

func (p *Projector) Run(ctx context.Context) {
	if p.connections != nil {
		done := make(chan struct{})
		go func() { defer close(done); p.connections.Run(ctx) }()
		defer func() { <-done }()
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	w := &writer{p: p, pending: make(map[string]Observation)}
	for {
		select {
		case <-ctx.Done():
			w.drain()
			return
		case o := <-p.queue:
			w.merge(o)
			if len(w.pending) >= BatchSize {
				w.flush(ctx, time.Now())
			}
		case now := <-ticker.C:
			w.flush(ctx, now)
		}
	}
}

// writer is owned by the Run goroutine.
type writer struct {
	p          *Projector
	pending    map[string]Observation
	retryAt    time.Time
	backoff    time.Duration
	lastPingAt time.Time
}

func (w *writer) merge(o Observation) {
	if o.Offline {
		return
	}
	old, exists := w.pending[o.ClientID]
	if !exists {
		if len(w.pending) >= MaxPendingDevices {
			w.p.lose(o)
			return
		}
		w.pending[o.ClientID] = o
		w.p.pendingDevices.Store(int64(len(w.pending)))
		return
	}
	merged := old
	if o.ExpiresAt.After(old.ExpiresAt) {
		merged.At, merged.ExpiresAt = o.At, o.ExpiresAt
	}
	if o.IP != "" && (old.IP == "" || o.ConnectedAt.After(old.ConnectedAt)) {
		merged.IP, merged.ConnectedAt = o.IP, o.ConnectedAt
	}
	w.pending[o.ClientID] = merged
}

func (w *writer) flush(ctx context.Context, now time.Time) {
	if now.Before(w.retryAt) {
		return
	}
	if len(w.pending) == 0 {
		// Readiness needs evidence that Redis still accepts writes.
		if now.Sub(w.lastPingAt) < time.Second {
			return
		}
		w.lastPingAt = now
	}
	for i := 0; i < maxFlushBatches; i++ {
		batch := w.next()
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := w.p.apply(callCtx, batch)
		cancel()
		if err != nil {
			w.fail(err, now)
			return
		}
		w.backoff = 0
		w.p.lastSuccess.Store(time.Now().UnixNano())
		w.p.recordProjection(batch, time.Now())
		for _, o := range batch {
			delete(w.pending, o.ClientID)
		}
		w.p.pendingDevices.Store(int64(len(w.pending)))
		if len(w.pending) == 0 {
			return
		}
	}
}

var projectionAgeBounds = [...]int64{10, 50, 100, 500, 1000, 5000}

// These are ages of coalesced observations at successful Redis submission,
// not per-heartbeat network latency or a count of physical Redis mutations.
func (p *Projector) recordProjection(batch []Observation, now time.Time) {
	for _, o := range batch {
		age := max(int64(0), now.Sub(o.At).Milliseconds())
		p.projectedSamples.Add(1)
		p.projectionAgeMS.Add(age)
		bucket := len(projectionAgeBounds)
		for i, bound := range projectionAgeBounds {
			if age <= bound {
				bucket = i
				break
			}
		}
		p.projectionAgeBuckets[bucket].Add(1)
	}
}

func (w *writer) next() []Observation {
	batch := make([]Observation, 0, min(len(w.pending), BatchSize))
	for _, o := range w.pending {
		if len(batch) == BatchSize {
			break
		}
		batch = append(batch, o)
	}
	return batch
}

func (w *writer) fail(err error, now time.Time) {
	if w.p.failures.Add(1)%60 == 1 {
		slog.Error("presence batch failed; retrying", "err", err, "pending_devices", len(w.pending))
	}
	w.backoff = min(max(2*w.backoff, 500*time.Millisecond), 2*time.Second)
	w.retryAt = now.Add(w.backoff)
}

// drain bounds shutdown work after the source has stopped. TTL remains the
// safety net if the store cannot accept the final observations.
func (w *writer) drain() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for {
		select {
		case o := <-w.p.queue:
			w.merge(o)
			continue
		default:
		}
		break
	}
	for len(w.pending) > 0 && ctx.Err() == nil {
		batch := w.next()
		if err := w.p.apply(ctx, batch); err != nil {
			slog.Error("presence shutdown flush failed", "err", err, "pending_devices", len(w.pending))
			return
		}
		for _, o := range batch {
			delete(w.pending, o.ClientID)
		}
	}
}

func (p *Projector) apply(ctx context.Context, batch []Observation) error {
	if lost := p.lostIPAt.Load(); p.connections != nil && lost > p.gapPersisted.Load() {
		if err := p.connections.queue.MarkGap(ctx, time.UnixMilli(lost)); err != nil {
			return err
		}
		p.gapPersisted.Store(lost)
	}
	return p.store.Project(ctx, batch)
}

// Reconcile writes every connection the broker currently holds and resolves
// losses that happened before the listing started. It writes the store
// directly; monotonic projection makes this safe beside the Run writer.
func (p *Projector) Reconcile(ctx context.Context, clients BrokerClients) error {
	listedFrom := time.Now()
	var count int64
	err := clients.ListConnected(ctx, func(batch []Observation) error {
		now := time.Now()
		for i := range batch {
			batch[i].At = now
			batch[i].ExpiresAt = now.Add(p.ttl(batch[i].ClientID))
			batch[i].Offline = false
		}
		for start := 0; start < len(batch); start += BatchSize {
			if err := p.store.Project(ctx, batch[start:min(start+BatchSize, len(batch))]); err != nil {
				return err
			}
		}
		count += int64(len(batch))
		return nil
	})
	if err == nil && p.connections != nil {
		err = p.connections.queue.ClearGap(ctx, listedFrom)
		if err == nil {
			p.connections.gap.Store(false)
		}
	}
	if err != nil {
		p.reconcileFailures.Add(1)
		return err
	}
	p.reconciledFrom.Store(listedFrom.UnixMilli())
	p.reconcileClients.Store(count)
	p.reconcileLast.Store(time.Now().UnixMilli())
	return nil
}

// RunReconciler lists broker connections once at startup and again whenever
// observations are lost. clients returns nil while no broker API is
// configured; lost connection metadata then still requires manual handling.
func (p *Projector) RunReconciler(ctx context.Context, clients func() BrokerClients) {
	const interval = 5 * time.Second
	delay := time.Duration(0)
	startup := true
	consecutive := time.Duration(0)
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay = interval
		source := clients()
		if source == nil || (!startup && !p.NeedsReconcile()) {
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		err := p.Reconcile(callCtx, source)
		cancel()
		if err != nil {
			slog.Warn("presence broker reconciliation failed", "err", err)
			consecutive++
			delay = min(consecutive*interval, time.Minute)
			continue
		}
		startup, consecutive = false, 0
		slog.Info("presence broker reconciliation completed", "clients", p.reconcileClients.Load())
	}
}

// Stats supports operational diagnosis without logging individual heartbeats.
func (p *Projector) Stats() (queued int, rejected, failures uint64) {
	return len(p.queue), p.rejected.Load(), p.failures.Load()
}

// Metrics is a non-blocking snapshot for the existing service status reporter.
func (p *Projector) Metrics() map[string]int64 {
	queued, rejected, failures := p.Stats()
	result := map[string]int64{
		"presence_queued":                   int64(queued),
		"presence_pending_devices":          p.pendingDevices.Load(),
		"presence_rejected_total":           int64(rejected),
		"presence_write_failures_total":     int64(failures),
		"presence_reconcile_last_ms":        p.reconcileLast.Load(),
		"presence_reconcile_clients":        p.reconcileClients.Load(),
		"presence_reconcile_failures_total": int64(p.reconcileFailures.Load()),
		"presence_subscription_gaps_total":  int64(p.subscriptionGaps.Load()),
		"presence_projected_samples_total":  p.projectedSamples.Load(),
		"presence_projection_age_ms_total":  p.projectionAgeMS.Load(),
	}
	var cumulative int64
	for i, bound := range projectionAgeBounds {
		cumulative += p.projectionAgeBuckets[i].Load()
		result["presence_projection_age_le_"+strconv.FormatInt(bound, 10)+"ms_total"] = cumulative
	}
	result["presence_projection_age_le_inf_total"] = cumulative + p.projectionAgeBuckets[len(projectionAgeBounds)].Load()
	if r := p.connections; r != nil {
		result["connection_pending"] = r.pending.Load()
		result["connection_backlog_measured_at_ms"] = r.measuredMS.Load()
		age := int64(0)
		if oldest := r.oldestMS.Load(); oldest > 0 {
			age = max(0, time.Now().UnixMilli()-oldest)
		}
		result["connection_oldest_pending_age_ms"] = age
	}
	return result
}

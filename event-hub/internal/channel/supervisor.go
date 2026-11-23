package channel

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/unrealm-ai/kikoteam/event-hub/internal/election"
)

type Runner func(context.Context, Binding, *Backend) error
type running struct {
	binding Binding
	cancel  context.CancelFunc
	done    chan struct{}
}
type Supervisor struct {
	Backend  *Backend
	Addr     string
	Interval time.Duration
	Logger   *slog.Logger
	Connect  Runner
	mu       sync.RWMutex
	snapshot Snapshot
	active   map[string]*running
	refresh  chan struct{}
}

func NewSupervisor(b *Backend, addr string, interval time.Duration, logger *slog.Logger) *Supervisor {
	b.Logger = logger
	s := &Supervisor{Backend: b, Addr: addr, Interval: interval, Logger: logger, Connect: RunProvider, active: map[string]*running{}, refresh: make(chan struct{}, 1)}
	b.OnObserved = func() {
		select {
		case s.refresh <- struct{}{}:
		default:
		}
	}
	return s
}
func (s *Supervisor) Current(id string) (Binding, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.active[id]
	if !ok {
		return Binding{}, false
	}
	b := r.binding
	if b.Provider == "wecom" && b.TenantID == "" && b.BotOpenID == "" {
		if session := currentWecomSession(b); session != nil && session.identityConfirmed.Load() {
			b.TenantID, b.BotOpenID = b.Credentials.AppID, b.Credentials.AppID
		}
	}
	return b, true
}
func validateSnapshot(v Snapshot) error {
	if v.Revision < 0 || v.Items == nil || len(v.Items) > 10000 {
		return errors.New("invalid channel snapshot")
	}
	seen := map[string]bool{}
	for _, b := range v.Items {
		if b.ID == "" || seen[b.ID] || !supportedProvider(b.Provider) || b.Generation < 1 || b.Revision < 1 || b.RulesVersion < 1 {
			return errors.New("invalid channel binding")
		}
		seen[b.ID] = true
		if _, e := Compile(b.Trigger); e != nil {
			return e
		}
		if b.Enabled && (b.Credentials.AppID == "" || b.Credentials.AppSecret == "") {
			return errors.New("missing channel credentials")
		}
	}
	return nil
}
func (s *Supervisor) fetch(ctx context.Context) error {
	v, e := s.Backend.Snapshot(ctx)
	if e != nil {
		return e
	}
	if e = validateSnapshot(v); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if v.Revision < s.snapshot.Revision {
		return errors.New("stale channel snapshot")
	}
	s.snapshot = v
	return nil
}
func (s *Supervisor) Recover(ctx context.Context, _ *election.Session) error { return s.fetch(ctx) }
func (s *Supervisor) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path != "/api/internal/v1/channel-bindings/refresh" {
			http.NotFound(w, r)
			return
		}
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		if r.Header.Get("Origin") != "" || r.Header.Get("Cookie") != "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.Backend.Token)) != 1 {
			w.WriteHeader(401)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var v struct {
			Revision int64 `json:"revision"`
		}
		if json.NewDecoder(r.Body).Decode(&v) != nil || v.Revision < 0 {
			w.WriteHeader(400)
			return
		}
		select {
		case s.refresh <- struct{}{}:
		default:
		}
		w.WriteHeader(202)
	})
}
func (s *Supervisor) reconcile(ctx context.Context) {
	s.mu.Lock()
	items := append([]Binding(nil), s.snapshot.Items...)
	s.mu.Unlock()
	desired := map[string]Binding{}
	for _, b := range items {
		if b.Enabled {
			desired[b.ID] = b
		}
	}
	s.mu.Lock()
	var stopped []*running
	for id, r := range s.active {
		b, ok := desired[id]
		current := r.binding
		// The first durable WeCom callback fills these two fields without changing
		// configuration. Keep its original socket and pending reply correlations.
		if current.Provider == "wecom" && current.TenantID == "" && current.BotOpenID == "" &&
			b.TenantID == current.Credentials.AppID && b.BotOpenID == b.TenantID {
			current.TenantID, current.BotOpenID = b.TenantID, b.BotOpenID
		}
		if !ok || !reflect.DeepEqual(b, current) {
			r.cancel()
			stopped = append(stopped, r)
			delete(s.active, id)
		} else {
			r.binding = b
		}
	}
	s.mu.Unlock()
	for _, r := range stopped {
		<-r.done
	}
	for _, b := range items {
		if ctx.Err() != nil {
			return
		}
		if !b.Enabled {
			if e := s.Backend.Applied(ctx, b.ID, status(b, "applied", "")); e != nil {
				s.warn("channel.status_failed")
			}
			continue
		}
		s.mu.Lock()
		_, exists := s.active[b.ID]
		if !exists {
			runCtx, cancel := context.WithCancel(ctx)
			r := &running{binding: b, cancel: cancel, done: make(chan struct{})}
			s.active[b.ID] = r
			go s.runBinding(runCtx, r)
		}
		s.mu.Unlock()
	}
}
func (s *Supervisor) warn(event string) {
	if s.Logger != nil {
		s.Logger.Warn("channel operation failed", "event", event)
	}
}
func (s *Supervisor) runBinding(ctx context.Context, r *running) {
	defer close(r.done)
	for ctx.Err() == nil {
		s.mu.RLock()
		b := r.binding
		s.mu.RUnlock()
		err := s.Connect(ctx, b, s.Backend)
		if ctx.Err() != nil {
			return
		}
		code := "connection_closed"
		switch {
		case errors.Is(err, errWecomConnect), errors.Is(err, errWecomAuth), errors.Is(err, errWecomStatusUpdate):
			code = err.Error()
		case err != nil:
			code = "connection_failed"
		}
		if e := s.Backend.Applied(ctx, b.ID, status(b, "failed", code)); e != nil {
			s.warn("channel.status_failed")
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
func (s *Supervisor) stop() {
	s.mu.Lock()
	var all []*running
	for _, r := range s.active {
		r.cancel()
		all = append(all, r)
	}
	s.active = map[string]*running{}
	s.mu.Unlock()
	for _, r := range all {
		<-r.done
	}
}
func (s *Supervisor) Run(ctx context.Context, _ *election.Session) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", s.Addr)
	if err != nil {
		return errors.New("channel control listener failed")
	}
	server := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() {
		_ = server.Close() //nolint:errcheck // Close all notification sockets on elected-term exit.
		s.stop()
		if s.Backend.dingReplies != nil {
			s.Backend.dingReplies.reset()
		}
	}()
	s.reconcile(ctx)
	reactionDone := make(chan struct{})
	go func() {
		defer close(reactionDone)
		s.runReceiptReactions(ctx)
	}()
	defer func() { cancel(); <-reactionDone }()
	mediaDone := make(chan struct{})
	go func() {
		defer close(mediaDone)
		s.runInboundMedia(ctx)
	}()
	defer func() { cancel(); <-mediaDone }()
	deliveryDone := make(chan struct{})
	var deliveryErr error
	go func() { defer close(deliveryDone); deliveryErr = RunDeliveries(ctx, s.Backend, s.Current) }()
	defer func() { cancel(); <-deliveryDone }()
	historyDone := make(chan struct{})
	var historyErr error
	go func() { defer close(historyDone); historyErr = RunHistory(ctx, s.Backend, s.Current) }()
	defer func() { cancel(); <-historyDone }()
	groupDone := make(chan struct{})
	var groupErr error
	go func() { defer close(groupDone); groupErr = RunGroupChecks(ctx, s.Backend, s.Current) }()
	defer func() { cancel(); <-groupDone }()
	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-deliveryDone:
			return deliveryErr
		case <-historyDone:
			return historyErr
		case <-groupDone:
			return groupErr
		case err := <-done:
			if !errors.Is(err, http.ErrServerClosed) {
				return errors.New("channel control server failed")
			}
			return nil
		case <-ticker.C:
		case <-s.refresh:
		}
		if ctx.Err() != nil {
			return nil
		}
		if err = s.fetch(ctx); err != nil {
			s.warn("channel.config_refresh_failed")
			continue
		}
		s.reconcile(ctx)
	}
}

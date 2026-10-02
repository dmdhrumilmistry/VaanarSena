// Package mdm is the platform-neutral core: it queues commands, resolves
// effective policy and hands work to the platform drivers.
package mdm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/dmdhrumilmistry/VaanarSena/internal/command"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

// Driver is implemented by each platform integration.
type Driver interface {
	// Platforms lists the device platforms the driver handles.
	Platforms() []string
	// Wake prompts delivery of queued commands. Push-based drivers (Apple,
	// Windows) send a push; API-based drivers (Android, ChromeOS) execute the
	// queue directly. Pull-only drivers (Linux agent) return nil.
	Wake(ctx context.Context, d *store.Device) error
}

// Service coordinates drivers.
type Service struct {
	Store   *store.Store
	Log     *slog.Logger
	mu      sync.RWMutex // guards drivers; platforms are reconfigured at runtime
	drivers map[string]Driver
}

// New returns a Service.
func New(st *store.Store, log *slog.Logger) *Service {
	return &Service{Store: st, Log: log, drivers: map[string]Driver{}}
}

// Register adds a driver, replacing any driver for the same platforms.
func (s *Service) Register(d Driver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range d.Platforms() {
		s.drivers[p] = d
	}
}

// Unregister removes the drivers for platforms.
func (s *Service) Unregister(platforms ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range platforms {
		delete(s.drivers, p)
	}
}

// Enabled reports whether a driver is registered for platform.
func (s *Service) Enabled(platform string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.drivers[platform]
	return ok
}

// ErrForbidden wraps authorisation failures so the API can map them to 403.
var ErrForbidden = errors.New("forbidden")

// Enqueue authorises, queues and wakes. actorID may be nil for system commands.
func (s *Service) Enqueue(ctx context.Context, role string, actorID *string, d *store.Device, typ string, params json.RawMessage) (*store.Command, error) {
	p, err := command.ParseParams(params)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid params: %v", ErrForbidden, err)
	}
	if err := command.Authorize(role, d, typ, p); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrForbidden, err)
	}
	cmd, err := s.Store.EnqueueCommand(ctx, d.ID, typ, params, actorID)
	if err != nil {
		return nil, err
	}
	s.Wake(ctx, d)
	return cmd, nil
}

// EnqueueSystem queues a command on behalf of the server (enrollment
// bootstrap, policy changes). It still applies the BYOD guard.
func (s *Service) EnqueueSystem(ctx context.Context, d *store.Device, typ string) error {
	if err := command.Authorize(store.RoleAdmin, d, typ, command.Params{}); err != nil {
		return err
	}
	_, err := s.Store.EnqueueCommand(ctx, d.ID, typ, nil, nil)
	return err
}

// EnqueueSystemParams is EnqueueSystem with parameters (blueprint steps).
func (s *Service) EnqueueSystemParams(ctx context.Context, d *store.Device, typ string, params json.RawMessage) error {
	p, err := command.ParseParams(params)
	if err != nil {
		return err
	}
	if err := command.Authorize(store.RoleAdmin, d, typ, p); err != nil {
		return err
	}
	_, err = s.Store.EnqueueCommand(ctx, d.ID, typ, params, nil)
	return err
}

// Wake asks the device's driver to deliver queued work. Errors are logged, not
// returned: the command stays queued and is delivered at the next check-in.
func (s *Service) Wake(ctx context.Context, d *store.Device) {
	s.mu.RLock()
	drv, ok := s.drivers[d.Platform]
	s.mu.RUnlock()
	if !ok {
		return
	}
	if err := drv.Wake(ctx, d); err != nil {
		s.Log.Warn("wake device", "device", d.ID, "platform", d.Platform, "err", err)
	}
}

// PolicyChanged queues apply_policy for every device the policy targets.
func (s *Service) PolicyChanged(ctx context.Context, deviceIDs []string) {
	for _, id := range deviceIDs {
		d, err := s.Store.DeviceByID(ctx, id)
		if err != nil {
			continue
		}
		if err := s.EnqueueSystem(ctx, d, command.ApplyPolicy); err != nil {
			s.Log.Debug("skip policy push", "device", id, "err", err)
			continue
		}
		s.Wake(ctx, d)
	}
}

// AttachToGroup adds a newly enrolled device to its enrollment token's group.
func AttachToGroup(ctx context.Context, st *store.Store, t *store.EnrollmentToken, deviceID string) {
	if t != nil && t.GroupID != nil {
		_ = st.AddDeviceToGroup(ctx, *t.GroupID, deviceID)
	}
}

// Bootstrap runs after enrollment: it places the device in its smart groups,
// then queues the first inventory, the policy push and blueprint onboarding.
func (s *Service) Bootstrap(ctx context.Context, d *store.Device) {
	if _, err := s.ReconcileDevice(ctx, d); err != nil {
		s.Log.Warn("smart groups at enrollment", "device", d.ID, "err", err)
	}
	for _, typ := range []string{command.Refresh, command.ApplyPolicy} {
		if err := s.EnqueueSystem(ctx, d, typ); err != nil {
			s.Log.Debug("bootstrap", "device", d.ID, "cmd", typ, "err", err)
		}
	}
	if d.Status == store.StatusEnrolled {
		s.RunBlueprints(ctx, d)
	}
	s.Wake(ctx, d)
}

package mdm

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/dmdhrumilmistry/VaanarSena/internal/blueprint"
	"github.com/dmdhrumilmistry/VaanarSena/internal/groups"
	"github.com/dmdhrumilmistry/VaanarSena/internal/policy"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

// layer is one source of configuration with its priority.
type layer struct {
	priority int
	name     string
	doc      *policy.Document
}

// EffectivePolicy merges everything that applies to a device: policies
// assigned to it or its groups, policies referenced by blueprints targeting
// its groups, and those blueprints' inline policies. Lower priority numbers
// are more important and are merged last, so they win.
func EffectivePolicy(ctx context.Context, st *store.Store, deviceID string) (*policy.Document, error) {
	var layers []layer
	seen := map[string]bool{}
	addPolicy := func(p *store.Policy) error {
		if seen[p.ID] {
			return nil
		}
		seen[p.ID] = true
		doc, err := policy.Parse(p.Document)
		if err != nil {
			return fmt.Errorf("policy %s: %w", p.Name, err)
		}
		layers = append(layers, layer{p.Priority, "policy/" + p.Name, doc})
		return nil
	}

	direct, err := st.EffectivePolicies(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	for _, p := range direct {
		if err := addPolicy(p); err != nil {
			return nil, err
		}
	}
	bps, err := st.DeviceBlueprints(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	for _, b := range bps {
		spec, err := blueprint.Parse(b.Spec)
		if err != nil {
			return nil, fmt.Errorf("blueprint %s: %w", b.Name, err)
		}
		for _, name := range spec.Policies {
			p, err := st.PolicyByName(ctx, name)
			if errors.Is(err, store.ErrNotFound) {
				continue // referenced policy deleted; ignore rather than block the device
			}
			if err != nil {
				return nil, err
			}
			if err := addPolicy(p); err != nil {
				return nil, err
			}
		}
		if spec.Policy != nil {
			layers = append(layers, layer{b.Priority, "blueprint/" + b.Name, spec.Policy})
		}
	}
	// Most important (lowest number) last; ties broken by name for stability.
	sort.SliceStable(layers, func(i, j int) bool {
		if layers[i].priority != layers[j].priority {
			return layers[i].priority > layers[j].priority
		}
		return layers[i].name > layers[j].name
	})
	docs := make([]*policy.Document, len(layers))
	for i, l := range layers {
		docs[i] = l.doc
	}
	return policy.Merge(docs...), nil
}

// smartGroup is a parsed smart group.
type smartGroup struct {
	g    *store.Group
	rule *groups.Rule
}

func (s *Service) smartGroups(ctx context.Context) ([]smartGroup, error) {
	gs, err := s.Store.ListGroups(ctx, store.GroupSmart)
	if err != nil {
		return nil, err
	}
	out := make([]smartGroup, 0, len(gs))
	for _, g := range gs {
		r, err := groups.Parse(g.Rules)
		if err != nil {
			s.Log.Warn("skipping smart group with invalid rules", "group", g.Name, "err", err)
			continue
		}
		out = append(out, smartGroup{g, r})
	}
	return out, nil
}

// ReconcileGroups recomputes every smart group and applies the consequences
// (policy pushes, blueprint onboarding) to devices whose membership changed.
func (s *Service) ReconcileGroups(ctx context.Context) error {
	sgs, err := s.smartGroups(ctx)
	if err != nil || len(sgs) == 0 {
		return err
	}
	devices, err := s.Store.AllDevices(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	changed := map[string]bool{}
	for _, sg := range sgs {
		var members []string
		for _, d := range devices {
			if d.Status != store.StatusRetired && sg.rule.Matches(d, now) {
				members = append(members, d.ID)
			}
		}
		added, removed, err := s.Store.SetSmartMembers(ctx, sg.g.ID, members)
		if err != nil {
			return fmt.Errorf("group %s: %w", sg.g.Name, err)
		}
		for _, id := range append(added, removed...) {
			changed[id] = true
		}
		if len(added)+len(removed) > 0 {
			s.Log.Info("smart group membership changed", "group", sg.g.Name, "added", len(added), "removed", len(removed))
		}
	}
	ids := make([]string, 0, len(changed))
	for id := range changed {
		ids = append(ids, id)
	}
	s.MembershipChanged(ctx, ids)
	return nil
}

// ReconcileDevice evaluates one device against every smart group. Called at
// enrollment so the first policy push already reflects smart groups.
func (s *Service) ReconcileDevice(ctx context.Context, d *store.Device) (bool, error) {
	sgs, err := s.smartGroups(ctx)
	if err != nil {
		return false, err
	}
	now := time.Now()
	changed := false
	for _, sg := range sgs {
		member := d.Status != store.StatusRetired && d.Status != store.StatusWiped && sg.rule.Matches(d, now)
		ch, err := s.Store.SetSmartMembership(ctx, sg.g.ID, d.ID, member)
		if err != nil {
			return changed, err
		}
		changed = changed || ch
	}
	return changed, nil
}

// MembershipChanged re-pushes policy and runs pending blueprint onboarding for
// devices whose groups changed.
func (s *Service) MembershipChanged(ctx context.Context, deviceIDs []string) {
	s.PolicyChanged(ctx, deviceIDs)
	for _, id := range deviceIDs {
		d, err := s.Store.DeviceByID(ctx, id)
		if err != nil || d.Status != store.StatusEnrolled {
			continue
		}
		s.RunBlueprints(ctx, d)
	}
}

// RunBlueprints queues the onboarding steps of every blueprint targeting the
// device that has not run on it yet. Steps still pass the BYOD guard: a step
// that is not allowed on a personal device is skipped and audited.
func (s *Service) RunBlueprints(ctx context.Context, d *store.Device) {
	bps, err := s.Store.DeviceBlueprints(ctx, d.ID)
	if err != nil {
		s.Log.Warn("device blueprints", "device", d.ID, "err", err)
		return
	}
	woke := false
	for _, b := range bps {
		spec, err := blueprint.Parse(b.Spec)
		if err != nil || len(spec.OnEnroll) == 0 {
			continue
		}
		claimed, err := s.Store.ClaimBlueprintRun(ctx, b.ID, d.ID, b.Version)
		if err != nil || !claimed {
			continue
		}
		for _, step := range spec.OnEnroll {
			if err := s.EnqueueSystemParams(ctx, d, step.Type, step.Params); err != nil {
				_ = s.Store.Audit(ctx, "blueprint:"+b.Name, "command.skipped", d.ID,
					map[string]any{"type": step.Type, "reason": err.Error()}, "")
				continue
			}
			_ = s.Store.Audit(ctx, "blueprint:"+b.Name, "command."+step.Type, d.ID, map[string]any{"onEnroll": true}, "")
			woke = true
		}
	}
	if woke {
		s.Wake(ctx, d)
	}
}

// RunGroupReconciler reconciles smart groups every interval until ctx ends.
func (s *Service) RunGroupReconciler(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := s.ReconcileGroups(ctx); err != nil && ctx.Err() == nil {
			s.Log.Warn("reconcile smart groups", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

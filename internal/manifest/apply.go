package manifest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/dmdhrumilmistry/VaanarSena/internal/mdm"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

// Options controls an apply.
type Options struct {
	// DryRun validates and reports what would change without writing.
	DryRun bool
	// Prune deletes resources owned by Owner that are not in the set.
	Prune bool
	// Owner tags every applied resource (managed_by) and scopes Prune, so
	// independent manifest sources never delete each other's resources and
	// console-made resources are never pruned. Defaults to "manifest".
	Owner string
}

// Change reports what happened to one resource.
type Change struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Action string `json:"action"` // created, updated, unchanged, deleted
}

// Result is the outcome of an apply.
type Result struct {
	DryRun  bool     `json:"dryRun"`
	Changes []Change `json:"changes"`
}

// Applier writes resources through the store and notifies the MDM core so
// devices pick up the result.
type Applier struct {
	Store *store.Store
	Svc   *mdm.Service
}

// ValidationError lists every problem found before any write happened.
type ValidationError struct{ Problems []string }

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%d problem(s): %s", len(e.Problems), joinLines(e.Problems))
}

func joinLines(s []string) string {
	var b bytes.Buffer
	for i, p := range s {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(p)
	}
	return b.String()
}

// Apply validates the whole set first and writes nothing if anything is
// wrong. Resources are then applied in dependency order.
func (a *Applier) Apply(ctx context.Context, rs []Resource, opt Options) (*Result, error) {
	if opt.Owner == "" {
		opt.Owner = "manifest"
	}
	if err := a.validateSet(ctx, rs); err != nil {
		return nil, err
	}
	sorted := append([]Resource(nil), rs...)
	sort.SliceStable(sorted, func(i, j int) bool { return kindOrder[sorted[i].Kind] < kindOrder[sorted[j].Kind] })

	res := &Result{DryRun: opt.DryRun}
	affected := map[string]bool{} // devices needing a policy push / onboarding
	smartChanged := false
	for i := range sorted {
		r := &sorted[i]
		var action string
		var err error
		switch r.Kind {
		case KindGroup:
			action, err = a.applyGroup(ctx, r, opt, affected, &smartChanged)
		case KindPolicy:
			action, err = a.applyPolicy(ctx, r, opt, affected)
		case KindBlueprint:
			action, err = a.applyBlueprint(ctx, r, opt, affected)
		}
		if err != nil {
			return res, fmt.Errorf("%s %q: %w", r.Kind, r.Metadata.Name, err)
		}
		res.Changes = append(res.Changes, Change{r.Kind, r.Metadata.Name, action})
	}
	if opt.Prune {
		pruned, err := a.prune(ctx, rs, opt, affected)
		res.Changes = append(res.Changes, pruned...)
		if err != nil {
			return res, err
		}
	}
	if !opt.DryRun {
		if smartChanged {
			if err := a.Svc.ReconcileGroups(ctx); err != nil {
				return res, fmt.Errorf("reconcile smart groups: %w", err)
			}
		}
		ids := make([]string, 0, len(affected))
		for id := range affected {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		a.Svc.MembershipChanged(ctx, ids)
	}
	return res, nil
}

// validateSet checks every resource and every cross reference.
func (a *Applier) validateSet(ctx context.Context, rs []Resource) error {
	var problems []string
	seen := map[string]bool{}
	groupsInSet := map[string]bool{}
	policiesInSet := map[string]bool{}
	for _, r := range rs {
		if err := r.Validate(); err != nil {
			problems = append(problems, fmt.Sprintf("%s %q: %v", r.Kind, r.Metadata.Name, err))
			continue
		}
		if seen[r.key()] {
			problems = append(problems, fmt.Sprintf("%s %q is defined twice", r.Kind, r.Metadata.Name))
		}
		seen[r.key()] = true
		switch r.Kind {
		case KindGroup:
			groupsInSet[r.Metadata.Name] = true
		case KindPolicy:
			policiesInSet[r.Metadata.Name] = true
		}
	}
	groupExists := func(name string) bool {
		if groupsInSet[name] {
			return true
		}
		_, err := a.Store.GroupByName(ctx, name)
		return err == nil
	}
	for _, r := range rs {
		var targets []string
		switch r.Kind {
		case KindPolicy:
			if s, err := r.PolicySpec(); err == nil {
				targets = s.Groups
			}
		case KindBlueprint:
			s, err := r.BlueprintSpec()
			if err != nil {
				continue
			}
			targets = s.Groups
			for _, p := range s.Policies {
				if policiesInSet[p] {
					continue
				}
				if _, err := a.Store.PolicyByName(ctx, p); err != nil {
					problems = append(problems, fmt.Sprintf("Blueprint %q references unknown policy %q", r.Metadata.Name, p))
				}
			}
		}
		for _, g := range targets {
			if !groupExists(g) {
				problems = append(problems, fmt.Sprintf("%s %q targets unknown group %q", r.Kind, r.Metadata.Name, g))
			}
		}
	}
	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}

func (a *Applier) groupIDs(ctx context.Context, names []string) ([]string, error) {
	ids := make([]string, 0, len(names))
	for _, n := range names {
		g, err := a.Store.GroupByName(ctx, n)
		if err != nil {
			return nil, fmt.Errorf("group %q: %w", n, err)
		}
		ids = append(ids, g.ID)
	}
	sort.Strings(ids)
	return ids, nil
}

func canon(v any) string {
	b, _ := json.Marshal(v)
	var x any
	_ = json.Unmarshal(b, &x)
	b, _ = json.Marshal(x) // map keys sorted
	return string(b)
}

func (a *Applier) applyGroup(ctx context.Context, r *Resource, opt Options, affected map[string]bool, smartChanged *bool) (string, error) {
	spec, _ := r.GroupSpec()
	var rules json.RawMessage
	if spec.Rules != nil {
		rules, _ = json.Marshal(spec.Rules)
	}
	existing, err := a.Store.GroupByName(ctx, r.Metadata.Name)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	want := &store.Group{Name: r.Metadata.Name, Description: r.Metadata.Description, Kind: spec.Kind, Rules: rules, ManagedBy: opt.Owner}

	var memberIDs []string
	if spec.Members != nil {
		memberIDs = append(memberIDs, spec.Members.IDs...)
		if len(spec.Members.Serials) > 0 {
			ids, err := a.Store.DeviceIDsBySerial(ctx, spec.Members.Serials)
			if err != nil {
				return "", err
			}
			memberIDs = append(memberIDs, ids...)
		}
	}

	action := "created"
	if existing != nil && err == nil {
		want.ID = existing.ID
		same := existing.Description == want.Description && existing.Kind == want.Kind &&
			canon(existing.Rules) == canon(want.Rules) && existing.ManagedBy == want.ManagedBy
		action = "updated"
		if same {
			action = "unchanged"
		}
		if spec.Members != nil && action == "unchanged" {
			cur, err := a.Store.GroupMembers(ctx, existing.ID)
			if err != nil {
				return "", err
			}
			if canon(sortedCopy(cur)) != canon(sortedCopy(memberIDs)) {
				action = "updated"
			}
		}
	}
	if opt.DryRun || action == "unchanged" {
		return action, nil
	}
	g, err := a.Store.SaveGroup(ctx, want)
	if err != nil {
		return "", err
	}
	if g.Kind == store.GroupSmart {
		*smartChanged = true
	}
	if spec.Members != nil {
		added, removed, err := a.Store.SetStaticMembers(ctx, g.ID, memberIDs)
		if err != nil {
			return "", err
		}
		for _, id := range append(added, removed...) {
			affected[id] = true
		}
	}
	return action, nil
}

func sortedCopy(s []string) []string {
	out := append([]string{}, s...)
	sort.Strings(out)
	return out
}

func (a *Applier) applyPolicy(ctx context.Context, r *Resource, opt Options, affected map[string]bool) (string, error) {
	spec, _ := r.PolicySpec()
	gids, err := a.groupIDsOrPending(ctx, spec.Groups, opt)
	if err != nil {
		return "", err
	}
	doc, _ := json.Marshal(spec.Document)
	existing, err := a.Store.PolicyByName(ctx, r.Metadata.Name)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	want := &store.Policy{Name: r.Metadata.Name, Description: r.Metadata.Description, Priority: spec.Priority,
		Document: doc, GroupIDs: gids, ManagedBy: opt.Owner}
	action := "created"
	var before []string
	if existing != nil && err == nil {
		want.ID, want.DeviceIDs = existing.ID, existing.DeviceIDs
		same := existing.Description == want.Description && existing.Priority == want.Priority &&
			canon(existing.Document) == canon(want.Document) && canon(sortedCopy(existing.GroupIDs)) == canon(gids) &&
			existing.ManagedBy == want.ManagedBy
		action = map[bool]string{true: "unchanged", false: "updated"}[same]
		if !opt.DryRun && !same {
			before, _ = a.Store.DevicesForPolicy(ctx, existing.ID)
		}
	}
	if opt.DryRun || action == "unchanged" {
		return action, nil
	}
	p, err := a.Store.SavePolicy(ctx, want)
	if err != nil {
		return "", err
	}
	after, _ := a.Store.DevicesForPolicy(ctx, p.ID)
	for _, id := range append(before, after...) {
		affected[id] = true
	}
	return action, nil
}

// groupIDsOrPending resolves group names; in a dry run, groups that would be
// created by the same apply do not exist yet, so they resolve to nothing.
func (a *Applier) groupIDsOrPending(ctx context.Context, names []string, opt Options) ([]string, error) {
	if !opt.DryRun {
		return a.groupIDs(ctx, names)
	}
	var ids []string
	for _, n := range names {
		if g, err := a.Store.GroupByName(ctx, n); err == nil {
			ids = append(ids, g.ID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (a *Applier) applyBlueprint(ctx context.Context, r *Resource, opt Options, affected map[string]bool) (string, error) {
	spec, _ := r.BlueprintSpec()
	gids, err := a.groupIDsOrPending(ctx, spec.Groups, opt)
	if err != nil {
		return "", err
	}
	body, _ := json.Marshal(spec.Spec)
	existing, err := a.Store.BlueprintByName(ctx, r.Metadata.Name)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	want := &store.Blueprint{Name: r.Metadata.Name, Description: r.Metadata.Description, Priority: spec.Priority,
		Spec: body, GroupIDs: gids, ManagedBy: opt.Owner}
	action := "created"
	var before []string
	if existing != nil && err == nil {
		want.ID = existing.ID
		same := existing.Description == want.Description && existing.Priority == want.Priority &&
			canon(existing.Spec) == canon(want.Spec) && canon(sortedCopy(existing.GroupIDs)) == canon(gids) &&
			existing.ManagedBy == want.ManagedBy
		action = map[bool]string{true: "unchanged", false: "updated"}[same]
		if !opt.DryRun && !same {
			before, _ = a.Store.DevicesForBlueprint(ctx, existing.ID)
		}
	}
	if opt.DryRun || action == "unchanged" {
		return action, nil
	}
	b, err := a.Store.SaveBlueprint(ctx, want)
	if err != nil {
		return "", err
	}
	after, _ := a.Store.DevicesForBlueprint(ctx, b.ID)
	for _, id := range append(before, after...) {
		affected[id] = true
	}
	return action, nil
}

// prune deletes owned resources absent from the set, dependents first.
func (a *Applier) prune(ctx context.Context, rs []Resource, opt Options, affected map[string]bool) ([]Change, error) {
	keep := map[string]bool{}
	for _, r := range rs {
		keep[r.key()] = true
	}
	var out []Change
	bps, err := a.Store.ListBlueprints(ctx)
	if err != nil {
		return out, err
	}
	for _, b := range bps {
		r := Resource{Kind: KindBlueprint, Metadata: Metadata{Name: b.Name}}
		if b.ManagedBy != opt.Owner || keep[r.key()] {
			continue
		}
		if !opt.DryRun {
			ids, _ := a.Store.DevicesForBlueprint(ctx, b.ID)
			if err := a.Store.DeleteBlueprint(ctx, b.ID); err != nil {
				return out, err
			}
			for _, id := range ids {
				affected[id] = true
			}
		}
		out = append(out, Change{KindBlueprint, b.Name, "deleted"})
	}
	pols, err := a.Store.ListPolicies(ctx)
	if err != nil {
		return out, err
	}
	for _, p := range pols {
		r := Resource{Kind: KindPolicy, Metadata: Metadata{Name: p.Name}}
		if p.ManagedBy != opt.Owner || keep[r.key()] {
			continue
		}
		if !opt.DryRun {
			ids, _ := a.Store.DevicesForPolicy(ctx, p.ID)
			if err := a.Store.DeletePolicy(ctx, p.ID); err != nil {
				return out, err
			}
			for _, id := range ids {
				affected[id] = true
			}
		}
		out = append(out, Change{KindPolicy, p.Name, "deleted"})
	}
	gs, err := a.Store.ListGroups(ctx)
	if err != nil {
		return out, err
	}
	for _, g := range gs {
		r := Resource{Kind: KindGroup, Metadata: Metadata{Name: g.Name}}
		if g.ManagedBy != opt.Owner || keep[r.key()] {
			continue
		}
		if !opt.DryRun {
			ids, err := a.Store.DeleteGroup(ctx, g.ID)
			if err != nil {
				return out, err
			}
			for _, id := range ids {
				affected[id] = true
			}
		}
		out = append(out, Change{KindGroup, g.Name, "deleted"})
	}
	return out, nil
}

// Export renders every group, policy and blueprint as resources. Device
// specific assignments (policies pinned to single devices) are not
// expressible in manifests and are omitted; static membership is exported by
// device ID.
func (a *Applier) Export(ctx context.Context) ([]Resource, error) {
	var out []Resource
	gs, err := a.Store.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, g := range gs {
		names[g.ID] = g.Name
		spec := map[string]any{"kind": g.Kind}
		if g.Kind == store.GroupSmart {
			var rules any
			_ = json.Unmarshal(g.Rules, &rules)
			spec["rules"] = rules
		} else {
			ids, err := a.Store.GroupMembers(ctx, g.ID)
			if err != nil {
				return nil, err
			}
			spec["members"] = map[string]any{"ids": sortedCopy(ids)}
		}
		out = append(out, resource(KindGroup, g.Name, g.Description, spec))
	}
	toNames := func(ids []string) []string {
		var n []string
		for _, id := range ids {
			if name, ok := names[id]; ok {
				n = append(n, name)
			}
		}
		sort.Strings(n)
		return n
	}
	pols, err := a.Store.ListPolicies(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range pols {
		var doc any
		_ = json.Unmarshal(p.Document, &doc)
		out = append(out, resource(KindPolicy, p.Name, p.Description,
			map[string]any{"priority": p.Priority, "groups": toNames(p.GroupIDs), "document": doc}))
	}
	bps, err := a.Store.ListBlueprints(ctx)
	if err != nil {
		return nil, err
	}
	for _, b := range bps {
		var spec map[string]any
		_ = json.Unmarshal(b.Spec, &spec)
		if spec == nil {
			spec = map[string]any{}
		}
		spec["priority"] = b.Priority
		spec["groups"] = toNames(b.GroupIDs)
		out = append(out, resource(KindBlueprint, b.Name, b.Description, spec))
	}
	return out, nil
}

func resource(kind, name, desc string, spec any) Resource {
	b, _ := json.Marshal(spec)
	return Resource{APIVersion: APIVersion, Kind: kind, Metadata: Metadata{Name: name, Description: desc}, Spec: b}
}

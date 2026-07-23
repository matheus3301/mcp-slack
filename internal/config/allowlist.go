package config

import "sort"

// Allowlist is an immutable set of channel IDs the server is permitted to
// read. It is safe for concurrent use because it is never mutated after
// construction.
type Allowlist struct {
	set     map[string]struct{}
	ordered []string
}

// newAllowlist builds an Allowlist from a slice of already-validated IDs. The
// input order is preserved for deterministic iteration, and a sorted copy is
// used for stable output.
func newAllowlist(ids []string) Allowlist {
	set := make(map[string]struct{}, len(ids))
	ordered := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := set[id]; ok {
			continue
		}
		set[id] = struct{}{}
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	return Allowlist{set: set, ordered: ordered}
}

// Allowed reports whether the given channel ID is in the allowlist.
func (a Allowlist) Allowed(id string) bool {
	if a.set == nil {
		return false
	}
	_, ok := a.set[id]
	return ok
}

// IDs returns a sorted copy of the allowlisted channel IDs.
func (a Allowlist) IDs() []string {
	out := make([]string, len(a.ordered))
	copy(out, a.ordered)
	return out
}

// Len returns the number of allowlisted channels.
func (a Allowlist) Len() int { return len(a.ordered) }

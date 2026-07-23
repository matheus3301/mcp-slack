package config

import "sort"

// Allowlist is an immutable read policy. It is either an explicit set of
// channel IDs or a wildcard that permits any channel the bot currently belongs
// to. It is safe for concurrent use because it is never mutated after
// construction.
//
// A wildcard allowlist permits a well-formed C.../G... ID at the policy layer,
// but membership is still verified against Slack at request time. The zero
// value denies everything.
type Allowlist struct {
	wildcard bool
	set      map[string]struct{}
	ordered  []string
}

// newAllowlist builds an explicit Allowlist from already-validated IDs. Input
// order is deduplicated, and a sorted copy backs stable output.
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

// wildcardAllowlist builds a wildcard (member-scoped) Allowlist.
func wildcardAllowlist() Allowlist { return Allowlist{wildcard: true} }

// Wildcard reports whether this allowlist is the member-scoped wildcard.
func (a Allowlist) Wildcard() bool { return a.wildcard }

// Allowed reports whether the channel ID is permitted by policy. In wildcard
// mode any ID is permitted here; the caller must still verify Slack membership
// at request time. Callers validate the ID format before calling this.
func (a Allowlist) Allowed(id string) bool {
	if a.wildcard {
		return true
	}
	if a.set == nil {
		return false
	}
	_, ok := a.set[id]
	return ok
}

// IDs returns a sorted copy of the explicit channel IDs. It is empty in
// wildcard mode, where the set of readable channels is discovered from Slack.
func (a Allowlist) IDs() []string {
	out := make([]string, len(a.ordered))
	copy(out, a.ordered)
	return out
}

// Len returns the number of explicit channels, or 0 in wildcard mode.
func (a Allowlist) Len() int { return len(a.ordered) }

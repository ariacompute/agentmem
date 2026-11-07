// Package okf implements the Open Knowledge Format v0.2 document model.
//
// Conformance is deliberately permissive: only a non-empty `type` is required.
// Unknown types, unknown frontmatter keys, broken links and missing index files
// must never cause a consumer to reject a bundle.
package okf

import (
	"errors"
	"time"
)

// Trust tiers are derived from `verified`, never stored.
type TrustTier string

const (
	TrustUnverified       TrustTier = "unverified"
	TrustMachineConfirmed TrustTier = "machine-confirmed"
	TrustHumanReviewed    TrustTier = "human-reviewed"
)

// Status values defined by OKF v0.2.
const (
	StatusDraft      = "draft"
	StatusStable     = "stable"
	StatusDeprecated = "deprecated"
)

// ActorEvent records who did something and when.
type ActorEvent struct {
	By string
	At time.Time
}

// Window frames usage_count signals.
type Window struct {
	From time.Time
	To   time.Time
}

// Source is a material a concept derives from. Resource is required within an
// entry; credibility signals are objective facts, never a subjective score.
type Source struct {
	ID           string
	Resource     string
	Title        string
	Author       string
	UsageCount   int
	LastModified *time.Time
	UsageWindow  *Window
}

// Document is one OKF concept: YAML frontmatter plus a markdown body.
// Extra holds unknown frontmatter keys so round-tripping never loses data.
type Document struct {
	Type        string
	Title       string
	Description string
	Resource    string
	Tags        []string
	Sources     []Source
	Generated   *ActorEvent
	Verified    []ActorEvent
	Status      string
	StaleAfter  *time.Time
	Attested    *Attested
	Body        string

	extra map[string]*yamlNode
	order []string
}

// Extra returns a copy of the unknown frontmatter keys.
func (d *Document) Extra() map[string]bool {
	out := make(map[string]bool, len(d.extra))
	for k := range d.extra {
		out[k] = true
	}
	return out
}

// Validate enforces the single conformance requirement.
func (d *Document) Validate() error {
	if d == nil || d.Type == "" {
		return errors.New("okf: frontmatter field 'type' is required")
	}
	return nil
}

// TrustTier derives trust from verification actors.
func (d *Document) TrustTier() TrustTier {
	if len(d.Verified) == 0 {
		return TrustUnverified
	}
	for _, v := range d.Verified {
		if len(v.By) >= 6 && v.By[:6] == "human:" {
			return TrustHumanReviewed
		}
	}
	return TrustMachineConfirmed
}

// EffectiveStatus returns stable when status is absent.
func (d *Document) EffectiveStatus() string {
	if d.Status == "" {
		return StatusStable
	}
	return d.Status
}

// IsStale compares now against the absolute stale_after instant.
func (d *Document) IsStale(now time.Time) bool {
	return d.StaleAfter != nil && !now.Before(*d.StaleAfter)
}

// Visibility and Owner are agentmem extensions carried as frontmatter keys.
func (d *Document) Visibility() string { return d.extraString("visibility") }
func (d *Document) Owner() string      { return d.extraString("owner") }

func (d *Document) SetVisibility(v string) { d.setExtraScalar("visibility", v) }
func (d *Document) SetOwner(v string)      { d.setExtraScalar("owner", v) }

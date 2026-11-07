package store

import (
	"errors"

	"agentmem/internal/okf"
)

// ACLDefaults are applied when a document omits the agentmem extensions.
type ACLDefaults struct {
	Visibility string
	Owner      string
}

// ApplyACLDefaults writes visibility/owner into frontmatter as a portable copy.
// PostgreSQL remains the runtime authority for ACL checks.
func ApplyACLDefaults(d *okf.Document, def ACLDefaults) error {
	if d.Visibility() == "" {
		v := def.Visibility
		if v == "" {
			v = "private"
		}
		if err := ValidateVisibility(v); err != nil {
			return err
		}
		d.SetVisibility(v)
	}
	if err := ValidateVisibility(d.Visibility()); err != nil {
		return err
	}
	if d.Owner() == "" {
		o := def.Owner
		if o == "" {
			o = "system"
		}
		d.SetOwner(o)
	}
	return nil
}

// ValidateVisibility enforces the three supported visibility levels.
func ValidateVisibility(v string) error {
	switch v {
	case "private", "team", "public":
		return nil
	default:
		return errors.New("store: invalid visibility " + v)
	}
}

package store

import (
	"errors"
	"fmt"
	"strings"

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

// TrustedACL applies the ACL defaults using the authenticated subject as the
// owner. A request may not mint a document owned by somebody else: that would
// let an unprivileged writer take control of a private concept.
func TrustedACL(d *okf.Document, def ACLDefaults, subject string) error {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return errors.New("store: anonymous write rejected")
	}
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
		d.SetOwner(subject)
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(d.Owner()), subject) {
		return fmt.Errorf("store: owner does not match the authenticated subject")
	}
	return nil
}

// AuthorizeUpdate rejects overwriting a body owned by another subject. This
// is the Git-side counterpart of the HTTP permission check and covers CLI
// callers too. Legacy documents without an owner remain writable.
func AuthorizeUpdate(existing *okf.Document, subject string) error {
	if strings.TrimSpace(subject) == "" {
		return errors.New("store: anonymous write rejected")
	}
	if existing == nil {
		return nil
	}
	owner := strings.TrimSpace(existing.Owner())
	if owner != "" && !strings.EqualFold(owner, subject) {
		return errors.New("store: write denied for this concept")
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

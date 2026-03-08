// session_resolve.go implements name-based session resolution.
// Sessions can be addressed by bead ID (e.g., "gc-42") or by template
// name (e.g., "overseer"). Template names are resolved by querying the
// bead store for session beads with matching template metadata.
package main

import (
	"fmt"
	"strings"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/session"
)

// resolveSessionID resolves a user-provided identifier to a bead ID.
// Accepts either a bead ID (contains "gc-" prefix) or a template name.
// For template names, queries the store for open session beads with
// matching template metadata. Returns an error if the name is ambiguous
// (multiple matches) or not found.
func resolveSessionID(store beads.Store, identifier string) (string, error) {
	// If it looks like a bead ID, use directly.
	if looksLikeBeadID(identifier) {
		return identifier, nil
	}

	// Search for open session beads matching the template name.
	all, err := store.ListByLabel(session.LabelSession, 0)
	if err != nil {
		return "", fmt.Errorf("listing sessions: %w", err)
	}

	var matches []beads.Bead
	for _, b := range all {
		if b.Type != session.BeadType || b.Status == "closed" {
			continue
		}
		tmpl := b.Metadata["template"]
		// Match on exact template name or qualified name.
		if tmpl == identifier || strings.HasSuffix(tmpl, "/"+identifier) {
			matches = append(matches, b)
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no session found for %q", identifier)
	case 1:
		return matches[0].ID, nil
	default:
		var ids []string
		for _, m := range matches {
			ids = append(ids, fmt.Sprintf("%s (%s)", m.ID, m.Metadata["template"]))
		}
		return "", fmt.Errorf("ambiguous: %q matches %d sessions: %s\nUse a session ID instead", identifier, len(matches), strings.Join(ids, ", "))
	}
}

// looksLikeBeadID returns true if the identifier looks like a bead ID
// rather than a template name. Bead IDs typically start with "gc-" or
// contain a hyphen with a numeric suffix.
func looksLikeBeadID(s string) bool {
	return strings.HasPrefix(s, "gc-")
}

package domain

// === Veridian patch ===
// Typed hard/soft labels persisted on message_history.bounce_type, powering the
// dashboard KPI count_bounced_hard / count_bounced_soft (ticket
// todo/2026-06-16-kpi-bounce-hard-soft-dashboard.md).
//
// The column message_history.bounce_type exists since v8 but upstream NEVER
// writes it (the bounce path only sets bounced_at + status_info). This file
// gives the bounce processor a single canonical literal to write so the
// analytics measures (analytics.go: ILIKE 'hard%' / 'soft%') match exactly,
// instead of leaking the raw per-provider BounceType/BounceCategory strings
// (which vary: "Permanent", "Transient", DSN codes, …) into the KPI filter.
//
// Mapping is derived from BounceClassification (bounce_classification.go), the
// SAME enum used to decide suppression — so the KPI label and the suppression
// decision can never diverge.
//
// NOTE on coverage: only the HARD bounce path sets bounced_at on
// message_history today (a transient soft bounce is, by upstream design, NOT a
// terminal bounce — setting bounced_at would wrongly suppress the contact via
// the message_history triggers). So in practice count_bounced_hard reflects the
// populated data and count_bounced_soft stays ~0 on this flow; the mapper still
// returns a Soft label so the field is correct the day a soft bounce does get
// persisted (e.g. a future soft-at-threshold escalation that writes the row).

const (
	// VeridianBounceTypeHard is written to message_history.bounce_type for a
	// permanent bounce. Matched by analytics measure count_bounced_hard
	// (bounce_type ILIKE 'hard%').
	VeridianBounceTypeHard = "HardBounce"
	// VeridianBounceTypeSoft is written for a transient/soft bounce. Matched by
	// analytics measure count_bounced_soft (bounce_type ILIKE 'soft%').
	VeridianBounceTypeSoft = "SoftBounce"
)

// VeridianBounceTypeLabel maps a BounceClassification to the canonical literal
// persisted on message_history.bounce_type. Returns "" for an unknown class
// (caller leaves the column untouched — nil *string).
func VeridianBounceTypeLabel(class BounceClassification) string {
	switch class {
	case BounceClassificationHard:
		return VeridianBounceTypeHard
	case BounceClassificationSoftCount, BounceClassificationSoftIgnore:
		return VeridianBounceTypeSoft
	default:
		return ""
	}
}

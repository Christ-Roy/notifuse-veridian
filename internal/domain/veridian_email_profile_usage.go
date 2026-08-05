package domain

import (
	"context"
	"time"
)

const VeridianProviderClassUnclassified = "unclassified"

type VeridianEmailProfileUsageRow struct {
	ProfileID     string
	ProviderClass string
	ReservedUsed  int
	AcceptedUsed  int
}

type VeridianEmailProfileUsage struct {
	IntegrationID string `json:"integration_id"`
	// Used is the authoritative atomic quota counter. It can exceed accepted
	// history when an SMTP outcome is ambiguous after DATA and capacity must stay
	// consumed to prevent a duplicate send.
	Used                    int            `json:"used"`
	AcceptedUsed            int            `json:"accepted_used"`
	Cap                     int            `json:"cap"`
	Remaining               int            `json:"remaining"`
	AcceptedByProviderClass map[string]int `json:"accepted_by_provider_class"`
}

type VeridianEmailProfilesUsage struct {
	Date          string                      `json:"date"`
	TotalUsed     int                         `json:"total_used"`
	TotalAccepted int                         `json:"total_accepted"`
	Profiles      []VeridianEmailProfileUsage `json:"profiles"`
}

type VeridianEmailProfileUsageRepository interface {
	GetEmailProfileUsage(ctx context.Context, workspaceID string, since time.Time) ([]VeridianEmailProfileUsageRow, error)
}

type VeridianEmailProfileUsageService interface {
	GetEmailProfilesUsage(ctx context.Context, workspaceID string) (*VeridianEmailProfilesUsage, error)
}

func VeridianAggregateEmailProfileUsage(workspace *Workspace, rows []VeridianEmailProfileUsageRow, now time.Time) *VeridianEmailProfilesUsage {
	result := &VeridianEmailProfilesUsage{Date: now.UTC().Format("2006-01-02"), Profiles: []VeridianEmailProfileUsage{}}
	profiles := workspace.VeridianMarketingEmailProfiles()
	byID := make(map[string]int, len(profiles))
	for _, profile := range profiles {
		byClass := make(map[string]int, len(VeridianAllProviderClasses())+1)
		for _, class := range VeridianAllProviderClasses() {
			byClass[class] = 0
		}
		byClass[VeridianProviderClassUnclassified] = 0
		cap := profile.Provider.VeridianEffectiveProfileDailyCap()
		result.Profiles = append(result.Profiles, VeridianEmailProfileUsage{
			IntegrationID: profile.IntegrationID, Cap: cap, Remaining: cap, AcceptedByProviderClass: byClass,
		})
		byID[profile.IntegrationID] = len(result.Profiles) - 1
	}
	for _, row := range rows {
		index, exists := byID[row.ProfileID]
		if !exists {
			continue
		}
		profile := &result.Profiles[index]
		profile.Used += row.ReservedUsed
		if row.AcceptedUsed > 0 {
			class := row.ProviderClass
			if !IsValidProviderClass(class) {
				class = VeridianProviderClassUnclassified
			}
			profile.AcceptedUsed += row.AcceptedUsed
			profile.AcceptedByProviderClass[class] += row.AcceptedUsed
		}
	}
	for i := range result.Profiles {
		result.TotalUsed += result.Profiles[i].Used
		result.TotalAccepted += result.Profiles[i].AcceptedUsed
		if result.Profiles[i].Cap > 0 {
			result.Profiles[i].Remaining = max(result.Profiles[i].Cap-result.Profiles[i].Used, 0)
		} else {
			result.Profiles[i].Remaining = 0
		}
	}
	return result
}

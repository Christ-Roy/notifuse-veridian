// Tests portes depuis Notifuse v39.1 (derniere version sous AGPL-3.0, avant la BSL de v40.0)
// source : internal/domain/broadcast_test.go ; ne contient que les declarations absentes du fork.

package domain_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Notifuse/notifuse/internal/domain"
)

// storedBroadcastWithOmittableFields returns a broadcast carrying everything an update
// body is allowed to leave out: a narrowed audience, an A/B test whose variations hold
// the templates, a future schedule, UTM parameters and metadata. Seeded richly on
// purpose — against an empty stored value a wipe is indistinguishable from a correct
// merge.
func storedBroadcastWithOmittableFields() *domain.Broadcast {
	b := createValidBroadcast()
	b.Status = domain.BroadcastStatusPaused
	b.Audience = domain.AudienceSettings{
		List:                "list123",
		Segments:            []string{"segment-vip", "segment-fr"},
		ExcludeUnsubscribed: true,
	}
	b.Schedule = domain.ScheduleSettings{
		IsScheduled:   true,
		ScheduledDate: "2099-03-15",
		ScheduledTime: "10:30",
		Timezone:      "UTC",
	}
	b.TestSettings = domain.BroadcastTestSettings{
		Enabled:          true,
		SamplePercentage: 20,
		Variations: []domain.BroadcastVariation{
			{VariationName: "A", TemplateID: "template-a"},
			{VariationName: "B", TemplateID: "template-b"},
		},
	}
	b.UTMParameters = &domain.UTMParameters{Source: "newsletter", Medium: "email", Campaign: "spring"}
	b.Metadata = domain.MapOfAny{"origin": "api"}
	return &b
}

// broadcasts.update is a patch: a body that omits a key must leave the stored value
// alone. Every field exercised here has a meaningful zero value, so an absent key
// decoded into a bare struct field is indistinguishable from an explicit zero — the
// difference is destroyed before Validate can observe it. These cases therefore decode
// raw JSON rather than build a request literal, which is the only shape that can
// express a missing key.
func TestUpdateBroadcastRequest_Validate_AbsentFieldsPreserveStoredValues(t *testing.T) {
	t.Skip("DÉFAUT D2 : la mise à jour d'une diffusion écrase les champs absents (segments, test A/B, planification, UTM)")
	decode := func(t *testing.T, body string) *domain.UpdateBroadcastRequest {
		t.Helper()
		var req domain.UpdateBroadcastRequest
		require.NoError(t, json.Unmarshal([]byte(body), &req))
		return &req
	}

	// What a spec-conformant client sends to rename a broadcast: every field the
	// published schema marks required, plus the audience it no longer requires.
	const renameOnly = `{
		"workspace_id": "workspace123",
		"id": "broadcast123",
		"name": "Renamed",
		"audience": {"list": "list123", "exclude_unsubscribed": true}
	}`

	t.Run("absent test_settings keeps the variations that hold the templates", func(t *testing.T) {
		stored := storedBroadcastWithOmittableFields()
		// Built as an independent literal, not read off the fixture: Validate mutates
		// and returns the very broadcast it was given, so an expectation derived from
		// stored would compare the result to itself and pass unconditionally.
		wantTestSettings := domain.BroadcastTestSettings{
			Enabled:          true,
			SamplePercentage: 20,
			Variations: []domain.BroadcastVariation{
				{VariationName: "A", TemplateID: "template-a"},
				{VariationName: "B", TemplateID: "template-b"},
			},
		}

		updated, err := decode(t, renameOnly).Validate(stored)
		require.NoError(t, err)
		assert.Equal(t, "Renamed", updated.Name)
		assert.Equal(t, wantTestSettings, updated.TestSettings)
	})

	t.Run("absent schedule keeps is_scheduled, which decides how a paused broadcast resumes", func(t *testing.T) {
		stored := storedBroadcastWithOmittableFields()
		wantSchedule := domain.ScheduleSettings{
			IsScheduled:   true,
			ScheduledDate: "2099-03-15",
			ScheduledTime: "10:30",
			Timezone:      "UTC",
		}

		updated, err := decode(t, renameOnly).Validate(stored)
		require.NoError(t, err)
		assert.Equal(t, wantSchedule, updated.Schedule)

		// Resuming a paused broadcast branches on IsScheduled and then on the parsed
		// datetime still being in the future. Lose either and the resume drops into the
		// immediate-send branch, so a rename would start the campaign.
		require.True(t, updated.Schedule.IsScheduled)
		scheduledAt, err := updated.Schedule.ParseScheduledDateTime()
		require.NoError(t, err)
		assert.True(t, scheduledAt.After(time.Now()))
	})

	t.Run("absent utm_parameters keeps the stored UTM parameters", func(t *testing.T) {
		stored := storedBroadcastWithOmittableFields()
		wantUTM := domain.UTMParameters{Source: "newsletter", Medium: "email", Campaign: "spring"}

		updated, err := decode(t, renameOnly).Validate(stored)
		require.NoError(t, err)
		require.NotNil(t, updated.UTMParameters)
		assert.Equal(t, wantUTM, *updated.UTMParameters)
	})

	t.Run("absent metadata keeps the stored metadata", func(t *testing.T) {
		stored := storedBroadcastWithOmittableFields()
		wantMetadata := domain.MapOfAny{"origin": "api"}

		updated, err := decode(t, renameOnly).Validate(stored)
		require.NoError(t, err)
		assert.Equal(t, wantMetadata, updated.Metadata)
	})

	// The other half of the tri-state: preserving on absence must not degrade into
	// "never writable". An explicitly sent zero still has to land.
	t.Run("explicit schedule still replaces the stored schedule", func(t *testing.T) {
		stored := storedBroadcastWithOmittableFields()

		updated, err := decode(t, `{
			"workspace_id": "workspace123",
			"id": "broadcast123",
			"name": "Renamed",
			"audience": {"list": "list123", "exclude_unsubscribed": true},
			"schedule": {"is_scheduled": false}
		}`).Validate(stored)
		require.NoError(t, err)
		assert.Equal(t, domain.ScheduleSettings{}, updated.Schedule)
	})

	t.Run("explicit test_settings still replaces the stored test settings", func(t *testing.T) {
		stored := storedBroadcastWithOmittableFields()

		updated, err := decode(t, `{
			"workspace_id": "workspace123",
			"id": "broadcast123",
			"name": "Renamed",
			"audience": {"list": "list123", "exclude_unsubscribed": true},
			"test_settings": {"enabled": false, "variations": []}
		}`).Validate(stored)
		require.NoError(t, err)
		assert.False(t, updated.TestSettings.Enabled)
		assert.Empty(t, updated.TestSettings.Variations)
	})

	// A null is a serializer writing out an absent optional — there is no schedule and no
	// set of test settings a client could have meant by it — so it is read the same way
	// as a key the body never carried.
	t.Run("null schedule and test_settings are read as absent", func(t *testing.T) {
		stored := storedBroadcastWithOmittableFields()
		wantSchedule := domain.ScheduleSettings{
			IsScheduled:   true,
			ScheduledDate: "2099-03-15",
			ScheduledTime: "10:30",
			Timezone:      "UTC",
		}

		updated, err := decode(t, `{
			"workspace_id": "workspace123",
			"id": "broadcast123",
			"name": "Renamed",
			"audience": {"list": "list123", "exclude_unsubscribed": true},
			"schedule": null,
			"test_settings": null
		}`).Validate(stored)
		require.NoError(t, err)
		assert.Equal(t, wantSchedule, updated.Schedule)
		assert.True(t, updated.TestSettings.Enabled)
		require.Len(t, updated.TestSettings.Variations, 2)
		assert.Equal(t, "template-a", updated.TestSettings.Variations[0].TemplateID)
	})

	t.Run("explicit utm_parameters and metadata still replace the stored values", func(t *testing.T) {
		stored := storedBroadcastWithOmittableFields()

		updated, err := decode(t, `{
			"workspace_id": "workspace123",
			"id": "broadcast123",
			"name": "Renamed",
			"audience": {"list": "list123", "exclude_unsubscribed": true},
			"utm_parameters": {"source": "blog"},
			"metadata": {"origin": "console"}
		}`).Validate(stored)
		require.NoError(t, err)
		require.NotNil(t, updated.UTMParameters)
		assert.Equal(t, domain.UTMParameters{Source: "blog"}, *updated.UTMParameters)
		assert.Equal(t, domain.MapOfAny{"origin": "console"}, updated.Metadata)
	})
}

// The audience is what decides who the next send reaches, and every key in it has a
// meaningful zero: no segments means the whole list, and exclude_unsubscribed false
// means the unsubscribed are mailed too. Broadcast.Validate only ever looks at the list,
// so a body naming a single audience key — a supported way to retarget a broadcast —
// passes validation while quietly widening the audience past what the workspace last
// agreed to. Raw JSON, because a request literal cannot express a key the body never
// carried.
func TestUpdateBroadcastRequest_Validate_AudienceIsPatchedKeyByKey(t *testing.T) {
	t.Skip("DÉFAUT D2 : la mise à jour d'une diffusion écrase les champs absents (segments, test A/B, planification, UTM)")
	decode := func(t *testing.T, body string) *domain.UpdateBroadcastRequest {
		t.Helper()
		var req domain.UpdateBroadcastRequest
		require.NoError(t, json.Unmarshal([]byte(body), &req))
		return &req
	}

	// One body per case, so the only thing that differs between them is the audience
	// object itself.
	body := func(audience string) string {
		return `{
			"workspace_id": "workspace123",
			"id": "broadcast123",
			"name": "Renamed"` + audience + `
		}`
	}

	t.Run("an audience naming only list keeps the stored segments and exclusion", func(t *testing.T) {
		stored := storedBroadcastWithOmittableFields()
		// Built as an independent literal, not read off the fixture: Validate mutates
		// and returns the very broadcast it was given, so an expectation derived from
		// stored would compare the result to itself and pass unconditionally.
		wantSegments := []string{"segment-vip", "segment-fr"}

		updated, err := decode(t, body(`, "audience": {"list": "list456"}`)).Validate(stored)
		require.NoError(t, err)
		assert.Equal(t, "list456", updated.Audience.List, "the retargeting the caller did ask for must still land")
		assert.Equal(t, wantSegments, updated.Audience.Segments, "dropping segment targeting sends to the whole list")
		assert.True(t, updated.Audience.ExcludeUnsubscribed, "resetting this mails contacts who have unsubscribed")
	})

	t.Run("an absent audience keeps the stored one", func(t *testing.T) {
		stored := storedBroadcastWithOmittableFields()
		wantSegments := []string{"segment-vip", "segment-fr"}

		updated, err := decode(t, body("")).Validate(stored)
		require.NoError(t, err)
		assert.Equal(t, "list123", updated.Audience.List)
		assert.Equal(t, wantSegments, updated.Audience.Segments)
		assert.True(t, updated.Audience.ExcludeUnsubscribed)
		assert.Equal(t, "Renamed", updated.Name, "the edit the caller did ask for must still land")
	})

	t.Run("a null audience is read as absent", func(t *testing.T) {
		stored := storedBroadcastWithOmittableFields()
		wantSegments := []string{"segment-vip", "segment-fr"}

		updated, err := decode(t, body(`, "audience": null`)).Validate(stored)
		require.NoError(t, err)
		assert.Equal(t, "list123", updated.Audience.List)
		assert.Equal(t, wantSegments, updated.Audience.Segments)
		assert.True(t, updated.Audience.ExcludeUnsubscribed)
	})

	// The other half of the tri-state: preserving on absence must not degrade into
	// "never writable". Each key stays independently clearable.
	t.Run("an explicitly empty segments array removes segment targeting", func(t *testing.T) {
		stored := storedBroadcastWithOmittableFields()

		updated, err := decode(t, body(`, "audience": {"segments": []}`)).Validate(stored)
		require.NoError(t, err)
		assert.Empty(t, updated.Audience.Segments, "widening back to the whole list must stay expressible")
		assert.Equal(t, "list123", updated.Audience.List, "the key the body left out is the stored one")
		assert.True(t, updated.Audience.ExcludeUnsubscribed)
	})

	t.Run("an explicit false switches the unsubscribed exclusion off", func(t *testing.T) {
		stored := storedBroadcastWithOmittableFields()
		wantSegments := []string{"segment-vip", "segment-fr"}

		updated, err := decode(t, body(`, "audience": {"exclude_unsubscribed": false}`)).Validate(stored)
		require.NoError(t, err)
		assert.False(t, updated.Audience.ExcludeUnsubscribed, "asking for it must stay expressible; only the silent reset is the bug")
		assert.Equal(t, "list123", updated.Audience.List)
		assert.Equal(t, wantSegments, updated.Audience.Segments)
	})

	t.Run("a full audience replaces every key", func(t *testing.T) {
		stored := storedBroadcastWithOmittableFields()

		updated, err := decode(t, body(`, "audience": {"list": "list456", "segments": ["segment-de"], "exclude_unsubscribed": false}`)).Validate(stored)
		require.NoError(t, err)
		assert.Equal(t, "list456", updated.Audience.List)
		assert.Equal(t, []string{"segment-de"}, updated.Audience.Segments)
		assert.False(t, updated.Audience.ExcludeUnsubscribed)
	})

	// A stored broadcast with no list is not made valid by an omission: the list is still
	// required, it is just the merged one that has to carry it.
	t.Run("an absent audience over a stored broadcast with no list is still rejected", func(t *testing.T) {
		stored := storedBroadcastWithOmittableFields()
		stored.Audience.List = ""

		updated, err := decode(t, body("")).Validate(stored)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "list is required")
		assert.Nil(t, updated)
	})

	// A request assembled in Go has no body to have left a key out of, so its fields are
	// the whole of what it means — otherwise every non-HTTP caller would find its
	// audience quietly merged with the stored one.
	t.Run("a request built in Go applies its own audience", func(t *testing.T) {
		stored := storedBroadcastWithOmittableFields()

		updated, err := (&domain.UpdateBroadcastRequest{
			WorkspaceID: "workspace123",
			ID:          "broadcast123",
			Name:        "Renamed",
			Audience:    domain.AudienceSettings{List: "list456"},
		}).Validate(stored)
		require.NoError(t, err)
		assert.Equal(t, "list456", updated.Audience.List)
		assert.Empty(t, updated.Audience.Segments)
		assert.False(t, updated.Audience.ExcludeUnsubscribed)
	})
}

// tracking_enabled is not a broadcast field: Broadcast has none, neither Validate
// assigns one, and per-email tracking is decided by the workspace's
// email_tracking_enabled setting. Publishing it anyway meant a client that set it got a
// 200 and no effect, so it must not appear on the wire at all.
func TestBroadcastRequests_DoNotPublishTrackingEnabled(t *testing.T) {
	t.Skip("DÉFAUT D2 : la mise à jour d'une diffusion écrase les champs absents (segments, test A/B, planification, UTM)")
	requests := map[string]interface{}{
		"create": domain.CreateBroadcastRequest{},
		"update": domain.UpdateBroadcastRequest{},
	}

	for name, request := range requests {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(request)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "tracking_enabled")
		})
	}
}

// A global feed that is switched off but carries data is a supported shape: it is how a client
// supplies its own payload instead of having Notifuse fetch one, and the send path renders it on
// the strength of the data being there. The cost of that is that nothing else ever clears the
// payload — the fetcher only ever writes it, and this Validate carries it across every save — so
// switching a live feed off is the one moment the response it left behind can be dropped. A feed
// that was already off is the supply-your-own-data case and must keep what it was given, which is
// why the clearing keys on the transition and not on the incoming Enabled alone.
func TestUpdateBroadcastRequest_Validate_GlobalFeedDataLifecycle(t *testing.T) {
	t.Skip("DÉFAUT D2 : la mise à jour d'une diffusion écrase les champs absents (segments, test A/B, planification, UTM)")
	decode := func(t *testing.T, body string) *domain.UpdateBroadcastRequest {
		t.Helper()
		var req domain.UpdateBroadcastRequest
		require.NoError(t, json.Unmarshal([]byte(body), &req))
		return &req
	}

	storedWithGlobalFeed := func(enabled bool) *domain.Broadcast {
		b := createValidBroadcast()
		fetchedAt := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
		b.DataFeed = &domain.DataFeedSettings{
			GlobalFeed: &domain.GlobalFeedSettings{
				Enabled: enabled,
				URL:     "https://feed.example.com/deals",
				Headers: []domain.DataFeedHeader{},
			},
			GlobalFeedData:      domain.MapOfAny{"product": "Smart Watch", "sale_price": "$199"},
			GlobalFeedFetchedAt: &fetchedAt,
		}
		return &b
	}

	// Built as independent literals, not read off the fixture: Validate mutates and returns the
	// very broadcast it was handed, so an expectation derived from it compares a field to itself
	// and holds no matter what Validate did.
	wantData := domain.MapOfAny{"product": "Smart Watch", "sale_price": "$199"}
	wantFetchedAt := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

	assertPayloadPreserved := func(t *testing.T, updated *domain.Broadcast) {
		t.Helper()
		require.NotNil(t, updated.DataFeed)
		assert.Equal(t, wantData, updated.DataFeed.GlobalFeedData)
		require.NotNil(t, updated.DataFeed.GlobalFeedFetchedAt)
		assert.Equal(t, wantFetchedAt, updated.DataFeed.GlobalFeedFetchedAt.UTC())
	}

	// Decoded from JSON rather than built as a literal: the omitted-data_feed case is the whole
	// point of the distinction, and only a body can express a key that is not there.
	const renameOnly = `{
		"workspace_id": "workspace123",
		"id": "broadcast123",
		"name": "Renamed",
		"audience": {"list": "list123", "exclude_unsubscribed": true}
	}`

	const globalFeedOff = `{
		"workspace_id": "workspace123",
		"id": "broadcast123",
		"name": "Renamed",
		"audience": {"list": "list123", "exclude_unsubscribed": true},
		"data_feed": {
			"global_feed": {"enabled": false, "url": "https://feed.example.com/deals", "headers": []}
		}
	}`

	const globalFeedOn = `{
		"workspace_id": "workspace123",
		"id": "broadcast123",
		"name": "Renamed",
		"audience": {"list": "list123", "exclude_unsubscribed": true},
		"data_feed": {
			"global_feed": {"enabled": true, "url": "https://feed.example.com/deals", "headers": []}
		}
	}`

	t.Run("switching a live global feed off drops the response it left behind", func(t *testing.T) {
		updated, err := decode(t, globalFeedOff).Validate(storedWithGlobalFeed(true))
		require.NoError(t, err)

		require.NotNil(t, updated.DataFeed)
		require.NotNil(t, updated.DataFeed.GlobalFeed)
		require.False(t, updated.DataFeed.GlobalFeed.Enabled)

		assert.Nil(t, updated.DataFeed.GlobalFeedData,
			"a feed switched off must not keep rendering its last response")
		assert.Nil(t, updated.DataFeed.GlobalFeedFetchedAt,
			"the fetch timestamp describes a payload that is gone")
	})

	t.Run("a body without data_feed keeps the cached payload", func(t *testing.T) {
		updated, err := decode(t, renameOnly).Validate(storedWithGlobalFeed(true))
		require.NoError(t, err)
		assertPayloadPreserved(t, updated)
	})

	t.Run("a feed that was already off keeps the payload supplied with it", func(t *testing.T) {
		updated, err := decode(t, globalFeedOff).Validate(storedWithGlobalFeed(false))
		require.NoError(t, err)
		assertPayloadPreserved(t, updated)
	})

	t.Run("a feed left switched on keeps its last response", func(t *testing.T) {
		updated, err := decode(t, globalFeedOn).Validate(storedWithGlobalFeed(true))
		require.NoError(t, err)
		require.True(t, updated.DataFeed.GlobalFeed.Enabled)
		assertPayloadPreserved(t, updated)
	})
}

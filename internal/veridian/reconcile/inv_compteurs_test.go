package reconcile

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- a. compteurs stockés ---

func TestJudgeStoredStats(t *testing.T) {
	ok := storedStatsRow{AutomationID: "a", Name: "A", Stored: counters{10, 2, 3, 1}, Real: counters{10, 2, 3, 1}}
	bad := storedStatsRow{AutomationID: "b", Name: "Prospection", Stored: counters{3054, 0, 1214, 0}, Real: counters{3054, 0, 1216, 0}}
	fs := judgeStoredStats([]storedStatsRow{ok, bad})
	require.Len(t, fs, 1)
	f := fs[0]
	assert.Equal(t, "stats_stockees:b", f.SubjectRef)
	assert.Contains(t, f.Explanation, "sortis : fiche 1 214, base 1 216")
	assert.NotContains(t, f.Explanation, "inscrits :", "seul le compteur faux est cité")
	require.NotNil(t, f.ProposedAction)
	assert.Equal(t, ActionRecountStats, f.ProposedAction.Kind)
	assert.Equal(t, "b", f.ProposedAction.Params["automation_id"])
}

var storedCols = []string{"id", "name", "se", "sc", "sx", "sf", "re", "rc", "rx", "rf"}

func TestConfirmedStoredStats_TransientDriftIsIgnoredPersistentIsKept(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	env := Env{DB: db}

	// Dérive vue à la 1re lecture, disparue à la 2e : un événement en vol, pas un écart.
	mock.ExpectQuery("FROM automations a").WillReturnRows(sqlmock.NewRows(storedCols).AddRow("a", "A", 5, 0, 1, 0, 5, 0, 2, 0))
	mock.ExpectQuery("FROM automations a").WillReturnRows(sqlmock.NewRows(storedCols).AddRow("a", "A", 5, 0, 2, 0, 5, 0, 2, 0))
	rows, err := confirmedStoredStats(context.Background(), env)
	require.NoError(t, err)
	assert.Empty(t, judgeStoredStats(rows))

	// Même dérive aux deux lectures : écart réel.
	mock.ExpectQuery("FROM automations a").WillReturnRows(sqlmock.NewRows(storedCols).AddRow("a", "A", 5, 0, 1, 0, 5, 0, 2, 0))
	mock.ExpectQuery("FROM automations a").WillReturnRows(sqlmock.NewRows(storedCols).AddRow("a", "A", 5, 0, 1, 0, 5, 0, 2, 0))
	rows, err = confirmedStoredStats(context.Background(), env)
	require.NoError(t, err)
	assert.Len(t, judgeStoredStats(rows), 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestConfirmedStoredStats_NoDriftReadsOnce(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery("FROM automations a").WillReturnRows(sqlmock.NewRows(storedCols).AddRow("a", "A", 5, 0, 2, 0, 5, 0, 2, 0))
	rows, err := confirmedStoredStats(context.Background(), Env{DB: db})
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	assert.NoError(t, mock.ExpectationsWereMet(), "une seule lecture quand tout concorde")
}

// --- b. envoyés par étape : 2 947 servis contre 2 891 réels ---

func TestJudgeStages_Devenir2947Vs2891(t *testing.T) {
	fs := judgeStages([]stageRow{{
		AutomationID: "ecomdevenir-j0j4j10-r2", Name: "E-commerce à devenir", Stage: 1, Label: "J0", Served: 2947, Base: 2891,
		Diag: []exitDiag{{Status: "exited", Reason: "unsubscribed", Count: 56}},
	}})
	require.Len(t, fs, 1)
	f := fs[0]
	assert.Equal(t, "etape_envoyes:ecomdevenir-j0j4j10-r2:1", f.SubjectRef)
	assert.Contains(t, f.Title, "2 947 servis, 2 891 réellement partis")
	assert.Contains(t, f.Explanation, "56 désinscrits entre la mise en file et l'envoi")
	assert.Equal(t, 56, f.Evidence["gap"])
	assert.Nil(t, f.ProposedAction, "le chiffre se corrige dans le code de comptage, pas dans les données")
}

func TestJudgeStages_EqualAndUndercount(t *testing.T) {
	assert.Empty(t, judgeStages([]stageRow{{AutomationID: "a", Stage: 1, Label: "J0", Served: 10, Base: 10}}))
	fs := judgeStages([]stageRow{{AutomationID: "a", Name: "A", Stage: 2, Label: "J+4", Served: 8, Base: 10}})
	require.Len(t, fs, 1)
	assert.Contains(t, fs[0].Explanation, "ne compte pas 2 contacts")
}

func TestDescribeDiag_FamiliesFromRealExitReasons(t *testing.T) {
	got := describeDiag([]exitDiag{
		{Status: "exited", Reason: "list_status_unsubscribed", Count: 4},
		{Status: "exited", Reason: "unsubscribed", Count: 3},
		{Status: "exited", Reason: "contact_replied", Count: 2},
		{Status: "sending", Reason: "", Count: 1},
	})
	assert.Equal(t, "7 désinscrits entre la mise en file et l'envoi, 2 sortis pour réponse entre la mise en file et l'envoi, 1 en cours d'envoi", got)
}

// --- c. réponses : 16 contre 17 ---

func TestJudgeReplies_16Vs17(t *testing.T) {
	// 17 sorties pour réponse, 16 réponses humaines : un contact est sorti pour une réponse
	// qui n'est pas une réponse humaine enregistrée.
	fs := judgeReplies([]replyRow{{
		AutomationID: "ecomdevenir", Name: "E-commerce à devenir", RepliesServed: 16, FirstStageServed: 16, ExitedReplied: 17,
		ExitedNoHumanRow: 1, ExitedNoHumanSamp: []string{"z@x.fr"},
	}})
	require.Len(t, fs, 1)
	f := fs[0]
	assert.Equal(t, "sorties_reponse:ecomdevenir", f.SubjectRef)
	assert.Equal(t, SeverityWarning, f.Severity)
	assert.Contains(t, f.Explanation, "16 contre 17")
	assert.Contains(t, f.Explanation, "(17)")
	assert.Contains(t, f.Explanation, "(16)")
}

func TestJudgeReplies_ManualExitIsInfo(t *testing.T) {
	fs := judgeReplies([]replyRow{{AutomationID: "a", Name: "A", RepliesServed: 5, FirstStageServed: 5, ExitedReplied: 6, ExitedNoHumanRow: 1, ManualOnly: true}})
	require.Len(t, fs, 1)
	assert.Equal(t, SeverityInfo, fs[0].Severity)
	assert.Contains(t, fs[0].Explanation, "à la main")
}

func TestJudgeReplies_StageSumIdentity(t *testing.T) {
	// 31 à J0 + 1 sans mail = 32 réponses de la séquence : cohérent (cas animalsd du 29/10).
	assert.Empty(t, judgeReplies([]replyRow{{AutomationID: "p5", Name: "P5", RepliesServed: 32, FirstStageServed: 31, NoMailRepliers: 1, ExitedReplied: 32}}))
	// 30 + 1 ≠ 32 : une des requêtes a dérivé.
	fs := judgeReplies([]replyRow{{AutomationID: "p5", Name: "P5", RepliesServed: 32, FirstStageServed: 30, NoMailRepliers: 1}})
	require.Len(t, fs, 1)
	assert.Equal(t, "reponses_etapes:p5", fs[0].SubjectRef)
}

// --- d. raisons de sortie mal rangées ---

func TestJudgeExitClasses_RealReasonsAreRecognised(t *testing.T) {
	var rows []exitReasonRow
	for _, r := range []string{"contact_replied", "list_status_unsubscribed", "list_status_bounced", "first_mail_bounced", "replied", "bounced", "unsubscribed", "contact_not_on_list", "automation_deleted", "manual"} {
		rows = append(rows, exitReasonRow{AutomationID: "a", Reason: r, Count: 2})
	}
	assert.Empty(t, judgeExitClasses(rows), "toutes les raisons du moteur sont rangées dans leur famille")
}

func TestJudgeExitClasses_UnknownSpellingIsReported(t *testing.T) {
	fs := judgeExitClasses([]exitReasonRow{{AutomationID: "a", Reason: "user_unsubscribing", Count: 5}, {AutomationID: "b", Reason: "user_unsubscribing", Count: 2}})
	require.Len(t, fs, 1)
	assert.Equal(t, "sortie_mal_rangee:user_unsubscribing", fs[0].SubjectRef)
	assert.Equal(t, 7, fs[0].Evidence["count"])
}

// --- e. partition : 1 127 contre 1 108 ---

func TestJudgeServedPartition_1127Vs1108(t *testing.T) {
	seq := func(sent, queued, waiting, before int) domain.VeridianSequenceStats {
		return domain.VeridianSequenceStats{
			AutomationID: "p5", Name: "Prospection", Enrolled: 1108,
			Stages: []domain.VeridianProspectionStage{{Stage: 1, Sent: sent, Queued: queued, Waiting: waiting}},
			Exits:  domain.VeridianProspectionExits{BeforeFirstMail: before},
		}
	}
	// 397 partis + 710 en file + 1 sorti avant tout mail = 1 108 : cohérent.
	assert.Empty(t, judgeServedPartition([]domain.VeridianSequenceStats{seq(397, 710, 0, 1)}))
	// Les 20 « sortis après » additionnés aux partis : 1 127.
	fs := judgeServedPartition([]domain.VeridianSequenceStats{seq(397+20, 710, 0, 0)})
	require.Len(t, fs, 1)
	assert.Equal(t, "partition_etape1:p5", fs[0].SubjectRef)
	assert.Equal(t, 1127, fs[0].Evidence["sum"])
	// Une séquence avec des terminés n'est pas jugée (les terminés sans mail échappent aux compartiments).
	done := seq(1, 1, 1, 1)
	done.Completed = 3
	assert.Empty(t, judgeServedPartition([]domain.VeridianSequenceStats{done}))
}

var _ = time.Now

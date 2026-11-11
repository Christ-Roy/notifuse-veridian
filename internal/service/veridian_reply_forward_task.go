package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Notifuse/notifuse/internal/domain"
	"github.com/Notifuse/notifuse/pkg/logger"
)

// Veridian fork (fiche 78, 11/11/2026) : le rejeu des copies de reponses passe par le MECANISME NATIF de
// taches du fork (domain.Task, TaskService, planificateur interne) : aucune tache planifiee externe, aucun
// cron. Une tache recurrente par workspace (veridian_reply_forward_retry, toutes les 60 s), creee a la
// demande des qu'une copie est reservee. Elle vit dans la table tasks : un redemarrage du processus ne la
// perd pas, et les copies pending/retry/deferred sont reprises par le journal.

// VeridianReplyForwardTaskType est le type de la tache native de rejeu.
const VeridianReplyForwardTaskType = "veridian_reply_forward_retry"

const veridianReplyForwardTaskInterval = 60 // secondes

// VeridianReplyForwardTaskProcessor execute la tache de rejeu d'un workspace.
type VeridianReplyForwardTaskProcessor struct {
	forwarder *VeridianReplyForwarder
	logger    logger.Logger
}

// NewVeridianReplyForwardTaskProcessor construit le processeur.
func NewVeridianReplyForwardTaskProcessor(forwarder *VeridianReplyForwarder, log logger.Logger) *VeridianReplyForwardTaskProcessor {
	return &VeridianReplyForwardTaskProcessor{forwarder: forwarder, logger: log}
}

// CanProcess : seulement veridian_reply_forward_retry.
func (p *VeridianReplyForwardTaskProcessor) CanProcess(taskType string) bool {
	return taskType == VeridianReplyForwardTaskType
}

// Process rejoue les copies dues. La tache est recurrente : on rend toujours (true, nil), une erreur
// est journalisee et le passage suivant (60 s plus tard) reessaie. Une erreur rendue marquerait la tache
// "failed" pour de bon, et le rejeu s'arreterait en silence.
func (p *VeridianReplyForwardTaskProcessor) Process(ctx context.Context, task *domain.Task, timeoutAt time.Time) (bool, error) {
	n, err := p.forwarder.ProcessDue(ctx, task.WorkspaceID, timeoutAt.Add(-5*time.Second))
	fields := map[string]interface{}{"task_id": task.ID, "workspace_id": task.WorkspaceID, "processed": n}
	if err != nil {
		fields["error"] = err.Error()
		p.logger.WithFields(fields).Warn("VeridianReplyForward: retry pass failed, will retry at next run")
		return true, nil
	}
	if n > 0 {
		p.logger.WithFields(fields).Info("VeridianReplyForward: retry pass done")
	}
	return true, nil
}

// VeridianReplyForwardTaskEnsurer cree (ou remet en route) la tache de rejeu d'un workspace.
type VeridianReplyForwardTaskEnsurer struct {
	taskRepo domain.TaskRepository
}

// NewVeridianReplyForwardTaskEnsurer construit l'ensurer.
func NewVeridianReplyForwardTaskEnsurer(taskRepo domain.TaskRepository) *VeridianReplyForwardTaskEnsurer {
	return &VeridianReplyForwardTaskEnsurer{taskRepo: taskRepo}
}

// EnsureReplyForwardTask : idempotent. Une tache saine (pending, prochain passage proche) n'est pas touchee ;
// une tache en echec, en pause ou dont l'echeance a derive est remise en pending maintenant.
func (e *VeridianReplyForwardTaskEnsurer) EnsureReplyForwardTask(ctx context.Context, workspaceID string) error {
	tasks, _, err := e.taskRepo.List(ctx, workspaceID, domain.TaskFilter{Type: []string{VeridianReplyForwardTaskType}, Limit: 1})
	if err != nil {
		return fmt.Errorf("list reply forward task: %w", err)
	}
	now := time.Now().UTC()
	if len(tasks) > 0 {
		t := tasks[0]
		healthy := t.Status == domain.TaskStatusPending || t.Status == domain.TaskStatusRunning
		if healthy && (t.NextRunAfter == nil || t.NextRunAfter.Before(now.Add(5*time.Minute))) {
			return nil
		}
		t.Status = domain.TaskStatusPending
		t.NextRunAfter = &now
		if err := e.taskRepo.Update(ctx, workspaceID, t); err != nil {
			return fmt.Errorf("restart reply forward task: %w", err)
		}
		return nil
	}
	interval := int64(veridianReplyForwardTaskInterval)
	next := now.Add(time.Duration(interval) * time.Second)
	task := &domain.Task{
		WorkspaceID:       workspaceID,
		Type:              VeridianReplyForwardTaskType,
		Status:            domain.TaskStatusPending,
		NextRunAfter:      &next,
		MaxRuntime:        50,
		MaxRetries:        3,
		RetryInterval:     60,
		RecurringInterval: &interval,
		State:             &domain.TaskState{Message: "Redirection des reponses : rejeu des copies en attente"},
	}
	if err := e.taskRepo.Create(ctx, workspaceID, task); err != nil {
		return fmt.Errorf("create reply forward task: %w", err)
	}
	return nil
}

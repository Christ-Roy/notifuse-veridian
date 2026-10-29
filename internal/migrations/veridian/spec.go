package veridian

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"sync"
)

// Executor est ce que voit une migration : la transaction du runner.
type Executor interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

// Reserved : numéro de migration -> lot propriétaire. main ajoute une ligne pour attribuer un numéro
// (y compris les numéros CONTRACT). Un numéro absent d'ici est refusé à l'enregistrement.
var Reserved = map[int]string{
	63: "L1",
	64: "L1b",
	65: "L2",
	66: "L3",
	67: "L4",
}

// Spec décrit une migration Veridian. Les listes d'instructions sont jouées dans l'ordre, dans une
// transaction, sur la base système (System) et sur chaque base workspace (Workspace).
type Spec struct {
	Number int    // numéro réservé (Reserved)
	Lot    string // lot propriétaire, doit égaler Reserved[Number]
	Name   string // nom court, pour les journaux

	System    []string
	Workspace []string

	// Pour du Go qui ne se dit pas en SQL (rétro-remplissage). Non analysables : rester additif et idempotent.
	SystemFunc    func(ctx context.Context, db Executor) error
	WorkspaceFunc func(ctx context.Context, workspaceID string, db Executor) error

	// Contract : étape destructive (DROP, SET NOT NULL, RENAME). Dispense du contrôle expand-only,
	// exige ContractOf = numéro de l'étape expand correspondante (déjà enregistrée, numéro inférieur).
	Contract   bool
	ContractOf int
}

// Registry conserve les migrations enregistrées.
type Registry struct {
	mu    sync.RWMutex
	specs map[int]Spec
}

// NewRegistry crée un registre vide (les tests en utilisent un isolé).
func NewRegistry() *Registry { return &Registry{specs: map[int]Spec{}} }

// Default est le registre du processus.
var Default = NewRegistry()

// Register enregistre dans le registre du processus ; panic si la migration est invalide
// (détecté au démarrage et par go test, jamais en production en cours de route).
func Register(s Spec) {
	if err := Default.Register(s); err != nil {
		panic("veridian migrations: " + err.Error())
	}
}

// Register valide et enregistre une migration.
func (r *Registry) Register(s Spec) error {
	owner, ok := Reserved[s.Number]
	if !ok {
		return fmt.Errorf("V%d : numéro non réservé (voir Reserved, attribué par main)", s.Number)
	}
	if s.Lot != owner {
		return fmt.Errorf("V%d appartient au lot %s, pas à %q", s.Number, owner, s.Lot)
	}
	if s.Name == "" {
		return fmt.Errorf("V%d : nom vide", s.Number)
	}
	if len(s.System) == 0 && len(s.Workspace) == 0 && s.SystemFunc == nil && s.WorkspaceFunc == nil {
		return fmt.Errorf("V%d : migration vide", s.Number)
	}
	if s.Contract {
		if s.ContractOf <= 0 || s.ContractOf >= s.Number {
			return fmt.Errorf("V%d : un CONTRACT désigne l'étape expand qu'il suit (ContractOf < Number)", s.Number)
		}
	} else {
		if s.ContractOf != 0 {
			return fmt.Errorf("V%d : ContractOf sans Contract", s.Number)
		}
		for _, stmt := range append(append([]string{}, s.System...), s.Workspace...) {
			if err := CheckExpandOnly(stmt); err != nil {
				return fmt.Errorf("V%d %s : %w", s.Number, s.Name, err)
			}
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.specs[s.Number]; dup {
		return fmt.Errorf("V%d enregistrée deux fois", s.Number)
	}
	if s.Contract {
		if _, ok := r.specs[s.ContractOf]; !ok {
			return fmt.Errorf("V%d : l'étape expand V%d n'est pas enregistrée", s.Number, s.ContractOf)
		}
	}
	r.specs[s.Number] = s
	return nil
}

// List renvoie les migrations triées par numéro.
func (r *Registry) List() []Spec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Spec, 0, len(r.specs))
	for _, s := range r.specs {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out
}

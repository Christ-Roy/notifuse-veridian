package module

import "testing"

func TestLoad_Reglages(t *testing.T) {
	env := map[string]string{"VERIDIAN_ACTIVITE_ENABLED": "true", "VERIDIAN_ACTIVITE_METRICS_PORT": "9464", "VERIDIAN_ACTIVITE_WORKSPACES": " a, b ,,", "VERIDIAN_ACTIVITE_ALERT_AFTER": "20m"}
	s := Load(func(k string) string { return env[k] })
	if !s.Enabled || s.MetricsPort != 9464 || len(s.Only) != 2 || !s.Only["a"] || !s.Only["b"] || s.AlertAfter.Minutes() != 20 {
		t.Fatalf("%+v", s)
	}
	if s := Load(func(string) string { return "" }); s.Enabled || s.MetricsPort != 0 || s.Only != nil {
		t.Fatalf("défauts : éteint, sans écouteur, tous les espaces : %+v", s)
	}
	if s := Load(func(k string) string { return map[string]string{"VERIDIAN_ACTIVITE_ENABLED": "1"}[k] }); s.Enabled {
		t.Fatal("seul « true » allume")
	}
}

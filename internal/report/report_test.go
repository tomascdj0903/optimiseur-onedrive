package report_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"optimiseur-onedrive/internal/analysis"
	P "optimiseur-onedrive/internal/platform"
	"optimiseur-onedrive/internal/report"
	"optimiseur-onedrive/internal/scan"
	T "optimiseur-onedrive/internal/testutil"
)

func build(masked bool) string {
	fs := T.Fixture()
	targets := []scan.Target{{Path: T.Root, Kind: scan.KindOneDrive, Label: "OneDrive – Personnel", Active: true}}
	res := scan.Run(context.Background(), fs, targets, scan.DefaultOptions(), nil, nil)
	d := analysis.Diagnose(analysis.Env{AppVersion: "1.0.0", Discovery: analysis.Discovery{Targets: targets}, Same: T.Same, Windows: true,
		OneDrive: P.OneDriveInfo{Accounts: []P.Account{{Key: "Personal", Email: "jean.dupont@example.com", UserFolder: T.Root}}}}, res)
	m := report.NewMasker(masked, "/C/Users/jean", T.Same)
	m.AddRoot(T.Root, "OneDrive – Personnel")
	return report.Text(d, res, nil, m)
}

func TestReportSections(t *testing.T) {
	txt := build(false)
	for _, s := range []string{"1. RÉSUMÉ", "POURQUOI", "6. RECOMMANDATIONS", "Niveau de risque", "Vérification", "ANNEXE TECHNIQUE", "mariage.mov"} {
		if !strings.Contains(txt, s) {
			t.Errorf("section manquante : %s", s)
		}
	}
	// L'annexe doit être du JSON valide.
	i := strings.Index(txt, "ANNEXE TECHNIQUE")
	j := strings.Index(txt[i:], "{")
	var v map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(txt[i+j:])), &v); err != nil {
		t.Fatalf("annexe JSON invalide : %v", err)
	}
}

func TestMaskedReportHidesNames(t *testing.T) {
	txt := build(true)
	for _, s := range []string{"mariage", "vacances", "Mon OneDrive (perso)", "jean.dupont", "projet.zip"} {
		if strings.Contains(txt, s) {
			t.Errorf("le nom « %s » apparaît malgré le masquage", s)
		}
	}
	if !strings.Contains(txt, ".mov") || !strings.Contains(txt, "[OneDrive – Personnel]") {
		t.Error("le masquage doit garder les extensions et le nom du dossier racine")
	}
}

func TestMaskerStable(t *testing.T) {
	m := report.NewMasker(true, "", T.Same)
	m.AddRoot("/D/OD", "OneDrive")
	a, b := m.Path("/D/OD/Photos/x.jpg"), m.Path("/D/OD/Photos/y.jpg")
	if a == b || strings.Split(a, "/")[1] != strings.Split(b, "/")[1] {
		t.Fatalf("masquage instable : %s / %s", a, b)
	}
	if m.Email("jean@ex.com") != "j***@ex.com" {
		t.Fatal(m.Email("jean@ex.com"))
	}
}

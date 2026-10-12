package analysis_test

import (
	"context"
	"strings"
	"testing"

	"optimiseur-onedrive/internal/analysis"
	P "optimiseur-onedrive/internal/platform"
	"optimiseur-onedrive/internal/scan"
	T "optimiseur-onedrive/internal/testutil"
)

func inputs() analysis.Inputs {
	fs := T.Fixture()
	return analysis.Inputs{
		OneDrive: P.OneDriveInfo{Installed: true, Running: true, RunningKnown: true, FilterDriver: "démarrage automatique",
			Accounts: []P.Account{{Key: "Personal", Email: "jean@example.com", UserFolder: T.Root}},
			SyncRoots: []P.SyncRoot{
				{ID: "OneDrive!S-1!Personal|1", Provider: "OneDrive", Path: T.Root, OneDrive: true},
				{ID: "Dropbox!S-1", Provider: "Dropbox", Path: "/C/Users/jean/Dropbox"},
			},
			EnvFolders: []string{T.Root + "/"}},
		Known: []P.KnownFolder{
			{ID: "Documents", Label: "Documents", Path: T.Root + "/Documents", DefaultPath: "/C/Users/jean/Documents"},
			{ID: "Desktop", Label: "Bureau", Path: "/C/Users/jean/Desktop", DefaultPath: "/C/Users/jean/Desktop"},
		},
		Volumes: []P.Volume{{Root: "/C", Type: "fixe", Total: 500 << 30, Free: 20 << 30, System: true}},
		Profile: "/C/Users/jean",
		ReadDir: fs.ReadDir,
		Exists:  func(p string) bool { _, _, err := fs.Attrs(p); return err == nil },
		Same:    T.Same,
	}
}

func TestDiscoverCustomLocationOrphansLegacy(t *testing.T) {
	d := analysis.Discover(inputs())
	kinds := map[scan.TargetKind][]string{}
	for _, t := range d.Targets {
		kinds[t.Kind] = append(kinds[t.Kind], t.Path)
	}
	if len(kinds[scan.KindOneDrive]) != 1 || kinds[scan.KindOneDrive][0] != T.Root {
		t.Fatalf("dossier OneDrive personnalisé mal détecté : %v", kinds)
	}
	if len(kinds[scan.KindOrphan]) != 1 || !strings.Contains(kinds[scan.KindOrphan][0], "AncienneSociete") {
		t.Fatalf("ancien dossier non détecté : %v", kinds)
	}
	if len(kinds[scan.KindLegacy]) != 1 || kinds[scan.KindLegacy][0] != "/C/Users/jean/Documents" {
		t.Fatalf("ancien emplacement Documents non détecté : %v", kinds)
	}
	if len(d.OtherClouds) != 1 || d.OtherClouds[0].Provider != "Dropbox" {
		t.Fatalf("autres clouds : %v", d.OtherClouds)
	}
}

func TestNestedTargetsCountedOnce(t *testing.T) {
	in := inputs()
	in.OneDrive.Accounts[0].Libraries = []P.Library{{Name: "x", Path: T.Root + "/Vidéos"}}
	d := analysis.Discover(in)
	for _, t2 := range d.Targets {
		if t2.Path == T.Root+"/Vidéos" {
			t.Fatal("une cible imbriquée doit être ignorée")
		}
	}
	if len(d.Notes) == 0 {
		t.Fatal("une note doit expliquer l'exclusion")
	}
}

func diagnose(t *testing.T, in analysis.Inputs, fs P.FS) *analysis.Diagnosis {
	t.Helper()
	disc := analysis.Discover(in)
	opts := scan.DefaultOptions()
	opts.ComputerName = "MONPC"
	opts.Now = T.Recent.AddDate(0, 1, 0)
	res := scan.Run(context.Background(), fs, disc.Targets, opts, nil, nil)
	return analysis.Diagnose(analysis.Env{OneDrive: in.OneDrive, Known: in.Known, Volumes: in.Volumes,
		Discovery: disc, Same: T.Same, Windows: true, Placeholders: "actif"}, res)
}

func TestDiagnosisTotalsAndRecommendations(t *testing.T) {
	in := inputs()
	d := diagnose(t, in, T.Fixture())
	tot := d.Totals
	if tot.Local != int64(800+1200+1200+3+3+1200)*T.MB || tot.Pinned != 602*T.MB {
		t.Fatalf("totaux : %+v", tot)
	}
	if tot.Freeable != tot.Local+tot.Pinned {
		t.Fatal("libérable incohérent")
	}
	if tot.Orphan != 2000*T.MB || tot.Legacy != 150*T.MB {
		t.Fatalf("orphelin %d, ancien emplacement %d", tot.Orphan, tot.Legacy)
	}
	ids := map[string]analysis.Recommendation{}
	for _, r := range d.Recs {
		ids[strings.SplitN(r.ID, ":", 2)[0]] = r
		if r.Title == "" || r.Problem == "" || r.Action == "" || r.Prerequisites == "" || r.Verification == "" || r.RiskLabel == "" {
			t.Errorf("recommandation incomplète : %+v", r)
		}
	}
	free, ok := ids["liberer-local"]
	if !ok || !free.InApp || free.Gain != tot.Local || free.GainKind != analysis.GainEstimated || free.Risk != analysis.RiskLow {
		t.Fatalf("recommandation principale incorrecte : %+v", free)
	}
	if r := ids["ancien"]; r.Risk != analysis.RiskHigh || r.InApp || r.GainKind != analysis.GainPotential {
		t.Fatalf("un ancien dossier doit être à risque élevé, manuel, gain potentiel : %+v", r)
	}
	if _, ok := ids["conflits"]; !ok {
		t.Error("copies de conflit non signalées")
	}
	// Aucune promesse au-delà de ce qui est mesuré : les gains « réalisables
	// ici » ne dépassent jamais l'espace libérable mesuré.
	var inApp int64
	for _, r := range d.Recs {
		if r.InApp {
			inApp += r.Gain
		}
	}
	if inApp > tot.Freeable {
		t.Fatalf("gains promis %d > libérable mesuré %d", inApp, tot.Freeable)
	}
	// Les recommandations les plus sûres viennent avant les risquées.
	last := analysis.RiskNone
	for _, r := range d.Recs {
		if r.Gain == 0 || r.GainKind == analysis.GainNone {
			break
		}
		if r.Risk < last {
			t.Fatalf("ordre de risque non respecté : %s après un risque %v", r.Title, last)
		}
		last = r.Risk
	}
	if len(d.Causes) == 0 || d.Causes[0].Key != "local" {
		t.Fatalf("cause principale : %+v", d.Causes)
	}
	foundFull := false
	for _, al := range d.Alerts {
		if strings.Contains(al.Text, "presque plein") {
			foundFull = true
		}
	}
	if !foundFull {
		t.Error("disque presque plein non signalé")
	}
}

func TestFilesOnDemandDisabledDetected(t *testing.T) {
	fs := T.Fixture()
	for i := 0; i < 60; i++ {
		fs.AddFile(T.Root+"/Brut/f"+string(rune('a'+i%26))+string(rune('a'+i/26))+".dat", 20*T.MB, P.StateNotCloud, T.Old, "z")
	}
	// Dossier OneDrive dont quasiment tout est « non géré » :
	in := inputs()
	in.OneDrive.Accounts[0].UserFolder = T.Root + "/Brut"
	in.OneDrive.SyncRoots = nil
	in.OneDrive.EnvFolders = nil
	in.Known = nil
	d := diagnose(t, in, fs)
	found := false
	for _, r := range d.Recs {
		if strings.HasPrefix(r.ID, "fichiers-a-la-demande") {
			found = true
			if len(r.Steps) == 0 || r.InApp {
				t.Error("doit guider pas à pas dans les paramètres officiels")
			}
		}
	}
	if !found {
		t.Fatal("Fichiers à la demande désactivé non détecté")
	}
}

func TestPlan(t *testing.T) {
	d := diagnose(t, inputs(), T.Fixture())
	if len(d.Plan) != 3 {
		t.Fatal("plan attendu pour 50/100/200 Go")
	}
	for _, p := range d.Plan {
		if p.Reachable {
			t.Fatalf("avec ~5 Go libérables, %d Go ne doit pas être atteignable", p.Goal>>30)
		}
		if p.Note == "" {
			t.Error("explication manquante")
		}
	}
	if len(d.Folders) == 0 || d.Folders[0].Freeable <= 0 {
		t.Fatal("dossiers candidats manquants")
	}
}

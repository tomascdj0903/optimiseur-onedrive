package actions_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"optimiseur-onedrive/internal/actions"
	P "optimiseur-onedrive/internal/platform"
	"optimiseur-onedrive/internal/platform/simfs"
	T "optimiseur-onedrive/internal/testutil"
)

func manager(t *testing.T, fs *simfs.FS) *actions.Manager {
	return &actions.Manager{FS: fs, Same: T.Same, Roots: func() []string { return []string{T.Root} },
		OneDriveRunning: func() (bool, bool) { return true, true },
		DiskFree:        func(string) int64 { return 10 << 30 },
		JournalDir:      t.TempDir()}
}

func count(fs *simfs.FS, dir string) int {
	n := 0
	var rec func(string)
	rec = func(d string) {
		ents, _ := fs.ReadDir(d)
		for _, e := range ents {
			if e.IsLink() {
				continue
			}
			if e.IsDir {
				rec(filepath.Join(d, e.Name))
			} else {
				n++
			}
		}
	}
	rec(dir)
	return n
}

func TestReadOnlyRefusesEverything(t *testing.T) {
	fs := T.Fixture()
	m := manager(t, fs)
	m.ReadOnly = true
	if _, err := m.Prepare(context.Background(), actions.Free, []string{T.Root}); !errors.Is(err, actions.ErrReadOnly) {
		t.Fatalf("Prepare : %v", err)
	}
	if _, err := m.Execute(context.Background(), "x", "LIBERER", "", nil); !errors.Is(err, actions.ErrReadOnly) {
		t.Fatalf("Execute : %v", err)
	}
	if fs.SetCalls != 0 {
		t.Fatal("modification en lecture seule")
	}
}

func TestOutsideOneDriveRejected(t *testing.T) {
	fs := T.Fixture()
	m := manager(t, fs)
	pv, err := m.Prepare(context.Background(), actions.Free, []string{"/C/Users/jean/Documents", "relatif/x", "/D/Mon OneDrive (perso)2"})
	if !errors.Is(err, actions.ErrNothing) || len(pv.Rejected) != 3 {
		t.Fatalf("err=%v rejetés=%v", err, pv.Rejected)
	}
}

func TestConfirmationsAndTokens(t *testing.T) {
	fs := T.Fixture()
	m := manager(t, fs)
	ctx := context.Background()
	pv, err := m.Prepare(ctx, actions.Free, []string{T.Root + "/Vidéos"})
	if err != nil {
		t.Fatal(err)
	}
	if pv.HighRisk {
		t.Fatal("Vidéos ne contient pas de fichier épinglé : pas de risque élevé attendu")
	}
	if fs.SetCalls != 0 {
		t.Fatal("le récapitulatif ne doit rien modifier")
	}
	if _, err := m.Execute(ctx, pv.Token, "oui", "", nil); !errors.Is(err, actions.ErrNotConfirm) {
		t.Fatalf("mot de confirmation erroné accepté : %v", err)
	}
	if _, err := m.Execute(ctx, "inconnu", "LIBERER", "", nil); !errors.Is(err, actions.ErrBadToken) {
		t.Fatalf("jeton inconnu accepté : %v", err)
	}
	if fs.SetCalls != 0 {
		t.Fatal("modification malgré un refus")
	}
	if _, err := m.Execute(ctx, pv.Token, "liberer", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Execute(ctx, pv.Token, "LIBERER", "", nil); !errors.Is(err, actions.ErrBadToken) {
		t.Fatal("un récapitulatif ne doit servir qu'une fois")
	}
	// Expiration
	m.Now = func() time.Time { return time.Now().Add(time.Hour) }
	pv2, _ := m.Prepare(ctx, actions.Free, []string{T.Root + "/Documents"})
	m.Now = func() time.Time { return time.Now().Add(3 * time.Hour) }
	if _, err := m.Execute(ctx, pv2.Token, "LIBERER", "", nil); !errors.Is(err, actions.ErrBadToken) {
		t.Fatalf("jeton expiré accepté : %v", err)
	}
}

func TestPinnedRequiresSecondConfirmation(t *testing.T) {
	fs := T.Fixture()
	m := manager(t, fs)
	pv, err := m.Prepare(context.Background(), actions.Free, []string{T.Root + "/Archives"})
	if err != nil || !pv.HighRisk || pv.PinnedFiles != 2 {
		t.Fatalf("pv=%+v err=%v", pv, err)
	}
	if _, err := m.Execute(context.Background(), pv.Token, "LIBERER", "", nil); !errors.Is(err, actions.ErrRiskConfirm) {
		t.Fatalf("risque non confirmé accepté : %v", err)
	}
	if fs.SetCalls != 0 {
		t.Fatal("modifié sans confirmation du risque")
	}
}

func TestOneDriveStopped(t *testing.T) {
	fs := T.Fixture()
	m := manager(t, fs)
	m.OneDriveRunning = func() (bool, bool) { return false, true }
	pv, _ := m.Prepare(context.Background(), actions.Free, []string{T.Root + "/Vidéos"})
	if _, err := m.Execute(context.Background(), pv.Token, "LIBERER", "", nil); !errors.Is(err, actions.ErrOneDriveOff) {
		t.Fatalf("err=%v", err)
	}
}

func TestFreeVerifyAndRestore(t *testing.T) {
	fs := T.Fixture()
	fs.Locked[T.Root+"/Documents/rapport.pdf"] = true // ouvert dans un programme
	fs.DenySet[T.Root+"/Documents/rapport-MONPC.pdf"] = true
	m := manager(t, fs)
	ctx := context.Background()
	before := count(fs, T.Root)
	pv, err := m.Prepare(ctx, actions.Free, []string{T.Root})
	if err != nil {
		t.Fatal(err)
	}
	wantGain := int64(800+1200+1200+3+3+1200+600+2) * T.MB
	if pv.EstGain != wantGain {
		t.Fatalf("gain estimé %d, attendu %d", pv.EstGain, wantGain)
	}
	run, err := m.Execute(ctx, pv.Token, "LIBERER", "OUI", nil)
	if err != nil {
		t.Fatal(err)
	}
	if count(fs, T.Root) != before {
		t.Fatal("des fichiers ont disparu !")
	}
	if run.Errors < 2 { // rapport-MONPC (refus) + dossier Privé illisible
		t.Fatalf("erreurs attendues, obtenu %d : %v", run.Errors, run.ErrorSamples)
	}
	if fs.State(T.Root+"/Vidéos/mariage.mov") != P.StateOnline || fs.State(T.Root+"/Archives/projet.zip") != P.StateOnline {
		t.Fatal("fichiers non libérés")
	}
	if fs.State(T.Root+"/Documents/nouveau.txt") != P.StateNotCloud {
		t.Fatal("un fichier non géré a été modifié")
	}
	if fs.State(T.Root+"/Documents/rapport.pdf") != P.StateLocal {
		t.Fatal("le fichier verrouillé devrait rester local (OneDrive refuse)")
	}
	// Journal présent et exploitable
	b, err := os.ReadFile(run.Journal)
	if err != nil || !strings.Contains(string(b), `"run"`) {
		t.Fatalf("journal : %v", err)
	}
	v := m.VerifyRun(ctx, run)
	if v.StillLocal != 2 { // rapport.pdf (verrouillé) + rapport-MONPC.pdf (refusé)
		t.Fatalf("encore locaux = %d", v.StillLocal)
	}
	if v.Freed != wantGain-6*T.MB {
		t.Fatalf("libéré mesuré = %d, attendu %d", v.Freed, wantGain-6*T.MB)
	}
	// Restauration du réglage précédent
	rp, err := m.PrepareRestore(run.Journal)
	if err != nil || !rp.HighRisk {
		t.Fatalf("restauration : %+v %v", rp, err)
	}
	if _, err := m.Execute(ctx, rp.Token, "RESTAURER", "OUI", nil); err != nil {
		t.Fatal(err)
	}
	if fs.State(T.Root+"/Archives/projet.zip") != P.StatePinned {
		t.Fatal("le fichier épinglé doit retrouver « Toujours conserver »")
	}
	a, _, _ := fs.Attrs(T.Root + "/Vidéos/mariage.mov")
	if a&(P.AttrPinned|P.AttrUnpinned) != 0 {
		t.Fatalf("le fichier doit retrouver l'état standard, attributs %#x", a)
	}
	if count(fs, T.Root) != before {
		t.Fatal("des fichiers ont disparu après restauration")
	}
}

func TestSingleFileSelection(t *testing.T) {
	fs := T.Fixture()
	m := manager(t, fs)
	pv, err := m.Prepare(context.Background(), actions.Free, []string{T.Root + "/Vidéos/mariage.mov"})
	if err != nil || pv.Files != 1 || pv.EstGain != 1200*T.MB {
		t.Fatalf("pv=%+v err=%v", pv, err)
	}
	if _, err := m.Execute(context.Background(), pv.Token, "LIBERER", "", nil); err != nil {
		t.Fatal(err)
	}
	if fs.State(T.Root+"/Vidéos/copie mariage.mov") != P.StateLocal {
		t.Fatal("seul le fichier choisi doit changer")
	}
}

package scan_test

import (
	"context"
	"strings"
	"testing"

	P "optimiseur-onedrive/internal/platform"
	"optimiseur-onedrive/internal/scan"
	T "optimiseur-onedrive/internal/testutil"
)

func run(t *testing.T) *scan.Result {
	t.Helper()
	fs := T.Fixture()
	opts := scan.DefaultOptions()
	opts.ComputerName = "MONPC"
	opts.Now = T.Recent.AddDate(0, 1, 0)
	return scan.Run(context.Background(), fs, []scan.Target{{Path: T.Root, Kind: scan.KindOneDrive, Label: "OneDrive"}}, opts, &scan.Tracker{}, nil)
}

func TestFolderSizesAndStates(t *testing.T) {
	r := run(t)
	root := r.Targets[0].Root
	a := root.Agg
	// Fichiers comptés : 4 vidéos + 2 archives + 4 documents (Privé illisible, jonction ignorée)
	if a.Files != 10 {
		t.Fatalf("fichiers = %d, attendu 10", a.Files)
	}
	wantLocal := int64(800+1200+1200+3+3+1200) * T.MB
	if got := a.States[P.StateLocal].Alloc; got != wantLocal {
		t.Errorf("local = %d, attendu %d", got, wantLocal)
	}
	if got := a.States[P.StatePinned].Alloc; got != 602*T.MB {
		t.Errorf("épinglé = %d", got)
	}
	if a.States[P.StateOnline].Alloc != 0 || a.States[P.StateOnline].Logical != 900*T.MB {
		t.Errorf("en ligne : taille logique %d, disque %d", a.States[P.StateOnline].Logical, a.States[P.StateOnline].Alloc)
	}
	// La taille logique inclut le fichier en ligne, l'espace disque non.
	if a.Logical-a.Alloc != 900*T.MB {
		t.Errorf("écart logique/disque = %d, attendu 900 Mo", a.Logical-a.Alloc)
	}
	if a.Freeable() != wantLocal+602*T.MB {
		t.Errorf("libérable = %d", a.Freeable())
	}
	// Somme des enfants = parent
	var sum int64
	for _, c := range root.Children {
		sum += c.Agg.Alloc
	}
	if sum != a.Alloc {
		t.Errorf("somme enfants %d != parent %d", sum, a.Alloc)
	}
	// Dossiers triés par taille décroissante
	if root.Children[0].Name != "Vidéos" {
		t.Errorf("premier dossier = %s", root.Children[0].Name)
	}
	if r.Targets[0].Root.Children[0].Agg.OldLocalAlloc != 2000*T.MB {
		t.Errorf("anciens fichiers = %d", r.Targets[0].Root.Children[0].Agg.OldLocalAlloc)
	}
}

func TestAccessErrorIsReportedNotFatal(t *testing.T) {
	r := run(t)
	if r.ErrorCount != 1 || len(r.Errors) != 1 || !strings.Contains(r.Errors[0].Msg, "Accès refusé") {
		t.Fatalf("erreurs = %+v", r.Errors)
	}
	n, _ := r.Find(T.Root+"/Privé", T.Same)
	if n == nil || n.ErrMsg == "" {
		t.Fatal("le dossier illisible doit être signalé")
	}
}

func TestJunctionNotFollowed(t *testing.T) {
	r := run(t)
	n, _ := r.Find(T.Root+"/Lien vers Vidéos", T.Same)
	if n == nil || !n.Link {
		t.Fatal("la jonction doit apparaître comme lien")
	}
	if n.Agg.Files != 0 || r.Targets[0].Root.Agg.Links != 1 {
		t.Fatalf("la jonction a été suivie : %+v", n.Agg)
	}
}

func TestBigFilesAndConflicts(t *testing.T) {
	r := run(t)
	if len(r.Big) != 6 { // >= 50 Mo : 4 vidéos, zip, même taille.bin
		t.Fatalf("gros fichiers = %d", len(r.Big))
	}
	if r.Big[0].Alloc < r.Big[len(r.Big)-1].Alloc {
		t.Error("gros fichiers non triés")
	}
	if r.Targets[0].ConflictCount != 1 {
		t.Errorf("copies de conflit = %d", r.Targets[0].ConflictCount)
	}
}

func TestCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := scan.Run(ctx, T.Fixture(), []scan.Target{{Path: T.Root, Label: "x"}}, scan.DefaultOptions(), nil, nil)
	if !r.Canceled {
		t.Fatal("annulation non prise en compte")
	}
}

func TestMissingTarget(t *testing.T) {
	r := scan.Run(context.Background(), T.Fixture(), []scan.Target{{Path: "/E/absent", Label: "x"}}, scan.DefaultOptions(), nil, nil)
	if r.Targets[0].Exists || r.Targets[0].Err == "" {
		t.Fatal("cible absente non signalée")
	}
}

func TestIsWithin(t *testing.T) {
	same := func(a, b string) bool { return strings.EqualFold(a, b) }
	cases := []struct {
		p, root string
		want    bool
	}{
		{"/c/OneDrive/x", "/c/OneDrive", true},
		{"/C/ONEDRIVE/x", "/c/OneDrive", true},
		{"/c/OneDrive", "/c/OneDrive", true},
		{"/c/OneDrive2/x", "/c/OneDrive", false},
		{"/c", "/c/OneDrive", false},
	}
	for _, c := range cases {
		if got := scan.IsWithin(c.p, c.root, same); got != c.want {
			t.Errorf("IsWithin(%q,%q)=%v", c.p, c.root, got)
		}
	}
}

func TestConflictNames(t *testing.T) {
	ok := []string{"Rapport-MONPC.docx", "rapport-monpc-2.xlsx"}
	ko := []string{"MONPC.docx", "Rapport-MONPC-x.docx", "Rapport.docx", "Rapport-MONPCX.docx"}
	for _, n := range ok {
		if !scan.IsConflictCopy(n, "MONPC") {
			t.Errorf("%s devrait être reconnu", n)
		}
	}
	for _, n := range ko {
		if scan.IsConflictCopy(n, "MONPC") {
			t.Errorf("%s ne devrait pas être reconnu", n)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		attrs, tag uint32
		want       P.FileState
	}{
		{P.AttrArchive, 0, P.StateNotCloud},
		{P.AttrReparsePoint | P.AttrSparse, P.TagCloud | 0x1000, P.StateLocal},
		{P.AttrReparsePoint | P.AttrPinned, P.TagCloud, P.StatePinned},
		{P.AttrReparsePoint | P.AttrOffline | P.AttrRecallOnDataAccess | P.AttrUnpinned, P.TagCloud, P.StateOnline},
		{P.AttrRecallOnDataAccess, 0, P.StateOnline},                // espace réservé « déguisé »
		{P.AttrPinned | P.AttrRecallOnDataAccess, 0, P.StateOnline}, // téléchargement en attente
		{P.AttrOffline, 0, P.StateNotCloud},                         // ancien attribut hors ligne, pas OneDrive
	}
	for _, c := range cases {
		if got := P.Classify(c.attrs, c.tag); got != c.want {
			t.Errorf("Classify(%#x,%#x)=%v, attendu %v", c.attrs, c.tag, got, c.want)
		}
	}
	e := P.Entry{Attrs: P.AttrReparsePoint | P.AttrDirectory, ReparseTag: P.TagMountPoint}
	if !e.IsLink() {
		t.Error("jonction non reconnue")
	}
	e.ReparseTag = P.TagCloud
	if e.IsLink() {
		t.Error("un dossier cloud n'est pas un lien")
	}
}

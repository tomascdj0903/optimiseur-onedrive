package dupes_test

import (
	"context"
	"testing"

	"optimiseur-onedrive/internal/dupes"
	P "optimiseur-onedrive/internal/platform"
	"optimiseur-onedrive/internal/scan"
	T "optimiseur-onedrive/internal/testutil"
)

func TestDuplicatesBySHA256WithoutDownloading(t *testing.T) {
	fs := T.Fixture()
	// Copie exacte d'un fichier « en ligne uniquement » : ne doit jamais être lue.
	fs.AddFile(T.Root+"/Documents/en ligne.mp4", 900*T.MB, P.StateLocal, T.Old, "video-c")
	res := scan.Run(context.Background(), fs, []scan.Target{{Path: T.Root, Label: "x"}}, scan.DefaultOptions(), nil, nil)
	r := dupes.Find(context.Background(), fs, res.DupCands, &dupes.Tracker{})
	if fs.Hydrations != 0 {
		t.Fatalf("%d fichier(s) en ligne ont été téléchargés par la recherche", fs.Hydrations)
	}
	if !r.Complete {
		t.Fatal("recherche incomplète")
	}
	if len(r.Groups) != 1 {
		t.Fatalf("groupes = %d, attendu 1 (mariage.mov et sa copie)", len(r.Groups))
	}
	g := r.Groups[0]
	if len(g.Files) != 2 || !g.Verified || g.Gain != 1200*T.MB {
		t.Fatalf("groupe incorrect : %+v", g)
	}
	// « même taille.bin » a la même taille mais un contenu différent : exclu.
	for _, f := range g.Files {
		if f.Name == "même taille.bin" {
			t.Fatal("contenu différent compté comme doublon")
		}
	}
	if len(r.Unverified) != 1 {
		t.Fatalf("doublon non vérifiable (copie en ligne) attendu : %+v", r.Unverified)
	}
}

func TestDuplicatesCancel(t *testing.T) {
	fs := T.Fixture()
	res := scan.Run(context.Background(), fs, []scan.Target{{Path: T.Root, Label: "x"}}, scan.DefaultOptions(), nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := dupes.Find(ctx, fs, res.DupCands, nil)
	if r.Complete {
		t.Fatal("annulation ignorée")
	}
}

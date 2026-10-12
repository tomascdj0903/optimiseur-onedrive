// Package testutil fournit un jeu de données synthétique commun aux tests.
package testutil

import (
	"time"

	P "optimiseur-onedrive/internal/platform"
	"optimiseur-onedrive/internal/platform/simfs"
)

const MB = int64(1) << 20

var Old = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
var Recent = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

// Fixture : un dossier OneDrive « personnalisé » sur un autre disque, avec
// des fichiers dans tous les états, un dossier épinglé, une jonction, un
// dossier illisible, des doublons et un ancien dossier OneDrive orphelin.
func Fixture() *simfs.FS {
	f := simfs.New()
	root := "/D/Mon OneDrive (perso)"
	f.CloudDir(root, false)
	f.CloudDir(root+"/Vidéos", false)
	f.AddFile(root+"/Vidéos/vacances 2019.mp4", 800*MB, P.StateLocal, Old, "video-a")
	f.AddFile(root+"/Vidéos/mariage.mov", 1200*MB, P.StateLocal, Old, "video-b")
	f.AddFile(root+"/Vidéos/copie mariage.mov", 1200*MB, P.StateLocal, Recent, "video-b") // doublon exact
	f.AddFile(root+"/Vidéos/en ligne.mp4", 900*MB, P.StateOnline, Old, "video-c")
	f.CloudDir(root+"/Archives", true) // « Toujours conserver »
	f.AddFile(root+"/Archives/projet.zip", 600*MB, P.StatePinned, Old, "zip-a")
	f.AddFile(root+"/Archives/notes été.docx", 2*MB, P.StatePinned, Recent, "docx")
	f.CloudDir(root+"/Documents", false)
	f.AddFile(root+"/Documents/rapport.pdf", 3*MB, P.StateLocal, Recent, "pdf")
	f.AddFile(root+"/Documents/rapport-MONPC.pdf", 3*MB, P.StateLocal, Recent, "pdf2") // copie de conflit
	f.AddFile(root+"/Documents/nouveau.txt", 1*MB, P.StateNotCloud, Recent, "txt")
	f.AddFile(root+"/Documents/même taille.bin", 1200*MB, P.StateLocal, Recent, "autre-contenu")
	f.CloudDir(root+"/Privé", false)
	f.AddFile(root+"/Privé/secret.txt", 5*MB, P.StateLocal, Recent, "x")
	f.DenyRead["/D/Mon OneDrive (perso)/Privé"] = true
	f.AddJunction(root + "/Lien vers Vidéos")
	f.AddFile(root+"/Lien vers Vidéos/ne pas compter.mp4", 999*MB, P.StateLocal, Old, "loop")
	// Ancien dossier non relié à un compte.
	f.AddFile("/C/Users/jean/OneDrive - AncienneSociete/vieux.pst", 2000*MB, P.StateNotCloud, Old, "pst")
	f.AddFile("/C/Users/jean/Documents/oublié.docx", 150*MB, P.StateNotCloud, Old, "doc")
	return f
}

const Root = "/D/Mon OneDrive (perso)"

func Same(a, b string) bool { return a == b }

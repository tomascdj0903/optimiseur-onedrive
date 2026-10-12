//go:build dev

package main

// Fichier compilé uniquement avec « -tags dev » : permet de tester l'interface
// sur des données SIMULÉES. Il n'est jamais inclus dans l'exécutable livré.

import (
	"os"

	P "optimiseur-onedrive/internal/platform"
	T "optimiseur-onedrive/internal/testutil"
)

func init() {
	if os.Getenv("ODO_SIMULATION") == "1" {
		newPlatform = func() (P.FS, P.System) {
			fs := T.Fixture()
			fs.Locked[T.Root+"/Documents/rapport.pdf"] = true
			return fs, &T.FakeSystem{Running: true, Free: 21 << 30}
		}
	}
}

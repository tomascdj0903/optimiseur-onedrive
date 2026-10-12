// Optimiseur OneDrive — diagnostic et libération d'espace OneDrive, sans perte de données.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"optimiseur-onedrive/internal/actions"
	"optimiseur-onedrive/internal/analysis"
	"optimiseur-onedrive/internal/dupes"
	F "optimiseur-onedrive/internal/format"
	P "optimiseur-onedrive/internal/platform"
	"optimiseur-onedrive/internal/report"
	"optimiseur-onedrive/internal/scan"
	"optimiseur-onedrive/internal/share"
	"optimiseur-onedrive/internal/update"
)

// Renseignés à la compilation (-ldflags "-X main.version=…").
var (
	version    = "0.0.0-dev"
	shareURL   = "https://vnnmagstargwettizywm.supabase.co"
	shareKey   = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZSIsInJlZiI6InZubm1hZ3N0YXJnd2V0dGl6eXdtIiwicm9sZSI6ImFub24iLCJpYXQiOjE3OTE1Mzk5NzYsImV4cCI6MjEwNzExNTk3Nn0.agiiBLjwTdaJznJy-zQmy4eDEhvO7YHcXcdCgCBEl4w"
	updateRepo = "tomascdj0903/optimiseur-onedrive"
	assetName  = "OptimiseurOneDrive.exe"
)

// newPlatform peut être remplacé uniquement dans les builds de développement.
var newPlatform = func() (P.FS, P.System) { return P.NewFS(), P.NewSystem() }

type App struct {
	fs   P.FS
	sys  P.System
	in   *Input
	out  *Console
	same func(a, b string) bool

	share    *share.Client
	mask     *report.Masker
	readOnly bool
	dataDir  string
	exeDir   string
	phcm     string

	disc   analysis.Discovery
	env    analysis.Env
	res    *scan.Result
	diag   *analysis.Diagnosis
	dupRes *dupes.Result
	mgr    *actions.Manager

	selection []string
	lastRun   *actions.Run

	cancelMu sync.Mutex
	cancel   context.CancelFunc

	noShare bool
	mu      sync.Mutex // protège res/diag/dupRes/lastRun en mode graphique
	gui *guiState
}

func main() {
	diagOnly := flag.Bool("diagnostic", false, "analyse, rapport puis fermeture (aucune modification)")
	readOnly := flag.Bool("lecture-seule", false, "interdit toute modification")
	noUpdate := flag.Bool("sans-maj", false, "ne pas rechercher de mise à jour")
	noShare := flag.Bool("sans-partage", false, "ne pas proposer le partage en direct")
	textMode := flag.Bool("console", false, "interface texte uniquement (sans fenêtre graphique)")
	flag.Parse()

	color := P.SetupConsole("Optimiseur OneDrive " + version)
	fs, sys := newPlatform()
	a := &App{fs: fs, sys: sys, in: &Input{r: bufio.NewReader(os.Stdin)},
		out: &Console{w: os.Stdout, color: color}, readOnly: *readOnly || *diagOnly}
	a.same = a.sys.SamePath
	a.phcm = P.ExposePlaceholders()
	a.setupDirs()
	a.mask = report.NewMasker(false, a.sys.ProfileDir(), a.same)
	a.out.mask = a.mask
	a.handleSignals()

	a.banner()
	if exe, err := update.ExePath(); err == nil {
		update.Cleanup(exe)
		if !*noUpdate {
			a.checkUpdate(exe, !*textMode && !*diagOnly)
		}
	}
	a.noShare = *noShare
	a.mgr = &actions.Manager{FS: a.fs, Same: a.same, Roots: a.activeRoots, ReadOnly: a.readOnly,
		JournalDir: filepath.Join(a.dataDir, "operations"),
		OneDriveRunning: func() (bool, bool) {
			od := a.sys.OneDrive()
			return od.Running, od.RunningKnown
		},
		DiskFree: func(p string) int64 {
			v := filepath.VolumeName(p)
			if v == "" {
				v = "/"
			} else {
				v += `\`
			}
			f, _, _ := a.sys.DiskFree(v)
			return f
		}}

	if !*textMode && !*diagOnly {
		a.out.Blank()
		a.out.Ok("L'analyse est en LECTURE SEULE : rien n'est modifié sans votre confirmation écrite.")
		a.runGUI()
		return
	}
	a.intro(*diagOnly)
	if !*noShare {
		a.askShare()
	}
	if *diagOnly {
		a.Ask("Appuyez sur Entrée pour lancer l'analyse (lecture seule)…")
		a.runScan()
		a.showSummary()
		a.export(true)
		a.out.Blank()
		a.out.Ok("Diagnostic terminé. Vous pouvez fermer cette fenêtre.")
		a.Pause()
		a.quit(0)
	}
	a.consoleFlow()
}

// consoleFlow : parcours entièrement en mode texte.
func (a *App) consoleFlow() {
	a.Ask("Appuyez sur Entrée pour lancer l'analyse (lecture seule)…")
	a.runScan()
	a.showSummary()
	a.sendReport("diagnostic")
	a.menu()
}

func (a *App) setupDirs() {
	base := a.sys.LocalAppData()
	if base == "" {
		base = os.TempDir()
	}
	a.dataDir = filepath.Join(base, "OptimiseurOneDrive")
	os.MkdirAll(filepath.Join(a.dataDir, "journaux"), 0o755)
	f, err := os.OpenFile(filepath.Join(a.dataDir, "journaux", "session-"+time.Now().Format("2006-01-02_15-04-05")+".log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		a.out.logFile = f
	}
	if exe, err := update.ExePath(); err == nil {
		a.exeDir = filepath.Dir(exe)
	}
}

func (a *App) handleSignals() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	go func() {
		for range ch {
			a.cancelMu.Lock()
			c := a.cancel
			a.cancelMu.Unlock()
			if c != nil {
				a.out.Warn("Annulation demandée… (l'opération s'arrête proprement)")
				c()
				continue
			}
			a.out.Blank()
			a.quit(0)
		}
	}()
}

// withCancel exécute f avec un contexte annulable par Ctrl+C.
func (a *App) withCancel(f func(ctx context.Context)) {
	ctx, cancel := context.WithCancel(context.Background())
	a.cancelMu.Lock()
	a.cancel = cancel
	a.cancelMu.Unlock()
	defer func() {
		a.cancelMu.Lock()
		a.cancel = nil
		a.cancelMu.Unlock()
		cancel()
	}()
	f(ctx)
}

func (a *App) quit(code int) {
	if a.share.Enabled() {
		a.out.Dim("Envoi des dernières lignes du journal…")
		a.share.Close(5 * time.Second)
	}
	if a.out.logFile != nil {
		a.out.logFile.Close()
	}
	os.Exit(code)
}

func (a *App) banner() {
	a.out.Blank()
	a.out.Title("  ════════════════════════════════════════════════════════════════")
	a.out.Title("    OPTIMISEUR ONEDRIVE  ·  version %s", version)
	a.out.Title("    Comprendre et réduire l'espace occupé, sans perte de données")
	a.out.Title("  ════════════════════════════════════════════════════════════════")
	osi := a.sys.OSInfo()
	a.out.Dim("  %s %s (build %d) · %s", osi.Name, osi.Version, osi.Build, runtime.GOARCH)
}

func (a *App) checkUpdate(exe string, auto bool) {
	if strings.HasSuffix(version, "-dev") {
		return
	}
	a.out.Dim("  Recherche d'une mise à jour…")
	u := &update.Updater{Manifest: "https://raw.githubusercontent.com/" + updateRepo + "/main/release/latest.json", AssetName: assetName}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	rel, err := u.Latest(ctx)
	cancel()
	if err != nil {
		a.out.Dim("  Mise à jour : vérification impossible (%v). On continue avec cette version.", err)
		return
	}
	if !update.Newer(rel.Tag, version) {
		a.out.Dim("  Vous utilisez la dernière version.")
		return
	}
	a.out.Ok("  Nouvelle version disponible : %s (vous avez %s).", rel.Tag, version)
	if !auto && !a.YesNo("Installer la mise à jour maintenant ?", true) {
		return
	}
	var last time.Time
	err = func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		return u.Install(ctx, rel, exe, func(done, total int64) {
			if time.Since(last) > 200*time.Millisecond {
				last = time.Now()
				a.out.Progress(fmt.Sprintf("Téléchargement… %s / %s", F.Bytes(done), F.Bytes(total)))
			}
		})
	}()
	if err != nil {
		a.out.Err("  Mise à jour impossible : %v", err)
		a.out.Line("  On continue avec la version actuelle.")
		return
	}
	a.out.Ok("  Mise à jour installée et vérifiée (SHA-256). Redémarrage du programme…")
	if a.out.logFile != nil {
		a.out.logFile.Close()
	}
	signal.Ignore(os.Interrupt)
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		fmt.Println("Relancez le programme manuellement :", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func (a *App) intro(diagOnly bool) {
	a.out.Blank()
	a.out.Line("Ce programme va :")
	a.out.Line("  1. repérer vos dossiers OneDrive (même s'ils ont été déplacés) ;")
	a.out.Line("  2. mesurer ce qui occupe réellement votre disque, et pourquoi ;")
	a.out.Line("  3. vous proposer des solutions, de la plus sûre à la plus délicate.")
	a.out.Blank()
	a.out.Ok("L'analyse est en LECTURE SEULE : rien n'est supprimé, déplacé ou modifié.")
	if diagOnly || a.readOnly {
		a.out.Ok("Mode lecture seule : aucune modification ne sera possible pendant cette session.")
	} else {
		a.out.Line("Ensuite, rien ne sera modifié sans un récapitulatif et votre confirmation écrite.")
	}
	a.out.Dim("Ctrl+C annule une analyse en cours. Vos fichiers ne sont jamais lus ni envoyés.")
}

func (a *App) askShare() {
	if shareURL == "" || shareKey == "" {
		return
	}
	a.out.Blank()
	a.out.Title("Partage avec la personne qui vous aide")
	a.out.Line("Vous pouvez lui montrer en direct ce qui s'affiche dans cette fenêtre, ainsi que le rapport.")
	a.out.Line("Ce qui est envoyé : le texte affiché ici (tailles, noms de dossiers, réglages OneDrive).")
	a.out.Line("Ce qui n'est JAMAIS envoyé : le contenu de vos fichiers, vos mots de passe, vos identifiants.")
	if !a.YesNo("Activer le partage en direct ?", true) {
		a.out.Dim("Partage désactivé. Vous pourrez l'activer plus tard depuis le menu (touche P).")
		return
	}
	a.enableShare()
}

func (a *App) enableShare() {
	masked := a.YesNo("Masquer les noms de vos dossiers et fichiers dans ce partage ?", false)
	if err := a.startShare(masked); err != nil {
		a.out.Err("Le partage n'a pas pu démarrer (%v).", err)
		a.out.Line("Pas d'inquiétude : vous pourrez exporter le rapport dans un fichier (menu 8).")
	}
}

// startShare ouvre la session de partage (après accord de l'utilisateur).
func (a *App) startShare(masked bool) error {
	a.mask.Enabled = masked
	c := share.New(shareURL, shareKey)
	osi := a.sys.OSInfo()
	machine := a.sys.ComputerName()
	if masked {
		machine = "PC-" + c.Code
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	err := c.Start(ctx, version, machine, fmt.Sprintf("%s %s build %d", osi.Name, osi.Version, osi.Build), masked,
		map[string]any{"phcm": a.phcm, "readOnly": a.readOnly, "code": c.Code})
	cancel()
	if err != nil {
		return err
	}
	a.share = c
	a.out.share = c
	a.out.Ok("Partage activé. Code de session à communiquer : %s", a.out.C(cBold, c.Code))
	return nil
}

func (a *App) activeRoots() []string {
	var out []string
	for _, t := range a.disc.Targets {
		if t.Kind == scan.KindOneDrive || t.Kind == scan.KindLibrary {
			out = append(out, t.Path)
		}
	}
	return out
}

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"optimiseur-onedrive/internal/analysis"
	"optimiseur-onedrive/internal/dupes"
	F "optimiseur-onedrive/internal/format"
	P "optimiseur-onedrive/internal/platform"
	"optimiseur-onedrive/internal/report"
	"optimiseur-onedrive/internal/scan"
)

// ---------------------------------------------------------------- analyse

// detect : étape 1 (configuration OneDrive et cibles), journalisée dans la console.
func (a *App) detect() {
	a.out.Blank()
	a.out.Title("ÉTAPE 1 — Détection de la configuration OneDrive")
	od := a.sys.OneDrive()
	vols := a.sys.Volumes()
	known := a.sys.KnownFolders()
	a.out.Line("Application OneDrive : %s · version %s · %s", map[bool]string{true: "installée", false: "introuvable"}[od.Installed],
		orDash(od.Version), runningText(od))
	a.out.Line("Pilote Windows des fichiers cloud (CldFlt) : %s · espaces réservés : %s", od.FilterDriver, a.phcm)
	for _, acc := range od.Accounts {
		typ := "personnel"
		if acc.Business {
			typ = "professionnel/scolaire"
		}
		a.out.Line("Compte %s (%s) %s → %s", acc.Key, typ, a.mask.Email(acc.Email), pth(acc.UserFolder))
		for _, l := range acc.Libraries {
			a.out.Line("    bibliothèque synchronisée : %s", pth(l.Path))
		}
	}
	if len(od.Accounts) == 0 {
		a.out.Warn("Aucun compte OneDrive connecté n'a été trouvé.")
	}
	a.disc = analysis.Discover(analysis.Inputs{OneDrive: od, Known: known, Volumes: vols, Profile: a.sys.ProfileDir(),
		LocalAppData: a.sys.LocalAppData(), ReadDir: a.fs.ReadDir,
		Exists: func(p string) bool { _, _, err := a.fs.Attrs(p); return err == nil }, Same: a.same})
	for _, t := range a.disc.Targets {
		a.mask.AddRoot(t.Path, t.Label)
	}
	a.out.Line("Dossiers à analyser :")
	for _, t := range a.disc.Targets {
		a.out.Line("  • %s — %s", t.Label, pth(t.Path))
		a.out.Dim("      (%s ; détecté via %s)", report.KindLabel(t.Kind), t.Source)
	}
	for _, c := range a.disc.OtherClouds {
		a.out.Dim("  Autre service cloud présent (non analysé) : %s", c.Provider)
	}
	var recycle []P.RecycleInfo
	for _, v := range vols {
		if v.Type == "fixe" {
			recycle = append(recycle, a.sys.RecycleBin(v.Root))
		}
	}
	a.env = analysis.Env{AppVersion: version, OS: a.sys.OSInfo(), Volumes: vols, OneDrive: od, Placeholders: a.phcm,
		Known: known, Recycle: recycle, Discovery: a.disc, Same: a.same, Windows: a.sys.OSInfo().Windows}
	a.out.Blank()
	a.out.Title("ÉTAPE 2 — Mesure des dossiers (lecture seule)")
	a.out.Dim("Cela peut prendre quelques minutes pour des centaines de milliers de fichiers.")
}

// measure : étape 2, parcours en lecture seule (bloquant).
func (a *App) measure(ctx context.Context, tr *scan.Tracker) *scan.Result {
	opts := scan.DefaultOptions()
	opts.ComputerName = a.sys.ComputerName()
	end := a.sys.BeginBackground()
	defer end()
	return scan.Run(ctx, a.fs, a.disc.Targets, opts, tr, func(p string) string { return filepath.VolumeName(p) })
}

// finish : diagnostic à partir du résultat.
func (a *App) finish(r *scan.Result) {
	if r.Canceled {
		a.out.Warn("Analyse annulée : les résultats sont PARTIELS.")
	}
	a.out.Ok("Analyse terminée : %s fichiers et %s dossiers en %s ; %s élément(s) illisible(s).", F.Int(r.Files), F.Int(r.Dirs),
		F.Duration(r.Finished.Sub(r.Started)), F.Int(r.ErrorCount))
	d := analysis.Diagnose(a.env, r)
	a.mu.Lock()
	a.res, a.diag, a.dupRes = r, d, nil
	a.mu.Unlock()
}

func (a *App) runScan() {
	a.detect()
	tr := &scan.Tracker{}
	var res *scan.Result
	a.withCancel(func(ctx context.Context) {
		done := make(chan *scan.Result, 1)
		go func() { done <- a.measure(ctx, tr) }()
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		lastShared := time.Now()
		for {
			select {
			case r := <-done:
				res = r
				return
			case <-tick.C:
				p := tr.Snapshot()
				line := fmt.Sprintf("[%d/%d] %s fichiers · %s · %s erreur(s) · %s", p.TargetIndex, p.TargetCount,
					F.Int(p.Files), F.Bytes(p.Bytes), F.Int(p.Errors), F.Duration(time.Duration(p.Elapsed*float64(time.Second))))
				a.out.Progress(line + " · " + p.Path)
				if time.Since(lastShared) > 10*time.Second && a.share.Enabled() {
					lastShared = time.Now()
					a.share.Log("progression", line)
				}
			}
		}
	})
	a.finish(res)
}

func orDash(s string) string {
	if s == "" {
		return "inconnue"
	}
	return s
}

func runningText(od P.OneDriveInfo) string {
	if !od.RunningKnown {
		return "état inconnu"
	}
	if od.Running {
		return "en cours d'exécution"
	}
	return "ARRÊTÉE"
}

// ---------------------------------------------------------------- résumé

func (a *App) showSummary() {
	d := a.diag
	if d == nil {
		return
	}
	t := d.Totals
	a.out.Blank()
	a.out.Title("══ DIAGNOSTIC ══")
	for _, v := range d.Volumes {
		if v.Type != "fixe" || v.Total == 0 {
			continue
		}
		used := v.Total - v.Free
		label := v.Root
		if v.System {
			label += " (système)"
		}
		a.out.Line("Disque %-14s %s %s utilisés sur %s · %s libres", label, bar(used, v.Total, 24), F.Bytes(used), F.Bytes(v.Total), F.Bytes(v.Free))
	}
	a.out.Blank()
	a.out.Line("%s %s %s", a.out.C(cBold, "OneDrive occupe"), a.out.C(cBold+cCyan, F.Bytes(t.ActiveAlloc)), a.out.C(cBold, "sur le disque de ce PC (mesuré) :"))
	rows := []struct {
		label string
		v     int64
		col   string
	}{
		{"téléchargés, déjà dans le cloud (libérables)", t.Local, cGreen},
		{"« Toujours conserver sur cet appareil »", t.Pinned, cYellow},
		{"non gérés « à la demande »", t.NotCloud, cRed},
	}
	for _, r := range rows {
		a.out.Line("  %s %s %s", a.out.C(r.col, bar(r.v, t.ActiveAlloc, 20)), lpad(F.Bytes(r.v), 10), r.label)
	}
	a.out.Line("  %s %s fichier(s) « en ligne uniquement » : %s dans le cloud, ≈ 0 octet ici", a.out.C(cBlue, bar(0, 1, 20)),
		lpad(F.Int(t.OnlineFiles), 10), F.Bytes(t.Online))
	if t.Orphan+t.Legacy > 0 {
		a.out.Warn("  + %s dans d'anciens dossiers liés à OneDrive mais NON synchronisés", F.Bytes(t.Orphan+t.Legacy))
	}
	a.out.Blank()
	a.out.Title("Pourquoi autant d'espace ?")
	for i, c := range d.Causes {
		if i >= 4 {
			break
		}
		a.out.Line("  %d) %s — %s", i+1, a.out.C(cBold, c.Title), F.Bytes(c.Bytes))
		for _, l := range wrap(c.Explanation, 74) {
			a.out.Dim("     %s", l)
		}
	}
	if len(d.Causes) == 0 {
		a.out.Line("  Aucune donnée OneDrive locale significative.")
	}
	a.out.Blank()
	a.out.Est("Récupérable sans rien supprimer (estimation) : %s, dont %s immédiatement (fichiers non épinglés).",
		F.Bytes(t.Freeable), F.Bytes(t.Local))
	for _, al := range d.Alerts {
		switch al.Level {
		case "probleme":
			a.out.Err("  ✖ %s", al.Text)
		case "attention":
			a.out.Warn("  ⚠ %s", al.Text)
		default:
			a.out.Line("  ℹ %s", al.Text)
		}
	}
	a.out.Blank()
	a.out.Title("Recommandations (de la plus sûre à la plus délicate)")
	for i, r := range d.Recs {
		if i >= 6 {
			a.out.Dim("  … voir le détail complet avec le menu 2")
			break
		}
		app := ""
		if r.InApp {
			app = a.out.C(cGreen, " [faisable ici : menu 6]")
		}
		a.out.Line("  %d. %s%s", i+1, r.Title, app)
		a.out.Dim("     gain %s · risque %s", gainText(r), r.RiskLabel)
	}
	if len(d.Plan) > 0 {
		a.out.Blank()
		var parts []string
		for _, p := range d.Plan {
			s := F.Bytes(p.Goal) + " : "
			if p.Reachable {
				s += fmt.Sprintf("oui (%d dossier(s))", len(p.Folders))
			} else {
				s += "non"
			}
			parts = append(parts, s)
		}
		a.out.Line("Atteignable en libérant de l'espace OneDrive → %s", strings.Join(parts, " · "))
	}
}

func gainText(r analysis.Recommendation) string {
	switch r.GainKind {
	case analysis.GainNone:
		return "aucun direct"
	case analysis.GainPotential:
		return F.Bytes(r.Gain) + " (potentiel, après vérification)"
	case analysis.GainMeasured:
		return F.Bytes(r.Gain) + " (mesuré)"
	}
	return F.Bytes(r.Gain) + " (estimation)"
}

// ---------------------------------------------------------------- menu

func (a *App) menu() {
	for {
		a.out.Blank()
		a.out.Rule()
		sel := ""
		if len(a.selection) > 0 {
			sel = a.out.C(cGreen, fmt.Sprintf("  (sélection : %d élément(s))", len(a.selection)))
		}
		shareTxt := "désactivé"
		if a.share.Enabled() {
			sent, pending, errTxt := a.share.Status()
			shareTxt = fmt.Sprintf("actif, code %s (%d lignes envoyées", a.share.Code, sent)
			if pending > 0 {
				shareTxt += fmt.Sprintf(", %d en attente", pending)
			}
			shareTxt += ")"
			if errTxt != "" {
				shareTxt += " — connexion difficile"
			}
		}
		a.out.Title("MENU%s", sel)
		a.out.Line("  1  Résumé du diagnostic          6  Libérer de l'espace (choisir des dossiers)")
		a.out.Line("  2  Recommandations détaillées    7  Vérifier / annuler une opération passée")
		a.out.Line("  3  Explorer les dossiers         8  Exporter le rapport (fichier .txt)")
		a.out.Line("  4  Plus gros fichiers            9  Relancer l'analyse")
		a.out.Line("  5  Rechercher les doublons       A  Ouvrir l'Assistant Stockage de Windows")
		a.out.Line("  P  Partage en direct : %s", shareTxt)
		a.out.Line("  0  Quitter")
		switch strings.ToLower(a.Ask("Votre choix :")) {
		case "1":
			a.showSummary()
		case "2":
			a.showRecs()
		case "3":
			a.browse(false)
		case "4":
			a.bigFiles()
		case "5":
			a.findDupes()
		case "6":
			a.freeFlow()
		case "7":
			a.verifyFlow()
		case "8":
			a.export(false)
		case "9":
			a.runScan()
			a.showSummary()
			a.sendReport("diagnostic")
		case "a":
			if err := a.sys.OpenSettings("ms-settings:storagesense"); err != nil {
				a.out.Err("Impossible d'ouvrir les Paramètres : %v", err)
			} else {
				a.out.Ok("Paramètres Windows ouverts sur l'Assistant Stockage. Suivez les étapes de la recommandation correspondante (menu 2).")
			}
		case "p":
			if a.share.Enabled() {
				if a.YesNo("Désactiver le partage en direct ?", false) {
					a.share.Disable()
					a.out.share = nil
					a.out.Ok("Partage désactivé.")
				}
			} else {
				a.enableShare()
				if a.share.Enabled() {
					a.sendReport("diagnostic")
				}
			}
		case "0", "q":
			a.out.Line("Au revoir. Journal local : %s", pth(filepath.Join(a.dataDir, "journaux")))
			a.quit(0)
		default:
			a.out.Warn("Choix inconnu.")
		}
	}
}

// ---------------------------------------------------------------- recommandations

func (a *App) showRecs() {
	d := a.diag
	a.out.Blank()
	a.out.Title("RECOMMANDATIONS DÉTAILLÉES")
	for i, r := range d.Recs {
		a.out.Blank()
		a.out.Line("%s", a.out.C(cBold, fmt.Sprintf("%d) %s", i+1, r.Title)))
		field := func(label, text string) {
			if text == "" {
				return
			}
			lines := wrap(text, 62)
			a.out.Line("   %s %s", a.out.C(cDim, pad(label, 13)), lines[0])
			for _, l := range lines[1:] {
				a.out.Line("   %s %s", pad("", 13), l)
			}
		}
		field("Problème", r.Problem)
		field("Explication", r.Explanation)
		field("Action", r.Action)
		for j, s := range r.Steps {
			a.out.Line("   %s %d. %s", pad("", 13), j+1, s)
		}
		field("Gain", gainText(r))
		field("Conséquences", r.Consequences)
		field("Risque", r.RiskLabel)
		field("Conditions", r.Prerequisites)
		field("Vérification", r.Verification)
		if r.InApp {
			a.out.Ok("   → Réalisable directement ici : menu 6.")
		}
		if r.SettingsURI != "" {
			a.out.Ok("   → La touche A du menu ouvre directement ce réglage.")
		}
		for _, p := range r.Paths {
			if r.ID != "liberer-local" {
				a.out.Dim("   Emplacement : %s", pth(p))
			}
		}
		if i%3 == 2 && i < len(d.Recs)-1 {
			if strings.ToLower(a.Ask("Entrée pour la suite, Q pour revenir au menu :")) == "q" {
				return
			}
		}
	}
	a.out.Blank()
	a.out.Title("Limites de cette analyse")
	for _, l := range d.Limits {
		a.out.Dim("  • %s", l)
	}
}

// ---------------------------------------------------------------- navigateur

func pinText(n *scan.Node) string {
	switch n.PinState() {
	case "toujours_conserver":
		return "épinglé"
	case "en_ligne":
		return "en ligne"
	}
	return ""
}

// browse : navigation dans l'arborescence mesurée, avec sélection.
func (a *App) browse(forFree bool) bool {
	var stack []*scan.Node
	var targets []*scan.TargetResult
	for _, t := range a.res.Targets {
		if t.Root != nil {
			targets = append(targets, t)
		}
	}
	showAll := false
	for {
		a.out.Blank()
		var items []*scan.Node
		if len(stack) == 0 {
			a.out.Title("DOSSIERS ANALYSÉS")
			for _, t := range targets {
				items = append(items, t.Root)
			}
		} else {
			cur := stack[len(stack)-1]
			a.out.Title("%s", trunc(cur.Path, 90))
			a.out.Dim("Total sur le disque %s · libérable %s · %s fichiers", F.Bytes(cur.Agg.Alloc), F.Bytes(cur.Agg.Freeable()), F.Int(cur.Agg.Files))
			for _, c := range cur.Children {
				if showAll || c.Agg.Alloc >= 1<<20 || len(cur.Children) <= 30 {
					items = append(items, c)
				}
			}
		}
		limit := 30
		if showAll {
			limit = 500
		}
		a.out.Line("  %s  %s  %s  %s", lpad("N°", 3), lpad("Disque", 10), lpad("Libérable", 10), "Nom")
		for i, n := range items {
			if i >= limit {
				a.out.Dim("  … %d autres (tapez T pour tout afficher)", len(items)-limit)
				break
			}
			mark := "  "
			for _, s := range a.selection {
				if a.same(s, n.Path) {
					mark = a.out.C(cGreen, "✔ ")
				}
			}
			name := n.Name
			if len(stack) == 0 {
				name = n.Name + " — " + n.Path
			}
			extra := pinText(n)
			if n.Link {
				extra = "lien (non suivi)"
			}
			if n.ErrMsg != "" {
				extra = "illisible"
			}
			if extra != "" {
				extra = a.out.C(cYellow, " ["+extra+"]")
			}
			a.out.Line("%s%s  %s  %s  %s%s", mark, lpad(strconv.Itoa(i+1), 3), lpad(F.Bytes(n.Agg.Alloc), 10),
				lpad(F.Bytes(n.Agg.Freeable()), 10), pth(trunc(name, 60)), extra)
		}
		a.out.Dim("Commandes : N° = ouvrir · +N° (ex. +2 ou +1,3) = ajouter à la sélection · -N° = retirer")
		a.out.Dim("            R = remonter · E N° = ouvrir dans l'Explorateur · V = valider la sélection · Q = menu")
		cmd := strings.ToLower(a.Ask("Commande :"))
		switch {
		case cmd == "q" || cmd == "0":
			return false
		case cmd == "r" || cmd == "..":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			showAll = false
		case cmd == "t":
			showAll = true
		case cmd == "v":
			if len(a.selection) == 0 {
				a.out.Warn("La sélection est vide.")
				continue
			}
			a.prepareAndRun(a.selection)
			return true
		case strings.HasPrefix(cmd, "e"):
			if n := pick(items, strings.TrimSpace(cmd[1:])); n != nil {
				a.openPath(n.Path, false)
			}
		case strings.HasPrefix(cmd, "+"):
			for _, n := range pickMany(items, cmd[1:]) {
				a.addSel(n.Path)
			}
		case strings.HasPrefix(cmd, "-"):
			for _, n := range pickMany(items, cmd[1:]) {
				a.removeSel(n.Path)
			}
		default:
			n := pick(items, cmd)
			if n == nil {
				a.out.Warn("Commande inconnue.")
				continue
			}
			if n.Link {
				a.out.Warn("C'est un lien vers un autre emplacement : il n'est pas suivi (évite les doubles comptages).")
				continue
			}
			if len(n.Children) == 0 {
				a.out.Warn("Ce dossier ne contient pas de sous-dossier.")
				continue
			}
			stack = append(stack, n)
			showAll = false
		}
	}
}

func pick(items []*scan.Node, s string) *scan.Node {
	i, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || i < 1 || i > len(items) {
		return nil
	}
	return items[i-1]
}

func pickMany(items []*scan.Node, s string) []*scan.Node {
	var out []*scan.Node
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		if n := pick(items, p); n != nil {
			out = append(out, n)
		}
	}
	return out
}

func (a *App) addSel(p string) {
	if !a.isActivePath(p) {
		a.out.Warn("« %s » n'est pas dans un dossier OneDrive synchronisé : la libération d'espace ne s'y applique pas.", pth(p))
		return
	}
	for _, s := range a.selection {
		if a.same(s, p) {
			return
		}
	}
	a.selection = append(a.selection, p)
	a.out.Ok("Ajouté : %s", pth(p))
}

func (a *App) removeSel(p string) {
	for i, s := range a.selection {
		if a.same(s, p) {
			a.selection = append(a.selection[:i], a.selection[i+1:]...)
			a.out.Line("Retiré : %s", pth(p))
			return
		}
	}
}

func (a *App) isActivePath(p string) bool {
	for _, r := range a.activeRoots() {
		if scan.IsWithin(p, r, a.same) {
			return true
		}
	}
	return false
}

func (a *App) openPath(p string, sel bool) {
	if err := a.sys.OpenInExplorer(p, sel); err != nil {
		a.out.Err("Impossible d'ouvrir l'Explorateur : %v", err)
	} else {
		a.out.Ok("Ouvert dans l'Explorateur : %s", pth(p))
	}
}

// ---------------------------------------------------------------- gros fichiers

func (a *App) bigFiles() {
	files := a.res.Big
	if len(files) == 0 {
		a.out.Line("Aucun fichier de plus de 50 Mo.")
		return
	}
	order := "disque"
	for {
		list := append([]scan.FileRec(nil), files...)
		switch order {
		case "date":
			sort.Slice(list, func(i, j int) bool { return list[i].ModTime.Before(list[j].ModTime) })
		case "taille":
			sort.Slice(list, func(i, j int) bool { return list[i].Size > list[j].Size })
		}
		a.out.Blank()
		a.out.Title("PLUS GROS FICHIERS (tri : %s)", order)
		a.out.Line("  %s  %s  %s  %s  %s", lpad("N°", 3), lpad("Disque", 9), lpad("Taille", 9), pad("État", 22), "Modifié     Fichier")
		n := 30
		if len(list) < n {
			n = len(list)
		}
		for i := 0; i < n; i++ {
			f := list[i]
			a.out.Line("  %s  %s  %s  %s  %s  %s", lpad(strconv.Itoa(i+1), 3), lpad(F.Bytes(f.Alloc), 9), lpad(F.Bytes(f.Size), 9),
				pad(report.StateLabel(f.State), 22), F.Date(f.ModTime), pth(trunc(f.Path, 70)))
		}
		a.out.Dim("Commandes : D/T/M = trier par disque/taille/date · +N° = ajouter à la sélection · E N° = montrer dans l'Explorateur · Q = menu")
		cmd := strings.ToLower(a.Ask("Commande :"))
		switch {
		case cmd == "q" || cmd == "":
			return
		case cmd == "d":
			order = "disque"
		case cmd == "t":
			order = "taille"
		case cmd == "m":
			order = "date"
		case strings.HasPrefix(cmd, "e"):
			if i, err := strconv.Atoi(strings.TrimSpace(cmd[1:])); err == nil && i >= 1 && i <= n {
				a.openPath(list[i-1].Path, true)
			}
		case strings.HasPrefix(cmd, "+"):
			for _, p := range strings.FieldsFunc(cmd[1:], func(r rune) bool { return r == ',' || r == ' ' }) {
				if i, err := strconv.Atoi(p); err == nil && i >= 1 && i <= n {
					a.addSel(list[i-1].Path)
				}
			}
		}
	}
}

// ---------------------------------------------------------------- doublons

func (a *App) findDupes() {
	a.out.Blank()
	a.out.Title("RECHERCHE DE DOUBLONS")
	a.out.Line("Les fichiers de plus de 1 Mo sont comparés par taille puis par empreinte SHA-256 (contenu identique au bit près).")
	a.out.Line("Les fichiers « en ligne uniquement » ne sont jamais lus : cela les téléchargerait.")
	a.out.Line("Cette recherche lit le contenu des fichiers locaux : elle peut prendre du temps. Rien n'est modifié.")
	if !a.YesNo("Lancer la recherche ?", true) {
		return
	}
	tr := &dupes.Tracker{}
	a.withCancel(func(ctx context.Context) {
		done := make(chan *dupes.Result, 1)
		go func() {
			end := a.sys.BeginBackground()
			defer end()
			done <- dupes.Find(ctx, a.fs, a.res.DupCands, tr)
		}()
		tick := time.NewTicker(300 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case r := <-done:
				a.dupRes = r
				return
			case <-tick.C:
				p := tr.Snapshot()
				a.out.Progress(fmt.Sprintf("%s : %s / %s fichiers · %s lus", p.Phase, F.Int(p.Done), F.Int(p.Total), F.Bytes(p.BytesRead)))
			}
		}
	})
	r := a.dupRes
	if !r.Complete {
		a.out.Warn("Recherche interrompue : résultats partiels.")
	}
	a.diag = withDupes(a, r)
	a.out.Ok("%d groupe(s) de doublons confirmés · %s d'espace local redondant · %d groupe(s) non vérifiables.", len(r.Groups), F.Bytes(r.Gain), len(r.Unverified))
	type ref struct{ g, f int }
	for {
		var refs []ref
		a.out.Blank()
		for gi, g := range r.Groups {
			if gi >= 15 {
				a.out.Dim("… %d autres groupes (voir le rapport exporté)", len(r.Groups)-15)
				break
			}
			a.out.Line("%s", a.out.C(cBold, fmt.Sprintf("Groupe %d — %d copies de %s — gain possible %s", gi+1, len(g.Files), F.Bytes(g.Size), F.Bytes(g.Gain))))
			for fi, f := range g.Files {
				refs = append(refs, ref{gi, fi})
				where := "hors OneDrive"
				if a.isActivePath(f.Path) {
					where = "OneDrive"
				}
				a.out.Line("   %s  %s  %-22s %-13s %s", lpad(fmt.Sprintf("%d.%d", gi+1, fi+1), 5), F.Date(f.ModTime), report.StateLabel(f.State), where, pth(trunc(f.Path, 64)))
			}
		}
		if len(r.Groups) == 0 {
			return
		}
		a.out.Dim("Rien n'est supprimé automatiquement. Option sûre : ajouter une copie située dans OneDrive à la sélection")
		a.out.Dim("pour libérer son espace local (elle reste en ligne). Commandes : +G.F (ex. +1.2) · E G.F · Q = menu")
		cmd := strings.ToLower(a.Ask("Commande :"))
		if cmd == "q" || cmd == "" {
			return
		}
		openMode := strings.HasPrefix(cmd, "e")
		addMode := strings.HasPrefix(cmd, "+")
		arg := strings.TrimSpace(strings.TrimLeft(cmd, "e+"))
		parts := strings.SplitN(arg, ".", 2)
		if len(parts) != 2 {
			a.out.Warn("Format attendu : numéro de groupe, point, numéro de fichier (ex. 1.2).")
			continue
		}
		gi, e1 := strconv.Atoi(parts[0])
		fi, e2 := strconv.Atoi(parts[1])
		if e1 != nil || e2 != nil || gi < 1 || gi > len(r.Groups) || fi < 1 || fi > len(r.Groups[gi-1].Files) {
			a.out.Warn("Numéro inconnu.")
			continue
		}
		f := r.Groups[gi-1].Files[fi-1]
		if openMode {
			a.openPath(f.Path, true)
		} else if addMode {
			a.addSel(f.Path)
		}
	}
}

func withDupes(a *App, r *dupes.Result) *analysis.Diagnosis {
	d := analysis.Diagnose(a.env, a.res)
	d.Dupes = &analysis.DupSummary{Groups: len(r.Groups), Gain: r.Gain, Unverified: len(r.Unverified), Hashed: r.Hashed, Complete: r.Complete}
	d.Recs = analysis.Recommend(d, a.env) // les recommandations tiennent compte des doublons
	return d
}

// ---------------------------------------------------------------- export / partage

func (a *App) reportText() string { return report.Text(a.diag, a.res, a.dupRes, a.mask) }

func (a *App) export(auto bool) {
	if a.diag == nil {
		return
	}
	txt := a.reportText()
	name := "Diagnostic-OneDrive-" + time.Now().Format("2006-01-02_15h04") + ".txt"
	var saved string
	for _, dir := range []string{a.exeDir, a.dataDir, os.TempDir()} {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, name)
		// BOM UTF-8 pour un affichage correct des accents dans le Bloc-notes.
		if err := os.WriteFile(p, append([]byte{0xEF, 0xBB, 0xBF}, []byte(strings.ReplaceAll(txt, "\n", "\r\n"))...), 0o644); err == nil {
			saved = p
			break
		}
	}
	if saved == "" {
		a.out.Err("Impossible d'enregistrer le rapport sur le disque.")
	} else {
		a.out.Ok("Rapport enregistré : %s", pth(saved))
		if err := a.sys.OpenInExplorer(saved, true); err == nil {
			a.out.Dim("Le dossier s'est ouvert avec le fichier sélectionné : vous pouvez l'envoyer par e-mail.")
		}
	}
	a.sendReport("rapport")
	_ = auto
}

func (a *App) sendReport(kind string) {
	if !a.share.Enabled() || a.diag == nil {
		return
	}
	an := report.BuildAnnex(a.diag, a.res, a.dupRes, a.mask)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := a.share.Report(ctx, kind, a.reportText(), an); err != nil {
		a.out.Warn("Le rapport n'a pas pu être envoyé (%v). Utilisez le menu 8 pour l'enregistrer dans un fichier.", err)
		return
	}
	a.out.Ok("Rapport transmis à la personne qui vous aide (code %s).", a.share.Code)
}

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"optimiseur-onedrive/internal/actions"
	F "optimiseur-onedrive/internal/format"
)

// freeFlow : choix des dossiers à rendre « disponibles uniquement en ligne ».
func (a *App) freeFlow() {
	if a.readOnly {
		a.out.Warn("Le programme a été lancé en lecture seule : aucune modification n'est possible.")
		return
	}
	d := a.diag
	a.out.Blank()
	a.out.Title("LIBÉRER DE L'ESPACE — sans rien supprimer")
	a.out.Line("« Libérer de l'espace » n'est PAS une suppression :")
	a.out.Line("  • le fichier reste visible dans l'Explorateur, avec une icône de nuage ;")
	a.out.Line("  • il reste intact dans votre OneDrive en ligne ;")
	a.out.Line("  • seule la copie locale est retirée ; il se retélécharge quand vous l'ouvrez (Internet nécessaire).")
	a.out.Line("Méthode : le réglage officiel OneDrive « Libérer de l'espace » (état « en ligne uniquement »).")
	if d.Totals.Freeable == 0 {
		a.out.Warn("Aucun fichier OneDrive téléchargé n'a été mesuré : il n'y a rien à libérer de cette façon.")
		a.out.Line("Consultez les autres recommandations (menu 2).")
		return
	}
	if od := a.sys.OneDrive(); od.RunningKnown && !od.Running {
		a.out.Err("OneDrive n'est pas démarré. Lancez OneDrive (menu Démarrer > OneDrive), attendez qu'il soit connecté, puis revenez ici.")
		return
	}
	for {
		a.out.Blank()
		a.out.Title("Dossiers suggérés (là où l'espace libérable est concentré)")
		n := len(d.Folders)
		if n > 20 {
			n = 20
		}
		for i := 0; i < n; i++ {
			f := d.Folders[i]
			mark := "  "
			for _, s := range a.selection {
				if a.same(s, f.Path) {
					mark = a.out.C(cGreen, "✔ ")
				}
			}
			info := ""
			if f.Pinned > 0 {
				info += a.out.C(cYellow, " [dont "+F.Bytes(f.Pinned)+" épinglés]")
			}
			if f.Old > 0 {
				info += a.out.C(cDim, " ["+F.Bytes(f.Old)+" non modifiés depuis 1 an]")
			}
			a.out.Line("%s%s  %s  %s%s", mark, lpad(strconv.Itoa(i+1), 3), lpad(F.Bytes(f.Freeable), 10), pth(trunc(f.Path, 64)), info)
		}
		for _, p := range d.Plan {
			if p.Reachable {
				a.out.Est("  P%d : sélection proposée pour récupérer %s → %d dossier(s), ~%s", p.Goal>>30, F.Bytes(p.Goal), len(p.Folders), F.Bytes(p.Gain))
			}
		}
		a.out.Dim("Commandes : +N° (ex. +1,4) = ajouter · -N° = retirer · P50 / P100 / P200 = sélection proposée")
		a.out.Dim("            T = choisir dans toute l'arborescence · S = voir la sélection · V = valider · Q = menu")
		cmd := strings.ToLower(strings.TrimSpace(a.Ask("Commande :")))
		switch {
		case cmd == "q" || cmd == "0":
			return
		case cmd == "t":
			if a.browse(true) {
				return
			}
		case cmd == "s":
			a.showSelection()
		case cmd == "v":
			if len(a.selection) == 0 {
				a.out.Warn("La sélection est vide.")
				continue
			}
			a.prepareAndRun(a.selection)
			return
		case strings.HasPrefix(cmd, "p"):
			g, err := strconv.Atoi(strings.TrimPrefix(cmd, "p"))
			if err != nil {
				a.out.Warn("Exemple : P50")
				continue
			}
			found := false
			for _, p := range d.Plan {
				if p.Goal == F.GB(int64(g)) && p.Reachable {
					found = true
					for _, f := range p.Folders {
						a.addSel(f)
					}
				}
			}
			if !found {
				a.out.Warn("Objectif non atteignable ou inconnu (P50, P100 ou P200).")
			}
		case strings.HasPrefix(cmd, "+") || strings.HasPrefix(cmd, "-"):
			for _, p := range strings.FieldsFunc(cmd[1:], func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
				i, err := strconv.Atoi(p)
				if err != nil || i < 1 || i > n {
					continue
				}
				if cmd[0] == '+' {
					a.addSel(d.Folders[i-1].Path)
				} else {
					a.removeSel(d.Folders[i-1].Path)
				}
			}
		default:
			a.out.Warn("Commande inconnue.")
		}
	}
}

func (a *App) showSelection() {
	if len(a.selection) == 0 {
		a.out.Line("Sélection vide.")
		return
	}
	a.out.Line("Sélection actuelle :")
	for i, p := range a.selection {
		est := ""
		if n, _ := a.res.Find(p, a.same); n != nil {
			est = F.Bytes(n.Agg.Freeable())
		}
		a.out.Line("  %d. %s  %s", i+1, lpad(est, 10), pth(p))
	}
}

// prepareAndRun : récapitulatif, double confirmation, exécution, vérification.
func (a *App) prepareAndRun(paths []string) {
	a.out.Blank()
	a.out.Line("Préparation du récapitulatif (relecture du disque, aucune modification)…")
	var pv *actions.Preview
	var err error
	a.withCancel(func(ctx context.Context) { pv, err = a.mgr.Prepare(ctx, actions.Free, paths) })
	if err != nil {
		a.out.Err("Opération impossible : %v", err)
		if pv != nil {
			for _, r := range pv.Rejected {
				a.out.Warn("  refusé : %s", r)
			}
		}
		return
	}
	a.out.Blank()
	a.out.Title("RÉCAPITULATIF — rien n'a encore été modifié")
	a.out.Line("Opération : rendre « disponibles uniquement en ligne » (Libérer de l'espace)")
	for _, p := range pv.Paths {
		a.out.Line("  • %s", pth(p))
	}
	for _, r := range pv.Rejected {
		a.out.Warn("  exclu : %s", r)
	}
	a.out.Line("Fichiers concernés : %s (dont %s déjà en ligne, %s épinglé(s), %s non géré(s) et ignoré(s))",
		F.Int(pv.Files), F.Int(pv.OnlineFiles), F.Int(pv.PinnedFiles), F.Int(pv.NotCloud))
	a.out.Est("Espace local qui devrait être libéré : %s (estimation ; OneDrive peut garder des fichiers ouverts ou non synchronisés)", F.Bytes(pv.EstGain))
	a.out.Line("Ce qui ne change pas : aucun fichier supprimé, déplacé ou renommé ; tout reste dans OneDrive en ligne.")
	a.out.Line("Conséquence : ces fichiers nécessiteront Internet pour être ouverts tant qu'ils ne sont pas retéléchargés.")
	a.out.Line("Retour possible : le menu 7 remet le réglage d'avant. Les fichiers « toujours conservés » sont alors")
	a.out.Line("retéléchargés ; les autres le seront simplement à leur prochaine ouverture.")
	for _, w := range pv.Warnings {
		a.out.Warn("⚠ %s", w)
	}
	typed := a.Ask(fmt.Sprintf("Pour appliquer, tapez %s (ou Entrée pour annuler) :", a.out.C(cBold, pv.ConfirmWord)))
	if strings.ToUpper(strings.TrimSpace(typed)) != pv.ConfirmWord {
		a.out.Ok("Annulé : rien n'a été modifié.")
		return
	}
	risk := ""
	if pv.HighRisk {
		a.out.Warn("ATTENTION : cette opération comporte un risque particulier (voir les avertissements ci-dessus).")
		risk = a.Ask("Confirmez-vous en connaissance de cause ? Tapez OUI :")
	}
	var run *actions.Run
	a.withCancel(func(ctx context.Context) {
		var last time.Time
		run, err = a.mgr.Execute(ctx, pv.Token, typed, risk, func(r *actions.Run) {
			if time.Since(last) > 250*time.Millisecond {
				last = time.Now()
				a.out.Progress(fmt.Sprintf("Application… %s fichiers traités · %s modifiés · %s erreur(s)", F.Int(r.Seen), F.Int(r.Changed), F.Int(r.Errors)))
			}
		})
	})
	if err != nil {
		a.out.Err("%v", err)
		return
	}
	a.lastRun = run
	a.selection = nil
	a.showRun(run)
	a.out.Line("OneDrive libère l'espace en arrière-plan : cela peut prendre de quelques secondes à quelques minutes.")
	if a.YesNo("Vérifier maintenant le résultat ?", true) {
		a.verify(run)
	}
}

func (a *App) showRun(run *actions.Run) {
	if run.Canceled {
		a.out.Warn("Opération interrompue : les éléments déjà traités restent dans leur nouvel état (rien n'est perdu).")
	}
	a.out.Ok("Terminé : %s fichier(s) examiné(s) · %s réglé(s) sur « en ligne uniquement » · %s déjà en ligne · %s non géré(s)",
		F.Int(run.Seen), F.Int(run.Changed), F.Int(run.Already), F.Int(run.NotCloud))
	if run.Errors > 0 {
		a.out.Warn("%s élément(s) n'ont pas pu être modifiés (fichiers verrouillés ou accès refusé) :", F.Int(run.Errors))
		for i, e := range run.ErrorSamples {
			if i >= 8 {
				break
			}
			a.out.Dim("   %s", e)
		}
	}
	a.out.Dim("Journal de l'opération : %s", pth(run.Journal))
}

func (a *App) verify(run *actions.Run) {
	for {
		var v *actions.Verify
		a.withCancel(func(ctx context.Context) {
			a.out.Line("Mesure de l'état réel des fichiers traités…")
			v = a.mgr.VerifyRun(ctx, run)
		})
		a.out.Blank()
		a.out.Title("VÉRIFICATION")
		a.out.Line("Fichiers : %s · en ligne uniquement maintenant : %s · encore présents localement : %s (%s)",
			F.Int(v.Files), F.Int(v.Online), F.Int(v.StillLocal), F.Bytes(v.StillLocalSize))
		if run.Kind == actions.Free {
			a.out.Ok("Espace libéré par ces fichiers (mesuré) : %s sur %s attendus", F.Bytes(v.Freed), F.Bytes(run.LocalBefore))
		}
		var vols []string
		for vol := range v.DiskDelta {
			vols = append(vols, vol)
		}
		sort.Strings(vols)
		for _, vol := range vols {
			a.out.Line("Espace libre du disque %s : %s (variation %s depuis l'opération ; inclut l'activité des autres programmes)",
				vol, F.Bytes(v.DiskNow[vol]), F.Bytes(v.DiskDelta[vol]))
		}
		if v.StillLocal > 0 && run.Kind == actions.Free {
			a.out.Warn("Certains fichiers sont encore locaux. Causes possibles : OneDrive n'a pas fini, fichier ouvert dans un")
			a.out.Warn("programme, fichier pas encore synchronisé, ou synchronisation en pause. Réessayez dans quelques minutes.")
			for i, e := range v.Examples {
				if i >= 5 {
					break
				}
				a.out.Dim("   exemple : %s", pth(e))
			}
		}
		a.share.Nudge()
		if !a.YesNo("Vérifier à nouveau ?", false) {
			return
		}
	}
}

// verifyFlow : liste des opérations passées, vérification ou restauration.
func (a *App) verifyFlow() {
	dir := filepath.Join(a.dataDir, "operations")
	js := actions.ListJournals(dir)
	if len(js) == 0 {
		a.out.Line("Aucune opération n'a encore été réalisée sur cet ordinateur.")
		return
	}
	a.out.Blank()
	a.out.Title("OPÉRATIONS PASSÉES")
	var runs []*actions.Run
	for _, j := range js {
		r, err := actions.RunFromJournal(j)
		if err != nil {
			continue
		}
		runs = append(runs, r)
		if len(runs) > 15 {
			break
		}
	}
	for i, r := range runs {
		first := ""
		if len(r.Paths) > 0 {
			first = r.Paths[0]
		}
		more := ""
		if len(r.Paths) > 1 {
			more = fmt.Sprintf(" (+%d)", len(r.Paths)-1)
		}
		a.out.Line("  %d. %s  %-10s %s%s", i+1, r.Started.Local().Format("02/01 15:04"), r.Kind, pth(trunc(first, 60)), more)
	}
	s := a.Ask("Numéro de l'opération (Entrée pour revenir) :")
	i, err := strconv.Atoi(s)
	if err != nil || i < 1 || i > len(runs) {
		return
	}
	r := runs[i-1]
	if a.lastRun != nil && a.lastRun.ID == r.ID {
		r = a.lastRun // contient les mesures « avant »
	}
	a.out.Line("  V = vérifier l'état actuel · R = remettre le réglage d'avant · Entrée = revenir")
	switch strings.ToLower(a.Ask("Choix :")) {
	case "v":
		a.verify(r)
	case "r":
		a.restore(r)
	}
}

func (a *App) restore(r *actions.Run) {
	if a.readOnly {
		a.out.Warn("Lecture seule : impossible.")
		return
	}
	pv, err := a.mgr.PrepareRestore(r.Journal)
	if err != nil {
		a.out.Err("Restauration impossible : %v", err)
		return
	}
	a.out.Title("RÉCAPITULATIF DE RESTAURATION")
	a.out.Line("%s éléments retrouveront leur réglage d'avant l'opération du %s.", F.Int(pv.Files), r.Started.Local().Format("02/01/2006 15:04"))
	a.out.Line("Limite : cela remet le RÉGLAGE. Les fichiers qui étaient « toujours conservés » seront retéléchargés par OneDrive")
	a.out.Line("(Internet et espace disque nécessaires). Les éléments modifiés depuis par vous ne sont pas touchés.")
	for _, w := range pv.Warnings {
		a.out.Warn("⚠ %s", w)
	}
	typed := a.Ask(fmt.Sprintf("Tapez %s pour confirmer (Entrée pour annuler) :", pv.ConfirmWord))
	if strings.ToUpper(strings.TrimSpace(typed)) != pv.ConfirmWord {
		a.out.Ok("Annulé : rien n'a été modifié.")
		return
	}
	risk := ""
	if pv.HighRisk {
		risk = a.Ask("Des fichiers vont être retéléchargés. Tapez OUI pour confirmer :")
	}
	var run *actions.Run
	a.withCancel(func(ctx context.Context) {
		run, err = a.mgr.Execute(ctx, pv.Token, typed, risk, nil)
	})
	if err != nil {
		a.out.Err("%v", err)
		return
	}
	a.out.Ok("Restauration terminée : %s élément(s) remis dans leur état précédent, %s erreur(s).", F.Int(run.Changed), F.Int(run.Errors))
	a.out.Dim("Journal : %s", pth(run.Journal))
}

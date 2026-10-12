package analysis

import (
	"fmt"
	"sort"
	"strings"
	"time"

	F "optimiseur-onedrive/internal/format"
	P "optimiseur-onedrive/internal/platform"
	"optimiseur-onedrive/internal/scan"
)

// GainKind précise la fiabilité d'un gain affiché.
const (
	GainMeasured  = "mesuré"     // somme d'espaces disque mesurés
	GainEstimated = "estimation" // calculé mais dépend du comportement de OneDrive
	GainPotential = "potentiel"  // seulement si l'utilisateur décide après vérification
	GainNone      = "aucun"
)

type Risk int

const (
	RiskNone Risk = iota
	RiskLow
	RiskMedium
	RiskHigh
)

func (r Risk) String() string {
	return [...]string{"aucun", "faible", "modéré", "élevé"}[r]
}

type Recommendation struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Problem       string   `json:"problem"`
	Explanation   string   `json:"explanation"`
	Action        string   `json:"action"`
	Gain          int64    `json:"gain"`
	GainKind      string   `json:"gainKind"`
	Consequences  string   `json:"consequences"`
	Risk          Risk     `json:"risk"`
	RiskLabel     string   `json:"riskLabel"`
	Prerequisites string   `json:"prerequisites"`
	Verification  string   `json:"verification"`
	InApp         bool     `json:"inApp"` // réalisable depuis l'outil (menu « Libérer de l'espace »)
	Steps         []string `json:"steps,omitempty"`
	Paths         []string `json:"paths,omitempty"`
	SettingsURI   string   `json:"settingsUri,omitempty"`
}

type Alert struct {
	Level string `json:"level"` // probleme | attention | info
	Text  string `json:"text"`
}

type Cause struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Bytes       int64  `json:"bytes"`
	Explanation string `json:"explanation"`
}

type Folder struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Target   string `json:"target"`
	Alloc    int64  `json:"alloc"`
	Freeable int64  `json:"freeable"`
	Pinned   int64  `json:"pinned"`
	Old      int64  `json:"old"`
	Files    int64  `json:"files"`
	PinState string `json:"pinState"`
	Depth    int    `json:"depth"`
}

type PlanStep struct {
	Goal      int64    `json:"goal"`
	Reachable bool     `json:"reachable"`
	Gain      int64    `json:"gain"`
	Folders   []string `json:"folders"`
	Note      string   `json:"note"`
}

type TargetSummary struct {
	scan.Target
	Exists        bool                    `json:"exists"`
	Err           string                  `json:"error,omitempty"`
	Volume        string                  `json:"volume"`
	Agg           scan.Agg                `json:"agg"`
	RootPin       string                  `json:"rootPin"`
	CloudShare    float64                 `json:"cloudShare"` // part des fichiers gérés par Fichiers à la demande
	Cats          map[string]*scan.CatAgg `json:"categories"`
	ConflictCount int64                   `json:"conflictCount"`
	ConflictAlloc int64                   `json:"conflictAlloc"`
}

type Totals struct {
	ActiveAlloc   int64 `json:"activeAlloc"`
	ActiveLogical int64 `json:"activeLogical"`
	Online        int64 `json:"onlineLogical"`
	OnlineFiles   int64 `json:"onlineFiles"`
	Local         int64 `json:"local"`
	Pinned        int64 `json:"pinned"`
	NotCloud      int64 `json:"notCloud"`
	Freeable      int64 `json:"freeable"`
	OldFreeable   int64 `json:"oldFreeable"`
	Orphan        int64 `json:"orphan"`
	Legacy        int64 `json:"legacy"`
	Cache         int64 `json:"cache"`
	Recycle       int64 `json:"recycle"`
	AllLocal      int64 `json:"allLocal"` // tout ce que l'outil associe à OneDrive sur le disque
}

type ScanInfo struct {
	Files     int64            `json:"files"`
	Dirs      int64            `json:"dirs"`
	Errors    int64            `json:"errors"`
	Samples   []scan.ScanError `json:"samples"`
	Seconds   float64          `json:"seconds"`
	Canceled  bool             `json:"canceled"`
	Estimated int64            `json:"estimated"`
}

type Diagnosis struct {
	Generated    time.Time           `json:"generated"`
	AppVersion   string              `json:"appVersion"`
	OS           P.OSInfo            `json:"os"`
	Volumes      []P.Volume          `json:"volumes"`
	OneDrive     P.OneDriveInfo      `json:"oneDrive"`
	Placeholders string              `json:"placeholderMode"`
	Known        []KnownFolderStatus `json:"knownFolders"`
	OtherClouds  []P.SyncRoot        `json:"otherClouds"`
	Targets      []TargetSummary     `json:"targets"`
	Totals       Totals              `json:"totals"`
	Recycle      []P.RecycleInfo     `json:"recycle"`
	Causes       []Cause             `json:"causes"`
	Alerts       []Alert             `json:"alerts"`
	Recs         []Recommendation    `json:"recommendations"`
	Folders      []Folder            `json:"folders"`
	Plan         []PlanStep          `json:"plan"`
	Limits       []string            `json:"limits"`
	Notes        []string            `json:"notes"`
	Scan         ScanInfo            `json:"scan"`
	Dupes        *DupSummary         `json:"dupes,omitempty"`
}

// DupSummary est rempli par la recherche de doublons (facultative).
type DupSummary struct {
	Groups       int   `json:"groups"`
	Files        int   `json:"files"`
	Gain         int64 `json:"gain"`
	FreeableInOD int64 `json:"freeableInOneDrive"`
	Unverified   int   `json:"unverifiedGroups"`
	Hashed       int64 `json:"hashed"`
	Complete     bool  `json:"complete"`
}

type Env struct {
	AppVersion   string
	OS           P.OSInfo
	Volumes      []P.Volume
	OneDrive     P.OneDriveInfo
	Placeholders string
	Known        []P.KnownFolder
	Recycle      []P.RecycleInfo
	Discovery    Discovery
	Same         func(a, b string) bool
	Windows      bool
	Now          time.Time
}

func isActiveKind(k scan.TargetKind) bool { return k == scan.KindOneDrive || k == scan.KindLibrary }

// Diagnose construit le diagnostic complet à partir du scan.
func Diagnose(env Env, res *scan.Result) *Diagnosis {
	if env.Now.IsZero() {
		env.Now = time.Now()
	}
	d := &Diagnosis{Generated: env.Now, AppVersion: env.AppVersion, OS: env.OS, Volumes: env.Volumes,
		OneDrive: env.OneDrive, Placeholders: env.Placeholders, Recycle: env.Recycle,
		OtherClouds: env.Discovery.OtherClouds, Notes: env.Discovery.Notes}
	d.Known = KnownStatus(env.Known, env.Discovery.Targets, env.Same)
	t := &d.Totals
	var estimated int64
	for _, tr := range res.Targets {
		ts := TargetSummary{Target: tr.Target, Exists: tr.Exists, Err: tr.Err, Volume: tr.Volume, Cats: tr.Cats,
			ConflictCount: tr.ConflictCount, ConflictAlloc: tr.ConflictAlloc}
		if tr.Root != nil {
			ts.Agg = tr.Root.Agg
			ts.RootPin = tr.Root.PinState()
			if ts.Agg.Files > 0 {
				ts.CloudShare = float64(ts.Agg.Files-ts.Agg.States[P.StateNotCloud].Count) / float64(ts.Agg.Files)
			}
		}
		estimated += ts.Agg.Estimated
		a := ts.Agg
		switch {
		case isActiveKind(tr.Kind):
			t.ActiveAlloc += a.Alloc
			t.ActiveLogical += a.Logical
			t.Online += a.States[P.StateOnline].Logical
			t.OnlineFiles += a.States[P.StateOnline].Count
			t.Local += a.States[P.StateLocal].Alloc
			t.Pinned += a.States[P.StatePinned].Alloc
			t.NotCloud += a.States[P.StateNotCloud].Alloc
			t.OldFreeable += a.OldLocalAlloc
		case tr.Kind == scan.KindOrphan:
			t.Orphan += a.Alloc
		case tr.Kind == scan.KindLegacy:
			t.Legacy += a.Alloc
		case tr.Kind == scan.KindCache:
			t.Cache += a.Alloc
		}
		d.Targets = append(d.Targets, ts)
	}
	t.Freeable = t.Local + t.Pinned
	for _, r := range env.Recycle {
		if r.OK {
			t.Recycle += r.Size
		}
	}
	t.AllLocal = t.ActiveAlloc + t.Orphan + t.Legacy + t.Cache
	d.Scan = ScanInfo{Files: res.Files, Dirs: res.Dirs, Errors: res.ErrorCount, Samples: firstErrors(res.Errors, 30),
		Seconds: res.Finished.Sub(res.Started).Seconds(), Canceled: res.Canceled, Estimated: estimated}
	d.Folders = CandidateFolders(res, 512<<20)
	d.Causes = causes(d)
	d.Alerts = alerts(d, env)
	d.Recs = recommendations(d, env)
	d.Plan = plan(d.Folders, t.Freeable)
	d.Limits = limits(d, env, res)
	return d
}

func firstErrors(e []scan.ScanError, n int) []scan.ScanError {
	if len(e) > n {
		return e[:n]
	}
	return e
}

func causes(d *Diagnosis) []Cause {
	t := d.Totals
	cs := []Cause{
		{"local", "Fichiers OneDrive téléchargés sur l'ordinateur", t.Local,
			"Ces fichiers sont déjà enregistrés dans le cloud et une copie complète est conservée sur ce PC. OneDrive peut libérer cette copie locale sans rien supprimer en ligne."},
		{"pinned", "Fichiers réglés sur « Toujours conserver sur cet appareil »", t.Pinned,
			"Ces fichiers ont été demandés explicitement hors connexion (par vous ou par un réglage appliqué à un dossier). OneDrive ne les libère jamais tout seul."},
		{"notcloud", "Fichiers du dossier OneDrive qui ne sont pas gérés « à la demande »", t.NotCloud,
			"Fichiers ordinaires : soit la fonction Fichiers à la demande est désactivée, soit ils ne sont pas encore synchronisés, soit OneDrive ne les prend pas en charge. Ils occupent toujours de la place."},
		{"orphan", "Anciens dossiers OneDrive non reliés à un compte", t.Orphan,
			"Copies locales laissées par un compte déconnecté ou un ancien dossier. Elles ne sont plus synchronisées : elles peuvent contenir des fichiers uniques."},
		{"legacy", "Anciens emplacements des dossiers Bureau/Documents/Images", t.Legacy,
			"Ces dossiers ont été redirigés vers OneDrive, mais l'ancien emplacement local contient encore des fichiers."},
		{"recycle", "Corbeille Windows", t.Recycle,
			"Les fichiers supprimés (y compris depuis OneDrive) restent dans la Corbeille et occupent l'espace tant qu'elle n'est pas vidée."},
		{"cache", "Données de l'application OneDrive", t.Cache,
			"Journaux et fichiers d'installation de l'application OneDrive."},
	}
	var out []Cause
	for _, c := range cs {
		if c.Bytes > 0 {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Bytes > out[j].Bytes })
	return out
}

func alerts(d *Diagnosis, env Env) []Alert {
	var out []Alert
	add := func(level, text string) { out = append(out, Alert{level, text}) }
	if !env.Windows {
		add("probleme", "Ce programme est conçu pour Windows : ici, aucune information OneDrive n'est disponible.")
	}
	od := d.OneDrive
	if env.Windows && len(od.Accounts) == 0 {
		add("probleme", "Aucun compte OneDrive connecté n'a été trouvé pour cet utilisateur Windows.")
	}
	if env.Windows && !od.Installed {
		add("attention", "L'application OneDrive n'a pas été trouvée sur cet ordinateur.")
	}
	if env.Windows && od.RunningKnown && !od.Running {
		add("attention", "OneDrive n'est pas en cours d'exécution : la libération d'espace ne peut pas être appliquée tant qu'il n'est pas démarré et connecté.")
	}
	if od.FilterDriver == "désactivé" {
		add("probleme", "Le pilote Windows des fichiers cloud (CldFlt) est désactivé : la fonction Fichiers à la demande ne peut pas fonctionner.")
	}
	for _, v := range d.Volumes {
		if v.Type == "fixe" && v.Total > 0 && float64(v.Free)/float64(v.Total) < 0.10 {
			add("attention", fmt.Sprintf("Le disque %s est presque plein : %s libres sur %s.", v.Root, F.Bytes(v.Free), F.Bytes(v.Total)))
		}
	}
	for _, ts := range d.Targets {
		if isActiveKind(ts.Kind) && !ts.Exists {
			add("attention", "Le dossier « "+ts.Path+" » déclaré par OneDrive est introuvable.")
		}
		if isActiveKind(ts.Kind) && ts.RootPin == "toujours_conserver" {
			add("info", "Le dossier entier « "+ts.Label+" » est réglé sur « Toujours conserver sur cet appareil » : tout son contenu est téléchargé.")
		}
	}
	if len(od.Accounts) > 1 {
		add("info", fmt.Sprintf("%d comptes OneDrive sont connectés sur ce PC.", len(od.Accounts)))
	}
	if d.Scan.Canceled {
		add("attention", "L'analyse a été annulée : les chiffres sont partiels.")
	}
	return out
}

func rec(id, title string) Recommendation { return Recommendation{ID: id, Title: title} }

func recommendations(d *Diagnosis, env Env) []Recommendation {
	t := d.Totals
	var out []Recommendation
	activePaths := func() []string {
		var p []string
		for _, ts := range d.Targets {
			if isActiveKind(ts.Kind) && ts.Exists {
				p = append(p, ts.Path)
			}
		}
		return p
	}
	runningNote := "OneDrive doit être démarré, connecté et à jour. Une connexion Internet sera nécessaire plus tard pour rouvrir ces fichiers."
	verify := "Menu « Vérifier » de l'outil : il remesure l'espace réellement occupé par les fichiers traités et l'espace libre du disque. Dans l'Explorateur, les fichiers concernés affichent un nuage bleu."

	if t.Local >= 100<<20 {
		r := rec("liberer-local", "Libérer l'espace des fichiers OneDrive déjà synchronisés")
		r.Problem = fmt.Sprintf("%s de fichiers OneDrive sont téléchargés sur ce PC alors qu'ils sont déjà dans le cloud.", F.Bytes(t.Local))
		r.Explanation = "Libérer de l'espace ne supprime pas le fichier : il reste visible dans l'Explorateur et intact dans OneDrive en ligne. Seule la copie locale est retirée ; il sera retéléchargé automatiquement quand vous l'ouvrirez."
		if t.OldFreeable > 0 {
			r.Explanation += fmt.Sprintf(" Parmi ces fichiers, %s n'ont pas été modifiés depuis plus d'un an : ce sont de bons candidats.", F.Bytes(t.OldFreeable))
		}
		r.Action = "Choisir les dossiers à rendre « disponibles uniquement en ligne » (menu Libérer de l'espace). Méthode officielle : réglage « Libérer de l'espace » de OneDrive (attribut épinglage U, comme la commande attrib +U -P)."
		r.Gain, r.GainKind = t.Local, GainEstimated
		r.Consequences = "Ces fichiers ne seront plus utilisables hors connexion tant qu'ils n'auront pas été rouverts avec Internet. Les fichiers ouverts ou pas encore synchronisés sont laissés tels quels par OneDrive."
		r.Risk = RiskLow
		r.Prerequisites = runningNote
		r.Verification = verify
		r.InApp = true
		r.Paths = activePaths()
		out = append(out, r)
	}
	if t.Pinned >= 100<<20 {
		r := rec("liberer-epingles", "Revoir les dossiers réglés sur « Toujours conserver sur cet appareil »")
		r.Problem = fmt.Sprintf("%s de fichiers sont épinglés pour rester en permanence sur ce PC.", F.Bytes(t.Pinned))
		r.Explanation = "Ce réglage est parfois appliqué à tout le dossier OneDrive par erreur. Tant qu'il est actif, OneDrive ne libère jamais ces fichiers."
		r.Action = "Dans le menu Libérer de l'espace, sélectionner les dossiers épinglés dont vous n'avez pas besoin hors connexion (l'outil les signale par « épinglé »)."
		r.Gain, r.GainKind = t.Pinned, GainEstimated
		r.Consequences = "Les fichiers concernés ne seront plus disponibles sans Internet. Si un dossier doit rester hors connexion (voyage, travail sans réseau), ne le sélectionnez pas."
		r.Risk = RiskMedium
		r.Prerequisites = runningNote
		r.Verification = verify
		r.InApp = true
		out = append(out, r)
	}
	for _, ts := range d.Targets {
		if !isActiveKind(ts.Kind) || !ts.Exists || ts.Agg.Files < 50 {
			continue
		}
		nc := ts.Agg.States[P.StateNotCloud]
		if ts.CloudShare < 0.2 && nc.Alloc >= 500<<20 {
			r := rec("fichiers-a-la-demande:"+ts.Path, "Activer « Fichiers à la demande » pour "+ts.Label)
			r.Problem = fmt.Sprintf("%s des fichiers de ce dossier ne sont pas gérés à la demande : %s occupés sur le disque.", F.Pct(nc.Count, ts.Agg.Files), F.Bytes(nc.Alloc))
			r.Explanation = "Quand cette fonction est désactivée (ou après une ancienne installation), OneDrive télécharge tout le contenu du compte. C'est la cause la plus fréquente d'un dossier OneDrive de plusieurs centaines de Go."
			r.Action = "Régler OneDrive pour libérer l'espace disque (manipulation dans les paramètres officiels de OneDrive)."
			r.Steps = []string{
				"Cliquer sur l'icône nuage OneDrive à droite de la barre des tâches (près de l'horloge).",
				"Cliquer sur la roue dentée ⚙ puis « Paramètres ».",
				"Ouvrir « Synchroniser et sauvegarder » puis « Paramètres avancés ».",
				"Dans « Fichiers à la demande », cliquer sur « Libérer de l'espace disque » (si le bouton propose « Télécharger tous les fichiers », c'est déjà actif).",
				"Attendre quelques minutes, puis relancer l'analyse de cet outil.",
			}
			r.Gain, r.GainKind = nc.Alloc, GainEstimated
			r.Consequences = "Les fichiers déjà synchronisés deviennent disponibles en ligne uniquement ; les fichiers non synchronisés restent sur le disque."
			r.Risk = RiskLow
			r.Prerequisites = "OneDrive démarré et connecté. Disque au format NTFS. Windows 10 (version 1709 ou ultérieure) ou Windows 11."
			r.Verification = "Relancer l'analyse : la part « non gérée à la demande » doit fortement baisser."
			out = append(out, r)
		} else if nc.Alloc >= 1<<30 {
			r := rec("non-synchronises:"+ts.Path, "Vérifier les fichiers non pris en charge dans "+ts.Label)
			r.Problem = fmt.Sprintf("%s de fichiers ordinaires (%s fichiers) dans un dossier pourtant géré à la demande.", F.Bytes(nc.Alloc), F.Int(nc.Count))
			r.Explanation = "Il peut s'agir de fichiers en attente de synchronisation, exclus par OneDrive (types ou chemins non pris en charge) ou en erreur. Ils ne peuvent pas être libérés tant qu'ils ne sont pas synchronisés."
			r.Action = "Ouvrir l'icône OneDrive et vérifier qu'il n'y a ni pause, ni erreur de synchronisation. Relancer ensuite l'analyse."
			r.Gain, r.GainKind = 0, GainNone
			r.Risk = RiskNone
			r.Prerequisites = "Aucun."
			r.Verification = "Relancer l'analyse."
			out = append(out, r)
		}
	}
	{
		r := rec("assistant-stockage", "Activer l'Assistant Stockage pour l'avenir")
		r.Problem = "Sans réglage, les fichiers ouverts restent téléchargés indéfiniment et l'espace se remplit à nouveau."
		r.Explanation = "L'Assistant Stockage de Windows peut rendre automatiquement « en ligne uniquement » les fichiers OneDrive non ouverts depuis une durée choisie (par exemple 30 jours)."
		r.Action = "Ouvrir Paramètres > Système > Stockage > Assistant Stockage, l'activer, puis dans la section OneDrive choisir une durée."
		r.Steps = []string{
			"Ouvrir les Paramètres Windows (l'outil peut les ouvrir directement).",
			"Système > Stockage > « Assistant Stockage » : activer.",
			"Cliquer sur « Assistant Stockage » pour ouvrir ses options.",
			"Dans la partie OneDrive, choisir « Rendre disponible en ligne uniquement si non ouvert depuis : 30 jours » (ou plus).",
		}
		r.Gain, r.GainKind = t.OldFreeable, GainPotential
		r.Consequences = "Réglage réversible à tout moment. Il ne supprime pas vos fichiers OneDrive."
		r.Risk = RiskLow
		r.Prerequisites = "Windows 10 ou 11, OneDrive avec Fichiers à la demande."
		r.Verification = "Les fichiers anciens affichent un nuage bleu après la prochaine exécution de l'Assistant."
		r.SettingsURI = "ms-settings:storagesense"
		out = append(out, r)
	}
	var conflicts int64
	var conflictCount int64
	for _, ts := range d.Targets {
		conflicts += ts.ConflictAlloc
		conflictCount += ts.ConflictCount
	}
	if conflictCount > 0 {
		r := rec("conflits", "Examiner les copies de conflit OneDrive")
		r.Problem = fmt.Sprintf("%s fichiers portent le nom de cet ordinateur en suffixe (%s) : ce sont probablement des copies créées lors de conflits de synchronisation.", F.Int(conflictCount), F.Bytes(conflicts))
		r.Explanation = "Quand un fichier est modifié à deux endroits à la fois, OneDrive garde les deux versions. Ces copies peuvent contenir des modifications uniques."
		r.Action = "Comparer chaque copie avec l'original avant de décider. L'outil ne supprime rien : il liste les fichiers et peut ouvrir leur emplacement."
		r.Gain, r.GainKind = conflicts, GainPotential
		r.Consequences = "Supprimer la mauvaise version ferait perdre des modifications."
		r.Risk = RiskMedium
		r.Prerequisites = "Vérification manuelle de chaque copie."
		r.Verification = "Relancer l'analyse."
		out = append(out, r)
	}
	if d.Dupes != nil && d.Dupes.Gain >= 100<<20 {
		r := rec("doublons", "Traiter les doublons confirmés")
		r.Problem = fmt.Sprintf("%d groupes de fichiers identiques (contenu vérifié par empreinte SHA-256) : %s d'espace local redondant.", d.Dupes.Groups, F.Bytes(d.Dupes.Gain))
		r.Explanation = "Des fichiers identiques au bit près existent à plusieurs endroits. Certains peuvent être volontaires (sauvegardes, fichiers utilisés par un logiciel)."
		r.Action = "Option sûre : libérer l'espace local des copies situées dans OneDrive (elles restent en ligne). Option manuelle : supprimer vous-même une copie après vérification."
		r.Gain, r.GainKind = d.Dupes.Gain, GainPotential
		r.Consequences = "Libérer : aucune perte. Supprimer : la copie part dans la Corbeille et dans la corbeille OneDrive en ligne."
		r.Risk = RiskMedium
		r.Prerequisites = "Vérifier que la copie conservée est la bonne."
		r.Verification = "Relancer la recherche de doublons."
		out = append(out, r)
	}
	for _, ts := range d.Targets {
		if ts.Kind == scan.KindOrphan && ts.Exists && ts.Agg.Alloc >= 100<<20 {
			r := rec("ancien:"+ts.Path, "Vérifier l'ancien dossier « "+ts.Path+" »")
			r.Problem = fmt.Sprintf("Ce dossier occupe %s et n'est relié à aucun compte OneDrive connecté.", F.Bytes(ts.Agg.Alloc))
			r.Explanation = "C'est souvent une copie laissée après une déconnexion de compte, un changement d'emplacement ou une réinstallation. Comme il n'est plus synchronisé, il peut contenir des fichiers qui n'existent nulle part ailleurs."
			r.Action = "Comparer son contenu avec OneDrive en ligne (onedrive.live.com ou portail Microsoft 365). Si tout s'y trouve, il peut être archivé sur un disque externe ou supprimé manuellement. L'outil ne le supprimera jamais."
			r.Gain, r.GainKind = ts.Agg.Alloc, GainPotential
			r.Consequences = "Une suppression sans vérification peut faire perdre des fichiers uniques."
			r.Risk = RiskHigh
			r.Prerequisites = "Vérification complète du contenu en ligne. Idéalement, copie de sauvegarde sur un disque externe."
			r.Verification = "Relancer l'analyse après votre décision."
			r.Paths = []string{ts.Path}
			out = append(out, r)
		}
		if ts.Kind == scan.KindLegacy && ts.Exists && ts.Agg.Alloc >= 100<<20 {
			r := rec("ancien-emplacement:"+ts.Path, "Ancien emplacement local : "+ts.Path)
			r.Problem = fmt.Sprintf("%s restent dans l'ancien emplacement local d'un dossier redirigé vers OneDrive.", F.Bytes(ts.Agg.Alloc))
			r.Explanation = "Ces fichiers ne sont pas synchronisés par OneDrive. Ils peuvent être d'anciennes copies… ou des fichiers que vous croyez sauvegardés et qui ne le sont pas."
			r.Action = "Ouvrir le dossier, comparer avec le dossier OneDrive correspondant, puis déplacer vous-même ce qui manque dans OneDrive."
			r.Gain, r.GainKind = ts.Agg.Alloc, GainPotential
			r.Consequences = "Ne rien supprimer sans comparaison."
			r.Risk = RiskHigh
			r.Prerequisites = "Comparaison manuelle."
			r.Verification = "Relancer l'analyse."
			r.Paths = []string{ts.Path}
			out = append(out, r)
		}
	}
	if t.Recycle >= 1<<30 {
		r := rec("corbeille", "Vider la Corbeille Windows")
		r.Problem = fmt.Sprintf("La Corbeille occupe %s.", F.Bytes(t.Recycle))
		r.Explanation = "Les fichiers supprimés de OneDrive sur ce PC passent par la Corbeille. Ils restent aussi récupérables dans la corbeille OneDrive en ligne pendant un temps limité (30 jours pour un compte personnel, 93 jours en général pour un compte professionnel)."
		r.Action = "Ouvrir la Corbeille, vérifier son contenu, puis la vider vous-même (clic droit > Vider la Corbeille). L'outil ne le fait pas."
		r.Gain, r.GainKind = t.Recycle, GainMeasured
		r.Consequences = "Irréversible sur ce PC. Les fichiers OneDrive restent récupérables en ligne pendant la durée de rétention."
		r.Risk = RiskMedium
		r.Prerequisites = "Vérifier qu'aucun fichier utile ne s'y trouve."
		r.Verification = "Relancer l'analyse."
		out = append(out, r)
	}
	if t.Cache >= 2<<30 {
		r := rec("cache", "Données de l'application OneDrive volumineuses")
		r.Problem = fmt.Sprintf("Le dossier de l'application OneDrive occupe %s.", F.Bytes(t.Cache))
		r.Explanation = "Il contient des journaux et des fichiers d'installation. Un volume important peut indiquer un problème de synchronisation répété."
		r.Action = "Ne pas supprimer ce dossier à la main. Mettre OneDrive à jour, puis, si le volume reste anormal, utiliser la réinitialisation officielle de OneDrive (aide Microsoft « Réinitialiser OneDrive »)."
		r.Gain, r.GainKind = 0, GainNone
		r.Risk = RiskMedium
		r.Prerequisites = "Aucun."
		r.Verification = "Relancer l'analyse."
		out = append(out, r)
	}
	if pst := pstSize(d); pst >= 1<<30 {
		r := rec("pst", "Fichiers de messagerie Outlook (.pst/.ost) dans OneDrive")
		r.Problem = fmt.Sprintf("%s de fichiers .pst/.ost se trouvent dans OneDrive.", F.Bytes(pst))
		r.Explanation = "Ces fichiers sont modifiés en permanence quand Outlook est ouvert, ce qui gêne leur synchronisation et les maintient sur le disque."
		r.Action = "Les déplacer hors de OneDrive (avec Outlook fermé) ou les archiver. À faire manuellement."
		r.Gain, r.GainKind = 0, GainNone
		r.Risk = RiskMedium
		r.Prerequisites = "Outlook fermé ; sauvegarde du fichier."
		r.Verification = "Relancer l'analyse."
		out = append(out, r)
	}
	if len(d.OneDrive.Accounts) > 1 || hasLibraries(d) {
		r := rec("comptes", "Plusieurs comptes ou bibliothèques synchronisés")
		var parts []string
		for _, ts := range d.Targets {
			if isActiveKind(ts.Kind) && ts.Exists {
				parts = append(parts, fmt.Sprintf("%s : %s sur le disque", ts.Label, F.Bytes(ts.Agg.Alloc)))
			}
		}
		r.Problem = "Plusieurs espaces OneDrive/SharePoint sont synchronisés : " + strings.Join(parts, " ; ") + "."
		r.Explanation = "Chaque compte ou bibliothèque synchronisé ajoute son contenu. Une bibliothèque d'équipe peut être très volumineuse."
		r.Action = "Pour un espace inutile : OneDrive > Paramètres > Compte > « Arrêter la synchronisation » sur la bibliothèque concernée. Attention : les fichiers déjà téléchargés restent alors sur le disque et ne sont plus synchronisés ; libérez d'abord leur espace avec cet outil."
		r.Gain, r.GainKind = 0, GainNone
		r.Risk = RiskMedium
		r.Prerequisites = "Savoir quels espaces sont réellement utilisés."
		r.Verification = "Relancer l'analyse."
		out = append(out, r)
	}
	for i := range out {
		out[i].RiskLabel = out[i].Risk.String()
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		ag, bg := a.Gain > 0 && a.GainKind != GainNone, b.Gain > 0 && b.GainKind != GainNone
		if ag != bg {
			return ag
		}
		if a.Risk != b.Risk {
			return a.Risk < b.Risk
		}
		if a.InApp != b.InApp {
			return a.InApp
		}
		return a.Gain > b.Gain
	})
	return out
}

func hasLibraries(d *Diagnosis) bool {
	for _, ts := range d.Targets {
		if ts.Kind == scan.KindLibrary && ts.Exists {
			return true
		}
	}
	return false
}

func pstSize(d *Diagnosis) int64 {
	var n int64
	for _, ts := range d.Targets {
		if isActiveKind(ts.Kind) {
			if c := ts.Cats["messagerie"]; c != nil {
				n += c.Alloc
			}
		}
	}
	return n
}

// CandidateFolders propose des dossiers précis à libérer : on descend dans
// l'arborescence tant que l'espace est concentré dans quelques sous-dossiers.
func CandidateFolders(res *scan.Result, min int64) []Folder {
	var out []Folder
	var visit func(n *scan.Node, t *scan.TargetResult, depth int)
	visit = func(n *scan.Node, t *scan.TargetResult, depth int) {
		f := n.Agg.Freeable()
		if f < min {
			return
		}
		var big []*scan.Node
		var bigSum int64
		for _, c := range n.Children {
			if !c.Link && c.Agg.Freeable() >= min {
				big = append(big, c)
				bigSum += c.Agg.Freeable()
			}
		}
		if depth < 4 && len(big) > 0 && float64(bigSum) >= 0.7*float64(f) {
			for _, c := range big {
				visit(c, t, depth+1)
			}
			return
		}
		out = append(out, Folder{Path: n.Path, Name: n.Name, Target: t.Label, Alloc: n.Agg.Alloc, Freeable: f,
			Pinned: n.Agg.States[P.StatePinned].Alloc, Old: n.Agg.OldLocalAlloc, Files: n.Agg.Files,
			PinState: n.PinState(), Depth: depth})
	}
	for _, t := range res.Targets {
		if t.Root != nil && isActiveKind(t.Kind) {
			visit(t.Root, t, 0)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Freeable > out[j].Freeable })
	return out
}

// plan : combinaison de dossiers pour atteindre 50/100/200 Go, en commençant
// par les dossiers les moins risqués (peu de fichiers épinglés, anciens).
func plan(folders []Folder, total int64) []PlanStep {
	sorted := append([]Folder(nil), folders...)
	score := func(f Folder) float64 {
		if f.Freeable == 0 {
			return 0
		}
		return float64(f.Old)/float64(f.Freeable) - float64(f.Pinned)/float64(f.Freeable)
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		si, sj := score(sorted[i]), score(sorted[j])
		if si != sj {
			return si > sj
		}
		return sorted[i].Freeable > sorted[j].Freeable
	})
	var out []PlanStep
	for _, g := range []int64{50, 100, 200} {
		goal := F.GB(g)
		st := PlanStep{Goal: goal}
		if total < goal {
			st.Note = fmt.Sprintf("Non atteignable avec la seule libération d'espace OneDrive (maximum mesuré : %s).", F.Bytes(total))
			out = append(out, st)
			continue
		}
		for _, f := range sorted {
			st.Folders = append(st.Folders, f.Path)
			st.Gain += f.Freeable
			if st.Gain >= goal {
				break
			}
		}
		st.Reachable = st.Gain >= goal
		if !st.Reachable {
			st.Note = "Atteignable en libérant aussi des fichiers dispersés (sélectionner le dossier OneDrive entier)."
		}
		out = append(out, st)
	}
	return out
}

func limits(d *Diagnosis, env Env, res *scan.Result) []string {
	var l []string
	l = append(l, "L'espace utilisé dans le cloud OneDrive (quota en ligne) n'est pas mesuré : l'outil mesure uniquement le disque de ce PC.")
	if d.Scan.Estimated > 0 {
		l = append(l, fmt.Sprintf("Pour %s fichiers, l'espace disque est estimé (taille arrondie au cluster) faute de mesure exacte.", F.Int(d.Scan.Estimated)))
	}
	l = append(l, "Les gains de libération sont des estimations : OneDrive peut refuser de libérer un fichier ouvert, verrouillé ou non synchronisé.")
	l = append(l, "Les fichiers « liens physiques » (hard links) éventuels sont comptés à chaque emplacement.")
	l = append(l, "Les liens symboliques et points de jonction ne sont jamais suivis (ni comptés deux fois).")
	if res.ErrorCount > 0 {
		l = append(l, fmt.Sprintf("%s éléments n'ont pas pu être lus (autorisations, fichiers verrouillés) : ils ne sont pas comptés.", F.Int(res.ErrorCount)))
	}
	if res.BigTruncated {
		l = append(l, "La liste des gros fichiers a été tronquée (trop d'éléments).")
	}
	if res.DupTruncated {
		l = append(l, "La recherche de doublons ne porte que sur une partie des fichiers (limite mémoire).")
	}
	if env.Placeholders != "" && env.Placeholders != "actif" && env.Windows {
		l = append(l, "Mode d'affichage des espaces réservés : "+env.Placeholders+" ; certains états pourraient être mal détectés.")
	}
	l = append(l, "Les doublons dont une copie est « en ligne uniquement » ne sont jamais lus (cela déclencherait un téléchargement) : ils sont signalés comme non vérifiés.")
	return l
}

// Recommend recalcule les recommandations (par exemple après la recherche de doublons).
func Recommend(d *Diagnosis, env Env) []Recommendation { return recommendations(d, env) }

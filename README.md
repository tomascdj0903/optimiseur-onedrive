# Optimiseur OneDrive

Outil Windows qui diagnostique et réduit l'espace disque occupé par OneDrive, **sans perte de données**. Il se présente sous forme d'un exécutable portable en console (français). Il peut, avec l'accord de l'utilisateur, partager sa console et son rapport en direct vers une webapp de suivi privée.

- Guide pour la personne aidée : [GUIDE-UTILISATEUR.md](GUIDE-UTILISATEUR.md)
- Téléchargement : onglet **Releases** (`OptimiseurOneDrive.exe` et son empreinte `.sha256`)

## Ce que fait l'outil

1. **Détection** (registre `HKCU\Software\Microsoft\OneDrive\Accounts`, racines `SyncRootManager`, variables `%OneDrive%`) :
   - comptes personnels et professionnels ;
   - bibliothèques SharePoint/Teams synchronisées ;
   - emplacement réel des dossiers, même déplacés sur un autre disque ;
   - redirection de Bureau, Documents et Images ;
   - anciens dossiers `OneDrive*` qui ne sont plus reliés à aucun compte ;
   - anciens emplacements locaux ;
   - état de l'application et du pilote `CldFlt`.
2. **Mesure en lecture seule**. Le parcours est récursif (`FindFirstFileExW`, chemins longs `\\?\`) et tourne en priorité d'E/S basse. Les liens symboliques et les jonctions ne sont **jamais suivis**. Pour chaque fichier, l'outil relève :
   - sa **taille logique** ;
   - son **espace réel sur le disque**, mesuré avec `GetCompressedFileSizeW` pour les espaces réservés, fichiers sparse et compressés, ou estimé par arrondi au cluster dans les autres cas ;
   - son **état Fichiers à la demande**, déduit des attributs `RECALL_ON_DATA_ACCESS`, `PINNED`, `UNPINNED` et de l'étiquette `IO_REPARSE_TAG_CLOUD` : en ligne uniquement, téléchargé, toujours conservé, ou non géré.
3. **Diagnostic** :
   - causes classées par volume ;
   - alertes ;
   - recommandations ordonnées par risque, chacune avec 8 champs : problème, explication, action, gain, conséquences, risque, conditions, vérification ;
   - dossiers candidats à libérer ;
   - plan pour récupérer 50, 100 ou 200 Go ;
   - limites de l'analyse.
4. **Doublons**, sur demande : regroupement par taille, puis empreinte rapide, puis SHA-256 complet. Les fichiers en ligne uniquement ne sont **jamais lus**, car la lecture déclencherait leur téléchargement.
5. **Libération d'espace**, seule opération qui modifie quelque chose (voir plus bas).
6. **Vérification** : l'outil remesure l'espace des fichiers traités et l'espace libre des disques.
7. **Restauration** du réglage d'épinglage antérieur, à partir du journal.

## Opérations susceptibles de modifier des données

| Opération | Ce qui change | Ce qui ne change jamais |
|---|---|---|
| Libérer de l'espace (menu 6) | Attribut d'épinglage (`-P +U`), équivalent de `attrib +U -P` et du menu « Libérer de l'espace ». Ensuite, **OneDrive** retire la copie locale. | Aucun fichier supprimé, déplacé ou renommé. Rien n'est supprimé du cloud. |
| Remettre le réglage d'avant (menu 7) | Les bits d'épinglage reprennent leur valeur antérieure, d'après le journal. Les fichiers « toujours conservés » sont retéléchargés. | Les éléments modifiés depuis par l'utilisateur ne sont pas touchés. |

Garde-fous mis en œuvre et testés :
- le scan est toujours en lecture seule ;
- un récapitulatif à usage unique expire au bout de 15 minutes ;
- l'utilisateur doit taper le mot `LIBERER` ;
- une seconde confirmation `OUI` est exigée si des fichiers épinglés sont concernés ou si le volume est très important ;
- les chemins situés hors des dossiers OneDrive synchronisés sont refusés ;
- l'opération est refusée si OneDrive est arrêté ;
- chaque modification est relue juste après pour vérification ;
- un journal JSONL est écrit avant toute modification. Si le journal ne peut pas être créé, l'opération est annulée ;
- l'option `--lecture-seule` interdit toute modification.

La « restauration » remet le **réglage**, pas une copie locale. Les fichiers redeviennent locaux en étant retéléchargés, ce qui demande Internet et de l'espace disque. Aucune fonction d'annulation au-delà de cela n'est promise.

## Partage en direct et webapp de suivi

- Le partage n'est activé qu'avec l'accord de l'utilisateur, et le masquage des noms est disponible.
- Seules sont envoyées les lignes affichées dans la console et le rapport. Jamais de contenu de fichier.
- Les données vont dans le schéma privé `odo` du projet Supabase « alternancehub ». Ce schéma n'est pas exposé par l'API. On y accède uniquement par les fonctions `odo_start_session`, `odo_push_logs` et `odo_push_report` (écriture), et `odo_view_*` (lecture protégée par une clé dont seul le hash SHA-256 est stocké).
- La webapp (`webapp/index.html`, déployée sur Vercel) affiche les sessions, la console en direct, la synthèse et le rapport complet.
- Pour tout retirer de Supabase :
  ```sql
  drop function if exists public.odo_start_session, public.odo_push_logs, public.odo_push_report,
    public.odo_view_sessions, public.odo_view_logs, public.odo_view_reports;
  drop schema if exists odo cascade;
  ```

## Mises à jour automatiques

Au démarrage, l'exe interroge `api.github.com/repos/tomascdj0903/optimiseur-onedrive/releases/latest`. Si une version plus récente existe, il propose de l'installer. Il télécharge alors l'exe **et** le fichier `.sha256`, vérifie l'empreinte, remplace l'exécutable en gardant l'ancien en `.old`, puis redémarre. Toute empreinte absente ou fausse entraîne un refus.

Pour publier une version :
```bash
VERSION=1.0.1
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o dist/OptimiseurOneDrive.exe ./cmd/optimiseur
(cd dist && sha256sum OptimiseurOneDrive.exe > OptimiseurOneDrive.exe.sha256)
gh release create v$VERSION dist/OptimiseurOneDrive.exe dist/OptimiseurOneDrive.exe.sha256 --title "v$VERSION" --notes "…"
```

## Construire et tester

Prérequis : Go 1.24 ou plus récent, sans aucune dépendance externe.

```bash
go test ./...                       # tests automatisés (données synthétiques, aucun fichier réel touché)
GOOS=windows go vet ./...           # vérification du code Windows
go build -tags dev -o odo-dev ./cmd/optimiseur && ODO_SIMULATION=1 ./odo-dev --sans-maj   # démo SIMULÉE (dev uniquement)
```

Les tests couvrent les points suivants :
- calcul des tailles (logique et disque), états des fichiers, agrégation par dossier ;
- erreurs d'accès non bloquantes ;
- jonctions non suivies ;
- gros fichiers et copies de conflit ;
- annulation ;
- chemins OneDrive personnalisés, anciens dossiers et anciens emplacements, cibles imbriquées comptées une seule fois ;
- gains jamais supérieurs au mesuré, ordre des recommandations par risque, détection de Fichiers à la demande désactivé ;
- doublons SHA-256 sans téléchargement des fichiers en ligne ;
- mode lecture seule ;
- confirmation erronée, jeton réutilisé ou expiré, confirmation du risque, OneDrive arrêté, chemins hors OneDrive ;
- libération avec fichier verrouillé et refus d'accès, vérification, restauration, aucun fichier perdu ;
- masquage des noms ;
- mise à jour avec empreinte valide ou invalide ;
- partage (envoi par lots, serveur injoignable).

La simulation (`simfs`) reproduit le comportement de OneDrive : la libération est refusée sur un fichier ouvert ou non synchronisé, et lire un fichier en ligne déclenche son téléchargement.

## Options

```
--diagnostic     analyse + rapport, aucune modification possible, puis fermeture
--lecture-seule  interdit toute modification
--sans-maj       ne cherche pas de mise à jour
--sans-partage   ne propose pas le partage en direct
```

## Limites connues

- Le quota utilisé dans le cloud n'est pas mesuré : seul le disque local l'est.
- Les gains de libération sont des **estimations**, car OneDrive peut refuser de libérer un fichier ouvert, verrouillé ou non synchronisé. La vérification montre ce qui a réellement été libéré.
- Un fichier lié physiquement (hard link) à plusieurs emplacements est compté à chaque emplacement.
- Les fichiers de moins d'environ 700 octets stockés dans la MFT sont estimés à un cluster.
- Si Fichiers à la demande est désactivé, l'outil le détecte et guide pas à pas vers le réglage officiel. Il ne modifie pas les paramètres de OneDrive lui-même.
- L'exe n'est pas signé numériquement, d'où l'avertissement SmartScreen au premier lancement (procédure dans le guide).
- La partie Windows (appels système) a été compilée et vérifiée statiquement. La logique est testée sur une simulation. Le premier lancement réel sur un PC reste le test de validation final : utiliser `--diagnostic` d'abord.

## Procédure en cas de problème

1. Relancer avec `OptimiseurOneDrive.exe --diagnostic`. Cette option n'autorise aucune modification et produit le fichier `Diagnostic-OneDrive-*.txt` à côté de l'exe.
2. Le journal de session se trouve dans `%LOCALAPPDATA%\OptimiseurOneDrive\journaux\` et les opérations dans `…\operations\*.jsonl`.
3. L'annexe technique en fin de rapport contient :
   - les attributs bruts des dossiers racine ;
   - les comptes et racines détectés ;
   - les erreurs ;
   - le mode des espaces réservés (`phcm`) ;
   - l'état du pilote `CldFlt`.

## Architecture

```
cmd/optimiseur        console (menus, confirmation, partage, mise à jour)
internal/platform     API Windows (FindFirstFileExW, attributs, registre, dossiers connus, Corbeille…) + implémentation générique pour le développement
internal/scan         parcours en lecture seule, agrégats, gros fichiers, candidats doublons
internal/analysis     détection des cibles, diagnostic, causes, recommandations, plan
internal/dupes        doublons par empreintes
internal/actions      seules opérations modifiantes : libérer, restaurer, vérifier, journal
internal/report       rapport texte et annexe JSON, masquage des noms
internal/share        partage vers Supabase (avec consentement)
internal/update       mise à jour depuis GitHub, vérification SHA-256
webapp/               page de suivi (statique, Vercel)
```

Choix techniques :
- **Go** : un seul `.exe` autonome de 6 à 7 Mo, sans runtime à installer, et des appels directs aux API Win32.
- **Console** : légère et fiable. Elle se partage telle quelle vers la webapp.
- Une interface web hébergée seule ne peut pas lire l'état OneDrive des fichiers : le programme local est donc indispensable.

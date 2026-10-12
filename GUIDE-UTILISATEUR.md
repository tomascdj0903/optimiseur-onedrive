# Optimiseur OneDrive — guide rapide

Ce programme vous aide à comprendre pourquoi OneDrive prend autant de place sur votre ordinateur. Il libère ensuite cet espace **sans supprimer vos fichiers**.

## 1. Télécharger

1. Ouvrez ce lien : **https://github.com/tomascdj0903/optimiseur-onedrive/raw/main/release/OptimiseurOneDrive.exe**. Le téléchargement démarre tout seul.
2. Si le navigateur affiche un avertissement, choisissez **« Conserver »**.

Rien à installer : c'est un programme unique. Il se met à jour tout seul ensuite.

## 2. Lancer

1. Double-cliquez sur **`OptimiseurOneDrive.exe`**.
2. Si une fenêtre bleue **« Windows a protégé votre ordinateur »** apparaît, cliquez sur **« Informations complémentaires »**, puis sur **« Exécuter quand même »**. Ce message s'affiche pour tout petit programme qui n'est pas signé par un éditeur commercial.
3. Aucun mot de passe ni droit administrateur n'est demandé.

## 3. Utiliser le programme

Une fenêtre **Optimiseur OneDrive** s'ouvre, accompagnée d'une petite fenêtre noire qui affiche le journal. **Laissez la fenêtre noire ouverte** : elle se ferme toute seule quand vous fermez le programme.

1. **Partage (facultatif).** Sur l'écran d'accueil, activez « Partager en direct » si quelqu'un vous aide à distance, puis donnez-lui le **code de 6 caractères** affiché. Vous pouvez aussi masquer les noms de vos dossiers. Le contenu de vos fichiers n'est jamais envoyé.
2. **Analyse.** Cliquez sur **« Analyser mon stockage »**. Rien n'est modifié pendant l'analyse, et vous pouvez l'annuler.
3. **Diagnostic.** Vous voyez :
   - combien d'espace OneDrive occupe vraiment ;
   - **pourquoi** il en occupe autant ;
   - les conseils, du plus sûr au plus délicat.

   Les étiquettes indiquent la fiabilité des chiffres : **mesuré**, **estimation** ou **potentiel**.
4. **Libérer de l'espace.**
   - Cochez les dossiers concernés, ou cliquez sur une sélection toute prête (par exemple « Récupérer 100 Go »).
   - Cliquez sur **Continuer** et lisez le récapitulatif.
   - Pour confirmer, tapez **LIBERER**. Le programme demande aussi **OUI** si certains fichiers étaient réglés pour rester hors connexion.
5. **Vérifier.** Cliquez ensuite sur **« Vérifier le résultat »**. OneDrive peut mettre quelques minutes à libérer l'espace.

« Libérer de l'espace » **n'est pas une suppression** :
- le fichier reste visible dans l'Explorateur, avec un petit nuage ;
- il reste intact dans OneDrive en ligne ;
- il se télécharge de nouveau tout seul quand vous l'ouvrez. Il faut alors une connexion Internet.

## 4. Les autres pages

| Page | Rôle |
|---|---|
| Conseils détaillés | Chaque conseil avec son gain, son risque, ses conditions et la manière de vérifier |
| Dossiers | Parcourir les dossiers et leur taille réelle sur le disque |
| Gros fichiers | Les fichiers de plus de 50 Mo, avec recherche et tri |
| Doublons | Fichiers identiques. Rien n'est supprimé automatiquement. |
| Historique | Revérifier une opération ou « Remettre comme avant » |
| Rapport | Enregistrer un fichier `.txt` à envoyer par e-mail |

## En cas de souci

- **La fenêtre ne s'ouvre pas** : la fenêtre noire affiche une adresse commençant par `http://127.0.0.1`. Copiez-la dans votre navigateur.
- **« OneDrive n'est pas démarré »** : ouvrez OneDrive depuis le menu Démarrer, attendez qu'il soit connecté, puis réessayez.
- **Des fichiers restent sur le disque après la libération** : ils sont peut-être ouverts dans un logiciel ou pas encore synchronisés. Fermez vos logiciels et attendez la fin de la synchronisation (icône nuage près de l'horloge). Revérifiez ensuite depuis l'Historique.
- **Le programme ne se lance pas** : l'antivirus l'a peut-être bloqué. Autorisez-le, ou envoyez une capture d'écran à la personne qui vous aide.

Le programme garde aussi un journal sur votre ordinateur, dans `%LOCALAPPDATA%\OptimiseurOneDrive\`.

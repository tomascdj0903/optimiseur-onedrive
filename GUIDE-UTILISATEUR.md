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

## 3. Répondre aux deux questions du début

- **« Activer le partage en direct ? »** Répondez **O** si quelqu'un vous aide à distance. Il verra ce qui s'affiche dans la fenêtre. Un **code de 6 caractères** apparaît alors : donnez-le-lui.
  Le contenu de vos fichiers n'est jamais envoyé, et vos mots de passe non plus.
- **« Masquer les noms de vos dossiers ? »** Répondez **O** si vous préférez que les noms ne soient pas visibles.

## 4. Lancer l'analyse

Appuyez sur **Entrée**. L'analyse **ne modifie rien**. Elle peut durer quelques minutes.

À la fin, vous voyez trois choses :
- combien d'espace OneDrive occupe vraiment ;
- **pourquoi** il occupe autant de place ;
- la liste des solutions, de la plus sûre à la plus délicate.

## 5. Libérer de l'espace (menu 6)

« Libérer de l'espace » **n'est pas une suppression** :
- le fichier reste visible dans l'Explorateur, avec un petit nuage ;
- il reste intact dans OneDrive en ligne ;
- il se télécharge de nouveau tout seul quand vous l'ouvrez. Il faut alors une connexion Internet.

Pour le faire :
1. Choisissez les dossiers. Tapez par exemple `+1` pour ajouter le premier, ou `P100` pour la sélection qui vise 100 Go.
2. Tapez `V` pour valider.
3. Lisez le récapitulatif. Si vous êtes d'accord, tapez **`LIBERER`**. Si certains fichiers étaient réglés pour rester hors connexion, le programme demande en plus **`OUI`**.
4. Le programme vérifie ensuite le résultat. OneDrive peut mettre quelques minutes à libérer l'espace.

## 6. Le reste du menu

| Touche | Rôle |
|---|---|
| 2 | Le détail de chaque recommandation (risques, conditions, vérification) |
| 3 | Parcourir les dossiers et leur taille |
| 4 | Les plus gros fichiers |
| 5 | Rechercher les doublons. Rien n'est supprimé automatiquement. |
| 7 | Revérifier une opération ou remettre le réglage d'avant |
| 8 | Enregistrer le rapport dans un fichier `.txt` pour l'envoyer par e-mail |
| A | Ouvrir l'Assistant Stockage de Windows |
| 0 | Quitter |

## En cas de souci

- **« OneDrive n'est pas démarré »** : ouvrez OneDrive depuis le menu Démarrer, attendez qu'il soit connecté, puis réessayez.
- **Des fichiers restent sur le disque après la libération** : ils sont peut-être ouverts dans un logiciel ou pas encore synchronisés. Fermez vos logiciels et attendez la fin de la synchronisation (icône nuage près de l'horloge). Revérifiez ensuite avec le menu 7.
- **Le programme ne se lance pas** : l'antivirus l'a peut-être bloqué. Autorisez-le, ou envoyez une capture d'écran à la personne qui vous aide.
- **Fermer pendant une opération** : `Ctrl+C` arrête proprement. Aucun fichier n'est perdu.

Le programme garde aussi un journal sur votre ordinateur, dans `%LOCALAPPDATA%\OptimiseurOneDrive\`.

# godio

Enregistre une réunion sous Windows, la transcrit **localement**, en extrait
l'essentiel avec Claude Code, et ajoute le résultat à votre note du jour Obsidian.

```
micro          ──► me.wav   ──┐
                               ├──► transcription ──► Claude (Haiku) ──► note du jour
sortie système ──► them.wav ──┘
```

Les deux pistes sont enregistrées séparément : c'est ce qui donne l'attribution
« moi » / « interlocuteur » sans modèle de diarisation.

L'audio ne quitte jamais la machine — la transcription tourne en local via
whisper.cpp. Seul le texte transcrit est transmis à Claude Code.

## Prérequis

- Windows 10 1703 ou plus récent (le loopback WASAPI événementiel l'exige)
- [Claude Code](https://claude.com/claude-code) dans le `PATH`
- Un coffre Obsidian
- Pour compiler : Go 1.25+ et un compilateur C (malgo utilise cgo)

## Installation

Téléchargez l'archive depuis les [releases](https://github.com/clementherve/godio/releases),
décompressez-la, puis :

```powershell
.\godio.exe setup      # télécharge whisper.cpp et les modèles (CUDA si GPU NVIDIA)
.\godio.exe doctor     # vérifie que tout est en place
```

`setup` choisit automatiquement la bonne variante : build CUDA en présence d'un
GPU NVIDIA, build CPU sinon. Le coffre Obsidian se choisit ensuite depuis le
menu de l'icône (**Configuration ▸ Choisir le coffre Obsidian…**).

## Deux binaires

| Binaire | Rôle |
|---|---|
| **`godio-tray.exe`** | **l'application** — icône dans la zone de notification |
| `godio.exe` | la ligne de commande : `setup`, `doctor`, `record`, `transcribe`… |

`godio-tray.exe` est compilé dans le sous-système graphique de Windows : il
n'ouvre jamais de fenêtre de console, et il **survit à la fermeture du terminal**
depuis lequel vous l'avez lancé. C'est celui à épingler ou à mettre au démarrage.

`godio.exe` reste une application console pour que ses commandes affichent
quelque chose.

## L'icône

Deux pastilles, empilées à droite du micro :

| Pastille | Signification |
|---|---|
| **haut** | le micro — vous |
| **bas** | le son système — l'interlocuteur |

| Couleur | État |
|---|---|
| gris | en veille, pas d'enregistrement |
| **vert** | cette source a produit du son récemment |
| **rouge** | cette source est muette |
| orange | traitement en cours (le pourcentage est dans le menu) |
| croix rouge | erreur (le détail est dans le menu) |

**La pastille basse en rouge est le signal important.** Elle veut généralement
dire que Teams joue le son sur un périphérique différent de celui qui est
capturé : changez la « Sortie capturée » dans le menu. Sans cela l'autre partie
est enregistrée en silence, et vous ne le découvririez qu'après la réunion.

L'indicateur utilise une fenêtre glissante de 10 secondes : une conversation
normale, où chacun parle à son tour, reste verte des deux côtés.

Une notification Windows s'affiche quand la réunion est traitée — la
transcription prend assez de temps pour qu'on s'éloigne de la machine.

## Vitesse de transcription

Deux réglages dans `config.toml` :

```toml
[whisper]
backend = "auto"    # auto | cuda | cpu
preset  = "fast"    # fast | balanced | accurate
threads = 0         # 0 = un par coeur physique
```

**`backend`** est de loin le plus important. Un GPU NVIDIA transcrit 5 à 10 fois
plus vite que le CPU. `auto` détecte le GPU via `nvidia-smi` et installe la bonne
variante ; `godio doctor` indique laquelle est active.

**`preset`** contrôle le décodage. whisper.cpp fait par défaut une recherche en
faisceau de largeur 5, ce qui coûte plusieurs fois le décodage glouton :

| Préréglage | Décodage | |
|---|---|---|
| `fast` | `-bs 1 -nf` | **défaut** — environ 2× plus rapide |
| `balanced` | `-bs 2` | |
| `accurate` | `-bs 5` | le défaut de whisper.cpp |

`fast` est le défaut parce que Claude réécrit ensuite la transcription : la
plupart des erreurs supplémentaires du décodage glouton n'atteignent jamais les
notes. Si vous constatez qu'une décision ou une action se perd, passez à
`balanced`.

**`threads`** vaut un fil par **coeur physique**, pas par coeur logique. Les
noyaux de calcul de whisper.cpp saturent déjà les unités SIMD de chaque coeur ;
les hyperthreads ajoutent de la contention sans débit.

Le modèle suit le backend : `setup` installe le modèle complet pour le GPU, et
sa version quantifiée `q5_0` (trois fois plus petite, nettement plus rapide) pour
le CPU, où la bande passante mémoire est le facteur limitant.

## En ligne de commande

Chaque étape est exécutable seule, sur un dossier de session existant. Les
fichiers intermédiaires sont conservés, donc on peut retravailler le prompt
d'extraction — ou comparer deux préréglages — sans réenregistrer quoi que ce soit.

```powershell
.\godio.exe devices              # lister les périphériques
.\godio.exe doctor               # diagnostiquer la configuration, le GPU, le préréglage
.\godio.exe setup [--dry-run]    # installer les dépendances
.\godio.exe record -s 30         # enregistrer 30 s, avec vu-mètres
.\godio.exe transcribe           # transcrire la dernière session
.\godio.exe extract              # régénérer les notes via Claude
.\godio.exe write                # ajouter à la note du jour
.\godio.exe process              # les trois d'un coup
.\godio.exe config [edit]        # afficher ou ouvrir la configuration
```

Sans argument, ces commandes agissent sur la session la plus récente ; on peut
aussi passer un chemin de session.

## Fichiers

| Chemin | Contenu |
|---|---|
| `%APPDATA%\godio\config.toml` | configuration |
| `%LOCALAPPDATA%\godio\sessions\<date>\` | une session |
| └ `me.wav`, `them.wav` | les deux pistes, 16 kHz mono |
| └ `me.json`, `them.json` | sortie brute de whisper.cpp |
| └ `transcript.txt` | transcription fusionnée et étiquetée |
| └ `notes.md` | notes produites par Claude |
| └ `session.json` | horodatage, périphériques, décalages des pistes |

Les enregistrements ne sont jamais supprimés automatiquement : purgez
`sessions\` vous-même quand vous le souhaitez.

## Consentement

Enregistrer une conversation sans prévenir les participants est, dans l'Union
européenne, un problème juridique et pas seulement une question de politesse.
godio ne s'enregistre jamais tout seul : il faut une action explicite à chaque
réunion. Prévenez les participants.

## Développement

```bash
go test ./...                 # la logique pure est testable hors Windows
go run ./scripts/genicons     # régénérer les icônes
go run ./scripts/checkpe godio.exe console godio-tray.exe gui
```

La fusion des transcriptions, le format WAV, l'analyse de la sortie whisper, la
résolution des releases et l'écriture Obsidian sont couverts par des tests et ne
dépendent pas de Windows. La capture loopback ne peut être vérifiée que sous
Windows.

### Intégration continue

`.github/workflows/ci.yml` compile et teste sous Windows (les runners GitHub
fournissent mingw-w64, nécessaire à cgo) et publie les deux binaires. `checkpe`
lit les en-têtes PE pour garantir que `godio-tray.exe` est bien dans le
sous-système graphique et qu'aucun binaire ne dépend des DLL de mingw. Un tag
`v*` attache les archives à la release correspondante.

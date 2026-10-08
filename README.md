# godio

Enregistre une réunion sous Windows, la transcrit **localement**, en extrait
l'essentiel avec Claude Code, et ajoute le résultat à votre note du jour Obsidian.

```
micro        ──► me.wav   ──┐
                             ├──► transcription ──► Claude (Haiku) ──► note du jour
sortie système ──► them.wav ─┘
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

```powershell
# 1. Compiler (depuis Windows, avec w64devkit ou mingw-w64 installé)
go build -o godio.exe ./cmd/godio

# 2. Télécharger whisper.cpp et les modèles, à côté de godio.exe
.\scripts\fetch-deps.ps1          # ajoutez -Cuda si vous avez un GPU NVIDIA

# 3. Créer la configuration, puis renseigner le chemin du coffre
.\godio.exe config

# 4. Vérifier que tout est en place
.\godio.exe doctor
```

## Utilisation

```powershell
.\godio.exe run
```

L'icône apparaît dans la zone de notification :

| Icône | État |
|---|---|
| micro gris | prêt |
| point **vert** | enregistrement, les deux côtés ont du son |
| point **rouge** | enregistrement, un côté est muet |
| point orange | traitement en cours |
| croix rouge | erreur (le détail est dans le menu) |

**Le point rouge est le signal important.** Il veut généralement dire que Teams
joue le son sur un périphérique différent de celui qui est capturé : changez la
« Sortie capturée » dans le menu. Sans cela, l'autre partie est enregistrée en
silence et vous ne le découvririez qu'après la réunion.

L'indicateur utilise une fenêtre glissante de 10 secondes : une conversation
normale, où chacun parle à son tour, reste verte.

## En ligne de commande

Chaque étape est exécutable seule, sur un dossier de session existant. Les
fichiers intermédiaires sont conservés, donc on peut retravailler le prompt
d'extraction sans réenregistrer quoi que ce soit.

```powershell
.\godio.exe devices              # lister les périphériques
.\godio.exe doctor               # diagnostiquer la configuration
.\godio.exe record -s 30         # enregistrer 30 s, avec vu-mètres
.\godio.exe transcribe           # transcrire la dernière session
.\godio.exe extract              # régénérer les notes via Claude
.\godio.exe write                # ajouter à la note du jour
.\godio.exe process              # les trois d'un coup
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

## Réglages utiles

Dans `config.toml` :

- `whisper.model` — passez à `models/ggml-medium.bin` si la transcription est
  trop lente. Comptez le temps de traitement : à 0,3× le temps réel, une heure
  de réunion demande vingt minutes.
- `claude.model` — `claude-haiku-5-5` par défaut. Passez à un modèle plus
  capable si les notes vous paraissent trop superficielles.
- `audio.silence_dbfs` — seuil de silence de l'indicateur (−45 dBFS par défaut).
- `obsidian.heading` — la section de la note du jour sous laquelle écrire.

## Consentement

Enregistrer une conversation sans prévenir les participants est, dans l'Union
européenne, un problème juridique et pas seulement une question de politesse.
godio ne s'enregistre jamais tout seul : il faut une action explicite à chaque
réunion. Prévenez les participants.

## Développement

```bash
go test ./...                 # la logique pure est testable hors Windows
go run ./scripts/genicons     # régénérer les icônes de la zone de notification
```

`devices`, le format WAV, la fusion des transcriptions, l'analyse de la sortie
whisper et l'écriture Obsidian sont couverts par des tests et ne dépendent pas
de Windows. La capture loopback, elle, ne peut être vérifiée que sous Windows.

### Intégration continue

`.github/workflows/ci.yml` compile et teste sous Windows (les runners GitHub
fournissent mingw-w64, nécessaire à cgo) et publie `godio.exe` en artefact.
Le binaire est lié statiquement : la CI échoue s'il se met à dépendre des DLL
de mingw. Un tag `v*` attache l'archive à la release correspondante.

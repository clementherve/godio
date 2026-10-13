<div align="center">
  <h1>Godio 🎤</h1>
  <p>The ears of your second brain. Whisper + Claude Code + Godio = ✨</p>
  <p>
    <a href="https://github.com/clementherve/godio/actions/workflows/ci.yml">
      <img src="https://github.com/clementherve/godio/actions/workflows/ci.yml/badge.svg" alt="CI">
    </a>
  </p>
</div>

## About

Record reunions and transcribe them using Whisper then interpret them using claude code before saving them into Obsidian.

```
micro           ──► me.wav   ──┐
                               ├──► transcription ──► Claude (Haiku) ──► daily note
system          ──► them.wav ──┘
```

## Requirements

- Windows 10 1703 or more recent (event-driven WASAPI loopback requires it)
- [Claude Code](https://claude.com/claude-code) in the `PATH`
- Obsidian Vault
- To build from source: Go 1.25+ and a C compiler for Malgo (cgo)

## Installation

```powershell
winget install ClementHerve.Godio
godio setup
godio doctor
```

winget puts both commands on the `PATH`, and installing this way does not go
through the browser download that makes Windows show "Windows protected your
PC" — the binaries are unsigned, so a direct download does.

The rest of this README writes `.\godio.exe`, which is what a manual install
needs; after `winget install` it is just `godio`.

<details>
<summary>Installing by hand instead</summary>

Download `godio-windows-amd64.zip` from the latest
[release](https://github.com/clementherve/godio/releases), unblock it
(right-click → Properties → Unblock), extract it, then from that folder:

```powershell
.\godio.exe setup
.\godio.exe doctor
```

Every release also carries `SHA256SUMS.txt`, printed by the build that
produced the files. To check a download against it:

```powershell
Get-FileHash .\godio-windows-amd64.zip -Algorithm SHA256
```

</details>

## Two binaries

| Binary               | Role                                                         |
| -------------------- | ------------------------------------------------------------ |
| **`godio-tray.exe`** | the notification-area icon                                   |
| `godio.exe`          | the command line: `setup`, `doctor`, `record`, `transcribe`… |

`godio-tray.exe` is built into the Windows GUI subsystem: it never opens a
console window, and it **survives the terminal it was launched from being
closed**. That is the one to pin, or to put in your startup folder.

`godio.exe` stays a console application so that its commands can print
something.

## Updating

```powershell
winget upgrade ClementHerve.Godio
```

On a manual install, godio updates itself:

```powershell
.\godio.exe update --check   # is there a newer release?
.\godio.exe update           # download it, verify it, install it
```

`update` refuses to run on a winget install: replacing the binaries behind
winget's back leaves it reporting the old version, and its next upgrade would
put that version back over the newer one.

## The tray icon

Two dots, stacked to the right of the microphone:

| Dot        | Meaning         |
| ---------- | --------------- |
| **top**    | you             |
| **bottom** | the other party |

| Colour    | State                                      |
| --------- | ------------------------------------------ |
| grey      | idle, not recording                        |
| **green** | this source made some sound recently       |
| **red**   | this source is silent                      |
| orange    | processing (the percentage is in the menu) |
| red cross | error (the details are in the menu)        |

## Automatic stop

A meeting ends, everyone hangs up, and nobody clicks stop. godio watches for
that: once **both** tracks have been silent for five minutes, it stops the
recording itself and runs the pipeline.

```toml
[audio]
auto_stop         = "5m0s"   # silence on both sides before stopping ("0s" = never)
auto_stop_warning = "30s"    # notice given before that ("0s" = none)
```

`godio doctor` prints the current setting, and `godio record` shows the
countdown inline next to the VU meters.

## Language

The interface speaks French and English. By default it follows the language you
set Windows to; `godio doctor` prints which it resolved to.

```toml
language = "auto"   # auto | fr | en
```

**The notes are not affected by this.** They follow `whisper.language` — the
language the meeting was actually held in — because the vault, the names and
the jargon in it are that language whatever the menus say. A French meeting
read on an English interface still produces French notes.

Changing `language` and clicking **Recharger** in the tray relabels the menu
without restarting.

## Note style

The transcript is verbatim; the _notes_ are what Claude writes from it, and how
much they say is a setting:

```toml
[claude]
style = "normal"   # bullets | normal | detailed | eli5
```

| Style      | What you get                                                                 |
| ---------- | ---------------------------------------------------------------------------- |
| `bullets`  | one line per point, and nothing that does not fit on one                     |
| `normal`   | **default**. a one-sentence summary and 3 to 7 factual points                |
| `detailed` | 8 to 15 points with sub-points: the figures, the reasons, the open questions |
| `eli5`     | written for someone outside the subject; every acronym explained once        |

Pick it from the tray (**Configuration → Style des notes**), or per run:

```powershell
.\godio.exe extract --style eli5              # the last meeting, re-read
.\godio.exe extract --day 2026-10-12 --style detailed
.\godio.exe extract --day hier --style bullets
```

## Transcription speed

Two settings in `config.toml`:

```toml
[whisper]
backend = "auto"    # auto | cuda | cpu
preset  = "fast"    # fast | balanced | accurate
threads = 0         # 0 = one per physical core
```

**`preset`** controls decoding. whisper.cpp defaults to a beam search of width
5, which costs several times what greedy decoding does:

| Preset     | Decoding    |                                 |
| ---------- | ----------- | ------------------------------- |
| `fast`     | `-bs 1 -nf` | **default** (roughly 2x faster) |
| `balanced` | `-bs 2`     |                                 |
| `accurate` | `-bs 5`     | whisper.cpp's own default       |

`fast` is the default because Claude rewrites the transcript afterwards: most
of greedy decoding's extra errors never reach the notes. If you find a decision
or an action going missing, move to `balanced`.

**`threads`** is one thread per **physical core**, not per logical one.
whisper.cpp's compute kernels already saturate each core's SIMD units;
hyperthreads add contention without adding throughput.

The model follows the backend: `setup` installs the full model for the GPU, and
its quantised `q5_0` version (three times smaller, markedly faster) for the
CPU, where memory bandwidth is the limiting factor.

## Command line

```powershell
.\godio.exe devices              # list the audio devices
.\godio.exe doctor               # check the configuration, the GPU, the preset
.\godio.exe setup [--dry-run]    # install the dependencies
.\godio.exe update [--check]     # update godio itself
.\godio.exe record -s 30         # record for 30 s, with VU meters
.\godio.exe transcribe           # transcribe the latest session
.\godio.exe extract [--style S]  # regenerate the notes through Claude
.\godio.exe purge [--dry-run]    # apply the retention policy
.\godio.exe write                # append to the daily note
.\godio.exe process              # all three in one go
.\godio.exe config [edit]        # show or open the configuration
```

With no argument these commands act on the most recent session; a session path
can be passed explicitly, or `--day` to replay a whole day. `godio --help`
lists the commands and `godio <command> --help` details one of them.

## Files

| Path                                    | Contents                                |
| --------------------------------------- | --------------------------------------- |
| `%APPDATA%\godio\config.toml`           | configuration                           |
| `%LOCALAPPDATA%\godio\sessions\<date>\` | one session                             |
| └ `me.wav`, `them.wav`                  | the two tracks, 16 kHz mono             |
| └ `me.json`, `them.json`                | whisper.cpp's raw output                |
| └ `transcript.txt`                      | the merged, speaker-labelled transcript |
| └ `notes.md`                            | the notes produced by Claude            |
| └ `session.json`                        | timestamp, devices, per-track offsets   |

### Retention

Recordings are kept forever by default. Two settings change that, and they are
separate because the costs are:

```toml
[sessions]
keep_audio_days = 0   # drop me.wav / them.wav after N days (0 = never)
keep_days       = 0   # delete the whole session folder after N days (0 = never)
```

The WAV pair is about **115 MB per hour**; the transcript and the notes are a
few kilobytes.
s
The tray applies the policy after each meeting it finishes. By hand:

```powershell
.\godio.exe purge --dry-run                     # what the config would delete
.\godio.exe purge --keep-audio-days 7 --dry-run # try a policy without saving it
.\godio.exe purge                               # apply it
```

## Development

```bash
go test ./...                 # the pure logic is testable off Windows
go run ./scripts/genicons     # regenerate the icons
go run ./scripts/checkpe godio.exe console godio-tray.exe gui
```

`packaging/winget/` holds the manifests submitted to
[winget-pkgs](https://github.com/microsoft/winget-pkgs), kept here as the copy
of what was sent. Tagging opens the pull request for the new version by itself;
the manifests are bumped by hand only when something other than the version
changes.

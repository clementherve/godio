# Godio

[![CI](https://github.com/clementherve/godio/actions/workflows/ci.yml/badge.svg)](https://github.com/clementherve/godio/actions/workflows/ci.yml)

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

Download the latest [release](https://github.com/clementherve/godio/releases), then:

```powershell
.\godio.exe setup
.\godio.exe doctor
```

## Two binaries

| Binary               | Role                                                       |
| -------------------- | ---------------------------------------------------------- |
| **`godio-tray.exe`** | **the application** — the notification-area icon           |
| `godio.exe`          | the command line: `setup`, `doctor`, `record`, `transcribe`… |

`godio-tray.exe` is built into the Windows GUI subsystem: it never opens a
console window, and it **survives the terminal it was launched from being
closed**. That is the one to pin, or to put in your startup folder.

`godio.exe` stays a console application so that its commands can print
something.

## Updating

```powershell
.\godio.exe update --check   # is there a newer release?
.\godio.exe update           # download it, verify it, install it
```

`update` replaces `godio.exe` and `godio-tray.exe` in place with the latest
GitHub release. Everything is downloaded and checked **before** anything on
disk moves: each file is hashed as it streams and compared against the SHA-256
digest GitHub publishes for that asset, and if any swap fails the previous
binaries are put back. A half-updated pair — a new CLI beside an old tray — is
the one outcome worth engineering against, so it is the one thing this cannot
leave behind.

The tray offers the same thing under **Mettre à jour godio…**, never during a
recording or while a meeting is being processed. It can also look for a new
release when it starts:

```toml
[update]
check_on_start = false   # check at launch and notify (never installs)
prerelease     = false
```

Installing is always an explicit click; nothing updates itself in the
background.

A running executable cannot delete itself on Windows, so the previous version
is renamed to `godio.exe.old` and swept on the next launch — and the update
only takes effect once godio is restarted, which both the CLI and the tray say.
The tray's **Redémarrer maintenant** does it for you.

Two caveats: the binaries are unsigned, so SmartScreen or an antivirus may have
an opinion about a freshly written `.exe`; and if godio lives somewhere you
cannot write, such as `Program Files`, the swap fails with a message saying so
— move the folder rather than elevating.

> `update` needs the repository to be public. While it is private every release
> asset returns 404, and godio says so rather than reporting a bare "not found".

## The tray icon

Two dots, stacked to the right of the microphone:

| Dot        | Meaning                              |
| ---------- | ------------------------------------ |
| **top**    | the microphone — you                 |
| **bottom** | the system audio — the other party   |

| Colour    | State                                             |
| --------- | ------------------------------------------------- |
| grey      | idle, not recording                               |
| **green** | this source made some sound recently              |
| **red**   | this source is silent                             |
| orange    | processing (the percentage is in the menu)        |
| red cross | error (the details are in the menu)               |

**The bottom dot turning red is the signal that matters.** It usually means
Teams is playing audio to a different device than the one being captured:
change « Sortie capturée » in the menu. Without that, the other party is
recorded in silence, and you would only find out after the meeting.

The indicator uses a 10-second sliding window, so a normal conversation, where
each side speaks in turn, stays green on both dots.

A Windows notification is shown once the meeting has been processed —
transcription takes long enough that you will have walked away from the
machine.

## Automatic stop

A meeting ends, everyone hangs up, and nobody clicks stop. godio watches for
that: once **both** tracks have been silent for five minutes, it stops the
recording itself and runs the pipeline.

```toml
[audio]
auto_stop         = "5m0s"   # silence on both sides before stopping ("0s" = never)
auto_stop_warning = "30s"    # notice given before that ("0s" = none)
```

Thirty seconds before the stop, the tray status counts down and a notification
appears. Any sound on either track cancels it and resets the clock, so a long
pause in a real meeting costs you nothing — the warning is a reprieve, not a
question to answer. It is deliberately **not** a modal dialog: the usual reason
a room goes quiet is that nobody is at the machine, and a dialog waiting for a
click would keep recording forever, which is the exact failure this prevents.

`godio doctor` prints the current setting, and `godio record` shows the
countdown inline next to the VU meters.

## Note style

The transcript is verbatim; the *notes* are what Claude writes from it, and how
much they say is a setting:

```toml
[claude]
style = "normal"   # bullets | normal | detailed | eli5
```

| Style      | What you get                                                                 |
| ---------- | ---------------------------------------------------------------------------- |
| `bullets`  | telegraphic — one line per point, and nothing that does not fit on one        |
| `normal`   | **default** — a one-sentence summary and 3 to 7 factual points                |
| `detailed` | 8 to 15 points with sub-points: the figures, the reasons, the open questions  |
| `eli5`     | written for someone outside the subject; every acronym explained once         |

The four sections — summary, key points, decisions, actions — are the same
whatever the style, because the daily note and the action count depend on them.
Only the writing changes.

Pick it from the tray (**Configuration → Style des notes**), or per run:

```powershell
.\godio.exe extract --style eli5              # the last meeting, re-read
.\godio.exe extract --day 2026-10-12 --style detailed
.\godio.exe extract --day hier --style bullets
```

`--day` takes `AAAA-MM-JJ`, `hier` / `yesterday`, `today`, or a negative number
of days (`-2`), and runs over **every** meeting of that day, oldest first. The
transcript is kept, so replaying a day costs one Claude call per meeting and no
audio at all — which is the point: a day recorded in `normal` can be read back
in `eli5` months later.

Note that `extract` only rewrites `notes.md`: running `write` afterwards appends
a *second* entry to the daily note rather than replacing the first.

No style loosens the "invent nothing" rule — `eli5` may explain what a term
means, never what was said about it.

## Transcription speed

Two settings in `config.toml`:

```toml
[whisper]
backend = "auto"    # auto | cuda | cpu
preset  = "fast"    # fast | balanced | accurate
threads = 0         # 0 = one per physical core
```

**`backend`** matters by far the most. An NVIDIA GPU transcribes 5 to 10 times
faster than the CPU. `auto` detects the GPU through `nvidia-smi` and installs
the matching build; `godio doctor` reports which one is **requested** and which
one is **actually installed**. When the two differ — typically a `whisper\`
folder installed as CPU before the GPU was available, or by the old
`fetch-deps.ps1` — transcription stays on the CPU whatever `backend` says: run
`godio setup` again and it replaces the installed build.

**`preset`** controls decoding. whisper.cpp defaults to a beam search of width
5, which costs several times what greedy decoding does:

| Preset     | Decoding    |                                   |
| ---------- | ----------- | --------------------------------- |
| `fast`     | `-bs 1 -nf` | **default** — roughly 2× faster   |
| `balanced` | `-bs 2`     |                                   |
| `accurate` | `-bs 5`     | whisper.cpp's own default         |

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

Every stage can be run on its own, against an existing session folder. The
intermediate files are kept, so you can rework the extraction prompt — or
compare two presets — without re-recording anything.

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

| Path                                    | Contents                                      |
| --------------------------------------- | --------------------------------------------- |
| `%APPDATA%\godio\config.toml`           | configuration                                 |
| `%LOCALAPPDATA%\godio\sessions\<date>\` | one session                                   |
| └ `me.wav`, `them.wav`                  | the two tracks, 16 kHz mono                   |
| └ `me.json`, `them.json`                | whisper.cpp's raw output                      |
| └ `transcript.txt`                      | the merged, speaker-labelled transcript       |
| └ `notes.md`                            | the notes produced by Claude                  |
| └ `session.json`                        | timestamp, devices, per-track offsets         |

### Retention

Recordings are kept forever by default. Two settings change that, and they are
separate because the costs are:

```toml
[sessions]
keep_audio_days = 0   # drop me.wav / them.wav after N days (0 = never)
keep_days       = 0   # delete the whole session folder after N days (0 = never)
```

The WAV pair is about **115 MB per hour**; the transcript and the notes are a
few kilobytes. Dropping the audio alone therefore recovers nearly all the space
while leaving the meeting replayable — `extract --day` still works on a session
whose recordings are gone. `keep_audio_days = 7` with `keep_days = 365` is a
reasonable pair.

Three rules make this safe to leave on:

- a session that never produced `notes.md` is **never** purged, whatever its
  age — an unprocessed recording is the only copy of that meeting;
- only folders whose name is a session timestamp are touched, so anything else
  you put in `sessions\` is left alone;
- age comes from the folder name, not from the file dates, so re-running
  `extract` does not quietly extend a meeting's life.

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

Transcript merging, the WAV format, parsing whisper's output, resolving
releases and writing to Obsidian are all covered by tests and do not depend on
Windows. Loopback capture can only be verified on Windows.

## License

godio is free software, licensed under the GNU General Public License version 3
or (at your option) any later version. See [LICENSE](LICENSE).

Copyright (C) 2026 Clément Hervé.

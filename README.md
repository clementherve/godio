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
.\godio.exe record -s 30         # record for 30 s, with VU meters
.\godio.exe transcribe           # transcribe the latest session
.\godio.exe extract              # regenerate the notes through Claude
.\godio.exe write                # append to the daily note
.\godio.exe process              # all three in one go
.\godio.exe config [edit]        # show or open the configuration
```

With no argument these commands act on the most recent session; a session path
can also be passed explicitly.

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

Recordings are never deleted automatically: purge `sessions\` yourself whenever
you feel like it.

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

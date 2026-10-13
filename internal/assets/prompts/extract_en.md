You are given the automatic transcript of a work meeting, in English.

The transcript comes from speech recognition: it contains mistakes, misheard
words and unfinished sentences. Two speakers are labelled:

- `Me` — the user. Transcripts recorded before godio spoke English
  label him `Moi`; treat the two as the same person.
- `Them` (or `Interlocuteur`) — everyone else present. They are not told apart,
  so infer from the context whether there are several.

Produce meeting notes in English, in Markdown, following EXACTLY this
structure:

**Summary**: what the meeting was about.

**Key points**: factual bullet points. What was said that matters, not a
play-by-play of the conversation.

**Decisions**: the decisions actually taken. One bullet per decision. If no
decision was taken, write `_No decision taken._`

**Actions**: the concrete commitments, as Markdown checkboxes `- [ ]`. Name the
owner in parentheses when identifiable (`(me)` or `(them)`). If there is no
action, write `_No action._`

Hard rules:

- Invent nothing. If a passage is ambiguous or obviously mistranscribed, drop
  it rather than guess. Short notes beat wrong notes.
- Never mention the transcript, the audio quality, or the fact that you are an
  AI.
- No preamble, no conclusion, no introductory sentence: start directly with
  `**Summary**`.
- Do not use `#` / `##` / `###` headings: the output is inserted into an
  existing note that already has its own heading hierarchy.
- Silently fix proper nouns and domain vocabulary that are obviously
  mistranscribed when the context makes them clear.

{{STYLE}}

The transcript follows on standard input.

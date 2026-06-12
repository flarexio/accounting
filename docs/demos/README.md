# Demo recordings

The GIFs embedded in the top-level [README](../../README.md) are generated from
the [VHS](https://github.com/charmbracelet/vhs) `.tape` scripts in this folder,
so they are reproducible and regenerable — edit the tape, re-record, commit the
GIF.

## Prerequisites

- `vhs` (`go install github.com/charmbracelet/vhs@latest`, plus its `ttyd` and
  `ffmpeg` runtime deps — see the VHS README).
- The `ledger` binary on `PATH` (`go install ./cmd/ledger`).
- A working `~/.flarex/accounting/config.yaml` with an OpenAI key: the TUI and
  `book-run` demos make real model calls.
- A CJK font, or the Chinese transcript renders as tofu (□). On Debian/Ubuntu:
  `sudo apt install -y fonts-noto-cjk && fc-cache -f`. The tapes set
  `FontFamily "JetBrains Mono, Noto Sans Mono CJK TC"` so Latin stays monospace
  and Chinese falls back to Noto.

## Record

Run each tape from the repository root so the relative `Output` and `--file`
paths resolve:

```bash
vhs docs/demos/posting.tape   # docs/demos/posting.gif
vhs docs/demos/policy.tape    # docs/demos/policy.gif
vhs docs/demos/reverse.tape   # docs/demos/reverse.gif
vhs docs/demos/reject.tape    # docs/demos/reject.gif
```

## Tuning

The `Sleep` durations are sized for one LLM turn and will need adjusting to your
model's latency — too short and the GIF cuts off mid-reasoning. The TUI tapes
press `Enter` once on the selection screen to enter the bookkeeper chat; if your
selection flow differs, fix the keystrokes there. `policy.tape` is a shell A/B
rather than a TUI flow because a policy change only reaches the prompt on the
next process start (`PromptRenderer` snapshots the company at session start).

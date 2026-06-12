# VHS tape: TUI happy-path posting (the Charm-style facelift).
# Record:  vhs docs/demos/posting.tape   →   docs/demos/posting.gif
# Prereqs: pre-seed the ledger (`ledger seed seed/taiwan_ledger.yaml`) — it's slow
# (embeds the chart), so it is NOT recorded here. Also needs `ledger` on PATH and
# an OpenAI key in config.yaml. The turn Sleeps cover one LLM turn — bump them if
# your model is slower; too short and the GIF cuts off mid-reasoning.

Output docs/demos/posting.gif

Set Shell "bash"
Set FontSize 16
Set Width 1200
Set Height 720
Set Padding 20
Set Theme "Catppuccin Mocha"
Set FontFamily "JetBrains Mono, Noto Sans Mono CJK TC"

Type "ledger tui" Enter
Sleep 3s

# Selection screen → enter the bookkeeper chat.
Enter
Sleep 2s

Type "5/10 台北總公司現金賣了一批商品，收到 105,000 含稅。"
Sleep 1s
Enter

# One reasoning turn: spinner → model rationale → preview.
Sleep 20s

# Hold on the posted entry so it can be read, then ctrl+c quits to the shell.
# (In chat, esc cancels a running turn / steps back; the turn waits above are long
# enough that we're idle here, so a single ctrl+c is a clean quit.)
Sleep 4s
Ctrl+C
Sleep 1s

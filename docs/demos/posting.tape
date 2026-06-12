# VHS tape: TUI happy-path posting (the Charm-style facelift).
# Record:  vhs docs/demos/posting.tape   →   docs/demos/posting.gif
# Prereqs: `ledger` on PATH, a seeded ledger, and an OpenAI key in config.yaml.
# The Sleep durations cover one LLM turn — tune them to your model's latency.

Output docs/demos/posting.gif

Set Shell "bash"
Set FontSize 16
Set Width 1200
Set Height 720
Set Padding 20
Set Theme "Catppuccin Mocha"
Set FontFamily "JetBrains Mono, Noto Sans Mono CJK TC"

# Seed off-camera so the demo opens straight into the TUI.
Hide
Type "ledger seed seed/taiwan_ledger.yaml" Enter
Sleep 2s
Show

Type "ledger tui" Enter
Sleep 3s

# Selection screen → enter the bookkeeper chat.
Enter
Sleep 2s

Type "5/10 台北總公司現金銷售商品，含稅 NT$105,000（銷貨收入 100,000、銷項稅額 5,000）。"
Sleep 1s
Enter

# One reasoning turn: spinner → model rationale → preview.
Sleep 14s

# Linger on the posted entry, then leave.
Sleep 2s
Type "q"
Sleep 1s

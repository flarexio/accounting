# VHS tape: TUI rejection — the validator blocks a closed period; the agent
# does not silently retarget it.
# Record:  vhs docs/demos/reject.tape   →   docs/demos/reject.gif
# Prereqs: pre-seed the ledger (slow, not recorded), `ledger` on PATH, an OpenAI
# key in config.yaml.

Output docs/demos/reject.gif

Set Shell "bash"
Set FontSize 16
Set Width 1200
Set Height 720
Set Padding 20
Set Theme "Catppuccin Mocha"
Set FontFamily "JetBrains Mono, Noto Sans Mono CJK TC"

Type "ledger tui" Enter
Sleep 3s
Enter
Sleep 2s

# 2026-03 is closed in the seed → expect a reject observation, not a posting.
Type "請在 2026-03 期間記錄 NT$10,000 現金收入。"
Sleep 1s
Enter

Sleep 14s
Sleep 2s
Type "q"
Sleep 1s

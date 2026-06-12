# VHS tape: TUI rejection — the validator refuses a deactivated account; the
# agent does not silently substitute an active one.
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

# 6199 其他費用（舊制）is deactivated in the seed → expect a reject, not a
# silent switch to an active account like 6117 雜費.
Type "幫我用「其他費用（舊制）」這個科目記一筆 5,000 的雜支。"
Sleep 1s
Enter

Sleep 14s
Sleep 2s
Ctrl+C
Sleep 1s

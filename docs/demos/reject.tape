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
# No select screen — launches straight into the bookkeeper chat (first branch).
Sleep 5s

# 6199 其他費用（舊制）is deactivated in the seed → expect a reject, not a
# silent switch to an active account like 6117 雜費.
Type "幫我用「其他費用（舊制）」這個科目記一筆 5,000 的雜支。"
Sleep 1s
Enter

# One reasoning turn ending in a reject observation.
Sleep 18s

# Hold on the reject, then ctrl+c quits to the shell.
Sleep 4s
Ctrl+C
Sleep 1s

# VHS tape: TUI reversal then re-post, driven by recent-context recall.
# Record:  vhs docs/demos/reverse.tape   →   docs/demos/reverse.gif
# Prereqs (pre-done, not recorded — slow): seed the ledger, then post one entry
# so JE-0001 exists, e.g.
#   ledger book-run --request '5/10 台北總公司現金賣了一批商品，收到 105,000 含稅。'
# Adjust the JE id below if your first entry is not JE-0001. Needs `ledger` on PATH
# and an OpenAI key in config.yaml. Recall is enabled, so turn 2 resolves the
# elliptical "重新過一筆" by calling recent_entries — both turns run in ONE session.

Output docs/demos/reverse.gif

Set Shell "bash"
Set FontSize 16
Set Width 1200
Set Height 820
Set Padding 20
Set Theme "Catppuccin Mocha"
Set FontFamily "JetBrains Mono, Noto Sans Mono CJK TC"

Type "ledger tui" Enter
Sleep 3s
Enter
Sleep 2s

# Turn 1: reverse the wrong entry (mechanical mirror + a reverses relation).
Type "JE-0001 金額打錯了，幫我沖掉。"
Sleep 1s
Enter
Sleep 18s
# Hold on the reversal preview before the next request.
Sleep 4s

# Turn 2: re-post with the correct amount. "重新過一筆" is elliptical, so the
# agent calls recent_entries to recover what JE-0001 was before posting the fix
# (94,500 incl 5% tax -> 90,000 sales + 4,500 output tax).
Type "再用正確的 94,500 含稅重新過一筆。"
Sleep 1s
Enter
Sleep 22s

# Hold on the re-posted entry, then ctrl+c quits to the shell.
Sleep 4s
Ctrl+C
Sleep 1s

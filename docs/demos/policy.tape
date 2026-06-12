# VHS tape: company policy flips the chosen account on the same transaction.
# Record:  vhs docs/demos/policy.tape   →   docs/demos/policy.gif
# Prereqs: pre-seed the ledger (slow, not recorded), `ledger` on PATH, an OpenAI
# key in config.yaml. Two separate TUI sessions: PromptRenderer snapshots the
# company at session start, so the policy is set BETWEEN them to take effect.

Output docs/demos/policy.gif

Set Shell "bash"
Set FontSize 16
Set Width 1280
Set Height 760
Set Padding 20
Set Theme "Catppuccin Mocha"
Set FontFamily "JetBrains Mono, Noto Sans Mono CJK TC"

# Pre-seed off-camera first (slow, not recorded). Here we only clear any leftover
# policy so the A/B starts from the no-policy state.
Hide
Type "ledger policy set <<<''" Enter
Sleep 2s
Show

# 1. First session, no policy → the gift lands in 交際費 (6115).
Type "ledger tui" Enter
Sleep 3s
Enter
Sleep 2s
Type "5/18 台北總公司送了一盒中秋禮盒給長期合作的客戶，現金付了 6,000。"
Sleep 1s
Enter
Sleep 14s
Sleep 2s
Ctrl+C

# 2. Show the policy, then set it (it routes client gifts to 廣告費).
Sleep 1s
Type "cat docs/demos/policy.md"
Sleep 1s
Enter
Sleep 3s
Type "ledger policy set --file docs/demos/policy.md"
Sleep 1s
Enter
Sleep 2s

# 3. Second session, same request → now 廣告費 (6108), and the memo cites the policy.
Type "ledger tui" Enter
Sleep 3s
Enter
Sleep 2s
Type "5/18 台北總公司送了一盒中秋禮盒給長期合作的客戶，現金付了 6,000。"
Sleep 1s
Enter
Sleep 14s
Sleep 2s
Ctrl+C
Sleep 1s

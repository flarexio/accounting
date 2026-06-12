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
Sleep 20s
# Hold on the 交際費 preview, then leave to the shell (chat → select → shell).
Sleep 4s
Escape
Sleep 1s
Escape
Sleep 1s

# 2. Show the policy, then set it (it routes client gifts to 廣告費).
Type "cat docs/demos/policy.md"
Sleep 1s
Enter
Sleep 4s
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
Sleep 20s
# Hold on the 廣告費 preview, then leave with esc.
Sleep 4s
Escape
Sleep 1s
Escape
Sleep 1s

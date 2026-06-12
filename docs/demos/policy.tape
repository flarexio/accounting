# VHS tape: company policy flips the chosen account on the same transaction.
# Record:  vhs docs/demos/policy.tape   →   docs/demos/policy.gif
# Prereqs: pre-seed the ledger (slow, not recorded), `ledger` on PATH, an OpenAI
# key in config.yaml. This is a shell A/B (not the TUI): book-run snapshots the
# company at start, so the policy is set BETWEEN two book-run processes to take effect.

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

# 1. No policy → the customer gift lands in 交際費 (6115).
Type "ledger book-run --request '5/18 台北總公司向長期合作客戶致贈中秋禮盒 NT$6,000，現金支付。'"
Sleep 1s
Enter
Sleep 14s

# 2. Show the policy, then set it (it routes client gifts to 廣告費).
Type "cat docs/demos/policy.md"
Sleep 1s
Enter
Sleep 3s
Type "ledger policy set --file docs/demos/policy.md"
Sleep 1s
Enter
Sleep 2s

# 3. Same request → now lands in 廣告費 (6108), and the memo cites the policy.
Type "ledger book-run --request '5/18 台北總公司向長期合作客戶致贈中秋禮盒 NT$6,000，現金支付。'"
Sleep 1s
Enter
Sleep 14s

Sleep 2s

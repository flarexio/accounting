# VHS tape: TUI reversal — reverse_journal mirrors an entry and links it.
# Record:  vhs docs/demos/reverse.tape   →   docs/demos/reverse.gif
# Prereqs: `ledger` on PATH, a seeded ledger, an OpenAI key in config.yaml.
# Assumes JE-0001 already exists (the seed/posting demo). Adjust the JE id if not.

Output docs/demos/reverse.gif

Set Shell "bash"
Set FontSize 16
Set Width 1200
Set Height 760
Set Padding 20
Set Theme "Catppuccin Mocha"

Hide
Type "ledger seed seed/taiwan_ledger.yaml" Enter
Sleep 2s
# A first entry to reverse.
Type "ledger book-run --request '5/10 台北總公司現金銷售商品 NT$105,000，含 5% 銷項稅額 5,000。'" Enter
Sleep 14s
Show

Type "ledger tui" Enter
Sleep 3s
Enter
Sleep 2s

Type "請沖轉分錄 JE-0001，金額誤打為 NT$105,000，正確應為 NT$95,000，需先沖銷再重新過帳。"
Sleep 1s
Enter

# Reversal turn: mirror entry + JournalRelation (reverses · amount_error).
Sleep 16s
Sleep 2s
Type "q"
Sleep 1s

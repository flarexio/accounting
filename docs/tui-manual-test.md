# Ledger TUI 手動測試劇本

**受測環境：** 東昇商貿股份有限公司 (`seed/taiwan_ledger.yaml`)
**開帳期間：** 2026-05（2026-03、2026-04 已關帳）
**分公司：** `hq` 台北總公司 · `tc` 台中營業所 · `ks` 高雄營業所
**貨幣：** TWD（整數，無小數）

---

## 0. 前置準備

```bash
# 確認 ledger seed 已執行（必先做）
ledger seed seed/taiwan_ledger.yaml

# 啟動 TUI（需 config.yaml 或環境變數 OPENAI_API_KEY）
ledger tui
```

---

## 1. 選擇畫面導覽

| 操作 | 預期行為 |
|------|----------|
| 啟動後看到 **bookkeeper** 選項 | 選擇畫面顯示 `Accounting - choose an agent + scenario` |
| `↑` / `↓` 或 `k` / `j` | 游標移動 |
| `ctrl+c` / `ctrl+d` | 程式結束（`q` / `ESC` 在此不結束程式）|
| `Enter` | 進入 chat 畫面，spinner 短暫出現後 input 可用 |

---

## 2. Happy Path — 正常過帳情境

每個情境輸入後按 `Enter`，觀察：
1. **spinner** 顯示「running…」
2. **model** 欄位顯示分析過程
3. **tool** 欄位（若出現）顯示 `find_accounts` 查詢結果（帳戶數 > 12 時觸發）
4. 最終 `·` 系統行顯示 `done in N turn(s) — posted entry JE-XXXX`

### T-01 現金銷貨含 5% 銷項稅

```
5/10 現金銷售商品給台灣客戶，含稅金額 NT$105,000，
其中銷貨收入 NT$100,000，銷項稅額 NT$5,000。由台北總公司出帳。
```

預期帳務：
- Dr 1101 庫存現金 105,000
- Cr 4101 銷貨收入 100,000
- Cr 2191 銷項稅額 5,000

---

### T-02 賒銷含 5% 銷項稅

```
5/12 賒賣商品給台中客戶，含稅售價 NT$52,500
（銷貨收入 NT$50,000，銷項稅額 NT$2,500），分公司 tc。
```

預期帳務：
- Dr 1141 應收帳款 52,500
- Cr 4101 銷貨收入 50,000
- Cr 2191 銷項稅額 2,500

---

### T-03 採購商品含 5% 進項稅（現金付款）

```
5/14 台北總公司現金採購商品 NT$42,000（不含稅），
另付 5% 進項稅 NT$2,100，合計 NT$44,100 以現金支付。
```

預期帳務：
- Dr 1211 商品存貨 42,000
- Dr 1281 進項稅額 2,100
- Cr 1101 庫存現金 44,100

---

### T-04 勞務收入含 5% 銷項稅（銀行存款收款）

```
5/15 高雄營業所完成軟體顧問服務，收到銀行轉帳 NT$30,000（不含稅），已入帳。分公司 ks。
```

預期帳務：
- Dr 1103 銀行存款 31,500
- Cr 4201 勞務收入 30,000
- Cr 2191 銷項稅額 1,500

> NT$30,000 為稅前金額，含稅實收 NT$31,500（NT$30,000 × 1.05）。

---

### T-05 薪資支出（含勞健保代扣及所得稅）

```
5月薪資：台北總公司正職員工薪資總額 NT$1,234,567，
扣勞保員工負擔 NT$38,400、健保員工負擔 NT$22,100、
所得稅代扣 NT$61,200，實際匯款員工帳戶。
```

預期帳務：
- Dr 6101 薪資支出 1,234,567
- Cr 2201 代收款（勞保）38,400
- Cr 2201 代收款（健保）22,100
- Cr 2201 代收款（所得稅）61,200
- Cr 1103 銀行存款 1,112,867

貸方合計：38,400 + 22,100 + 61,200 + 1,112,867 = 1,234,567 ✓

---

### T-06 租金支出（銀行轉帳）

```
5/1 台北總公司以銀行轉帳支付 5 月辦公室租金 NT$120,000。
```

預期帳務：
- Dr 6102 租金支出 120,000
- Cr 1103 銀行存款 120,000

---

### T-07 水電費

```
5/20 收到並立即用銀行存款支付台中營業所 5 月水電瓦斯費帳單
NT$8,500，分公司 tc。
```

預期帳務：
- Dr 6109 水電瓦斯費 8,500
- Cr 1103 銀行存款 8,500

---

### T-08 應收票據收現

```
5/22 台北總公司收到原 4 月應收票據 NT$200,000 到期兌現，
款項存入銀行。
```

預期帳務：
- Dr 1103 銀行存款 200,000
- Cr 1131 應收票據 200,000

---

## 3. 沖轉情境

### T-09 沖轉錯誤再重記（`reverse_journal` + recent-context recall）

兩步、**同一個 session**（recall 記憶只在 session 內存活）。先記下 T-01 產生的 JE ID（系統行 `posted entry JE-XXXX`）。

**Turn 1 — 沖銷**（只要求沖掉，不重記）：

```
JE-XXXX 金額打錯了，幫我沖掉。
```

預期：鏡像分錄（Cr/Dr 互換、金額相同）
- Cr 1101 庫存現金 105,000
- Dr 4101 銷貨收入 100,000
- Dr 2191 銷項稅額 5,000

預期 `JournalRelation`：
- `type = reverses`
- `reason = amount_error`（依據劇本的具體錯誤分類，**不應**落到 `other`）
- `note` 描述具體事實，**不應**只寫「過帳錯誤」或「需要沖正」

**Turn 2 — 重記正確金額**（口語、刻意不重述交易內容）：

```
再用正確的 94,500 含稅重新過一筆。
```

預期：agent 先呼叫 `recent_entries`（transcript 出現一條 `tool` 行）找回剛沖掉的是現金銷貨，再過一筆**新** `post_journal`：
- Dr 1101 庫存現金 94,500
- Cr 4101 銷貨收入 90,000
- Cr 2191 銷項稅額 4,500

> `reverse_journal` 只做機械式鏡像；「重記」是另一筆 `post_journal`，靠 recent-context recall 把 turn 1 的脈絡接起來。agent **絕不自行編造金額**，缺資訊則 `reject`。客戶退貨另走 `post_journal` 搭配 4111 銷貨退回及折讓，由使用者明確指定。

**Turn 1+2 合成一句 — multi-action 原子提交**：上面拆兩 turn 的事，也可以一個 request 一次說完。agent 在同一迴圈跑沖銷＋重記兩個 action，最後**一次** all-or-nothing 提交：

```
JE-XXXX 金額打錯了，幫我沖掉，再用正確的含稅 94,500 重新過一筆。
```

預期分錄同上（沖銷 + 1101 借 94,500 的新分錄），差別在系統行**一次**列兩筆：`posted 2 entries: JE-A, JE-B`，且中間沒有「只沖銷、未重記」的中途狀態。

**原子中止**：用過低的 turn 上限逼出 `MaxTurns`（`ledger tui --max-turns 1`），下同樣的多動作 request。第一個 turn 發出非 final 的沖銷後就撞上限：不出現 `posted ...`、顯示 `max turns exceeded`，且 ledger **完全沒變**（沒留下半筆沖銷）。用 `book-run` 對照最直接——正常時 `entries` 兩筆、中止時 `entries` 為空且回非零結束碼。

---

## 4. 應收應付（AR/AP）情境

驗證 counterparty（客戶/供應商）、發票單據（`source`）與 `settle` 收款沖銷。種子已含客戶／供應商主檔（`CP-0001` 台積電、`CP-0003` 中華電信、`CP-0099` 旭海貿易[停用]…），可用 `find_counterparties` 解析。

> **重要**：`counterparty_id` 是**選填**。對**未登記/泛稱的客戶**（如「台中客戶」、一次性散客），agent 仍應**照常過帳**、只是 `counterparty_id` 留空（不歸戶），**不該 reject**。只有當使用者指名某個 `find_counterparties` 顯示為**停用**的對象時才 reject（見 T-16）。

### T-13 賒銷開立發票（客戶 + 發票號）

同一個 session，先記下這筆產生的 JE ID（後面 T-14 要用）。

```
5/16 賒賣商品給台積電，開立統一發票 AB-12345678，
含稅 NT$52,500（銷貨收入 NT$50,000、銷項稅額 NT$2,500），台中營業所。
```

預期：
- agent 先呼叫 `find_counterparties`（transcript 出現 `tool` 行）把「台積電」解析成 `CP-0001`。
- 過一筆 `post_journal`：
  - Dr 1141 應收帳款 52,500（`counterparty_id = CP-0001`）
  - Cr 4101 銷貨收入 50,000
  - Cr 2191 銷項稅額 2,500
- entry 帶 `source = { kind: invoice, number: "AB-12345678" }`。
- 現金/稅額等非客戶歸屬的行 `counterparty_id` 留空。

> 用 `book-run` 看 JSON 最直接：`entries[0].source.number` 為發票號、應收那行 `dimensions.counterparty_id` 為 `CP-0001`。

---

### T-14 收款沖銷發票（`settle`）

承 T-13，**同一 session**。

```
台積電付清了上面那張發票 AB-12345678，款項 NT$52,500 匯入銀行存款。
```

預期：agent 用 **`settle`** intent（非單純 post_journal）：
- 過收款分錄：Dr 1103 銀行存款 52,500 / Cr 1141 應收帳款 52,500（`counterparty_id = CP-0001`）。
- 建立 `JournalRelation`：`type = settles`，`from_entry` = 收款分錄、`to_entry` = T-13 的發票 JE。
- 系統行顯示過帳一筆;`book-run` 對照時 `RelationsTo(<發票JE>)` 應出現一條 settles 指回收款分錄。

> 「這張發票收了沒」是衍生查詢（發票分錄 + settles 關係），不是存起來的狀態。

---

### T-15 賒購應付（供應商）

```
5/18 向中華電信賒購網路服務,取得發票 CHT-99887766,
含稅 NT$3,150（費用 NT$3,000、進項稅額 NT$150），台北總公司。
```

預期：`find_counterparties` 解析「中華電信」→ `CP-0003`，過一筆應付分錄，應付帳款那行帶 `counterparty_id = CP-0003`，entry `source.kind = bill`、`number = CHT-99887766`。

> 借方科目以 agent 依語意選用為準（如 6109 水電瓦斯費或服務費用科目）；重點在 2141 應付帳款帶供應商、`source.kind` 為 `bill`。

---

## 5. 拒絕情境

### T-10 指定已關帳期間

```
請在 2026-03 期間記錄 NT$10,000 現金收入。
```

預期：**observation** 欄位顯示拒絕原因，說明 2026-03 已關帳。不會過帳，不會靜默改成 2026-05。

---

### T-11（unit test 範疇）

不平衡分錄的驗證由 `PostJournal.Validate` 的 unit test 覆蓋。LLM 知道借貸必須平衡，不會刻意產生不平衡分錄，TUI 層不需要此情境。

---

### T-12 停用科目

```
請使用「其他費用（舊制）」帳號記錄 NT$5,000 雜支。
```

預期：**observation** 欄位顯示拒絕原因，說明該科目已停用。LLM 不會自行改用替代科目（如 6117 雜費），由使用者決定。

---

### T-16 停用客戶/供應商

```
向「旭海貿易」賒購商品 NT$5,000，記應付帳款。
```

預期：`find_counterparties` 找到 `CP-0099` 但標記為停用（disabled）。agent 不得把它放進分錄的 `counterparty_id`；應 `reject` 並說明該客戶/供應商已停用，由使用者決定，不自行改用別家。

---

## 6. 操作控制測試

| 操作 | 時機 | 預期行為 |
|------|------|----------|
| `ESC`（turn 進行中） | spinner 轉動時 | 中止該 turn，顯示「turn cancelled」，保持 chat 畫面（不結束程式）|
| `ESC`（turn 完成後） | 無 spinner 時 | 退回選擇畫面，session 關閉；單一分公司時改為重連一個全新 session（清空對話）|
| `ctrl+c` / `ctrl+d` | 任何時候、任何畫面 | 程式結束（唯一的結束方式）|
| `↑` / `↓` | chat 畫面、有多輪對話時 | viewport 單行捲動，不觸發新 turn（chat 的輸入是單行，方向鍵改作捲動）|
| `pgup` / `pgdn` | 有多輪對話時 | viewport 整頁捲動，不觸發新 turn |
| `home` / `end` | chat 畫面 | 捲到 transcript 最頂 / 最底（輸入框跳游標改用 `ctrl+a` / `ctrl+e`）|
| 送出空白輸入 | 直接按 Enter | 不觸發 turn，input 維持 focus |

---

## 7. 驗收檢查點

- [ ] T-01～T-08 每個情境都看到 `posted entry JE-XXXX`
- [ ] T-05 薪資複雜金額貸方合計正確（1,234,567）
- [ ] T-09 turn 1 沖轉分錄借貸互換、金額相同，`JournalRelation.reason` 為具體分類（非 `other`）、`note` 具體
- [ ] T-09 turn 2 出現 `recent_entries` 工具呼叫，重記分錄借方 1101 為 94,500（稅拆 90,000 + 4,500）
- [ ] T-09 合成單一 request 時一次過帳兩筆（`posted 2 entries: ...`），`--max-turns 1` 中止後 ledger 無任何變動
- [ ] T-13 賒銷出現 `find_counterparties` 工具呼叫，應收 1141 帶 `counterparty_id = CP-0001`，entry `source` 為發票 AB-12345678
- [ ] T-14 收款用 `settle`，產生 `settles` 關係由收款分錄指回 T-13 發票 JE
- [ ] T-15 賒購應付帶供應商 `CP-0003`，`source.kind = bill`
- [ ] T-16 停用客戶/供應商出現 `reject`，不放進 `counterparty_id`、不自行換別家
- [ ] T-10 指定關帳期間時出現 `reject`，不過帳、不替換期間
- [ ] T-12 停用科目出現 `reject`，LLM 不自行選替代科目
- [ ] 所有 TWD 金額以整數輸入，無小數點
- [ ] `ESC` 中止進行中的 turn 後程式不崩潰、仍可繼續輸入；`ESC` 永不結束程式，結束只能按 `ctrl+c` / `ctrl+d`
- [ ] 返回選擇畫面後可重新進入 session

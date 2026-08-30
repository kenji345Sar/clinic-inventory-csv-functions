# 卸業者の商品マスタCSV取り込み(商品マスタ側から見た設計)

複数の卸業者から送付される商品マスタCSVをS3経由で受け取り、`distributor_products`に反映する仕組みについて、
**backend側が持つ責務**をまとめる。

> このドキュメントは**反映される側(clinic-inventoryのbackend)の設計**を扱う。
> パイプラインそのもの(S3キーの読み取り・卸ごとのフォーマット差の吸収・冪等性・起動方法)は[design.md](design.md)。

- 位置づけ: `docs/architecture/domain-rules.md`(clinic-inventory側)の3種類のCSVのうち「商品マスタ・価格表CSV」
- S3バケット・IAMの実設定は[s3-storage.md](s3-storage.md)
- 別種のCSV: 受注確定CSV(卸の引き当て結果・納入単価)の受け皿は未実装。論点は[order-acknowledgement-import.md](order-acknowledgement-import.md)

最終更新: 2026-08-29

---

## 1. なぜリポジトリを分けたか

- **デプロイ単位が違う**。backendは常時起動するAPIサーバー、取り込みは定刻に起動するバッチ(将来はGCPのCloud Functions + Cloud Scheduler)。
- **止まったときの影響が違う**。取り込みが失敗してもAPIは動き続ける必要がある。
- 実際、社内の別プロジェクトでも同じ形(`external-csv-transaction-functions`)を採っている。

分けたうえで、**テーブルの作成・変更(マイグレーション)はbackendだけが行う**。取り込み側は
マイグレーションを持たず、既にあるテーブルへ読み書きするだけにしている。スキーマの所有者を
1つに保つため。

| | backend(このリポジトリ) | clinic-inventory-csv-functions |
|---|---|---|
| テーブルの作成・変更 | **行う**(`cmd/api/main.go`のAutoMigrate) | 行わない |
| 画面・APIからの参照/更新 | 行う | 行わない |
| S3からのCSV取り込みと反映 | 行わない | **行う** |

---

## 2. 単価をどう表現するか

**卸業者ごとに、送ってくる項目も粒度もバラバラ**である、というのがこのコンテキストの前提。
1社の形に合わせてモデルを作ると他社で破綻するため、「無い」ことを許容できる受け皿にしてある。
中でも影響が大きいのが単価で、backend側は3パターンすべてを表現できるスキーマとドメインを持つ。

JANコード・ベンダー名のように「列が無い卸がある」項目をどう埋めるかは取り込み側の責務。
[design.md](design.md)「4. 卸ごとのフォーマット差の吸収」を参照。

### 単価の3パターン

| 卸のパターン | 保存先 |
|---|---|
| 商品ごとの単価のみ公開 | `distributor_products.unit_price` |
| 医院ごとに単価を決めている | `distributor_product_facility_prices`(医院別単価)。商品側の`unit_price`はNULLのまま |
| 単価を公表していない | どちらにも入れない(`unit_price`はNULL) |

パターンごとに実際の行がどう作られるか(CSVの例と対応表)は
[design.md](design.md)「3-1. パターン別に、テーブルがどうなるか」を参照。

- `unit_price`は**NULL許容**。「0円」と「非公表」を区別するため、ドメイン側も`*int`で持つ
  (`backend/internal/domain/distributorcatalog/distributor_product.go`(clinic-inventory側))。
- 医院別単価は`DistributorProduct`の配下エンティティにせず独立させている。単価は卸とクリニックの契約に
  紐づく情報で、商品マスタとは別のタイミング・別のCSVで届くことがあるため
  (`backend/internal/domain/distributorcatalog/facility_price.go`(clinic-inventory側))。
- クリニック商品(`clinic_products.unit_price`)の仕入単価は、**クリニックでの入力値 → 自院向けの医院別単価 →
  卸の標準単価**の順で決める。どれも無い場合は**0のまま登録する**(登録を止めない)。単価が分からない卸があり、
  その場合は後日、卸から届く受注結果の単価で更新する運用にしているため。
  実装は`backend/internal/application/productcatalog/register_clinic_product.go`(clinic-inventory側)。
- 画面では「非公表」という表記は使わず、単価が無い場合は0円として表示する。卸から見れば非公表でも、
  クリニックから見れば「まだ分からない金額」であり、後から確定する扱いのため。
- 医院ごとに単価を決めている卸の商品は、クリニック側の卸商品一覧・登録画面で**自院向けの単価**が出る
  (`GET /api/distributors/{id}/products?facilityId=...` が `facilityUnitPrice` を返す)。
- 卸ポータルの商品マスタでは、標準単価が無く医院別単価がある商品を「医院別（N院）」と表示し、
  選ぶと医院名付きの内訳が出る。¥0と出すと「0円で卸している」と読めてしまうため
  (`GET /api/portal/distributors/{id}/products` の `facilityPriceCount` と、
  `.../products/{productId}/facility-prices`)。一覧に全商品分の単価を載せると
  商品数×医院数になるため、一覧は件数だけ・内訳は選択時に取得する。

---

## 3. backend側が持つもの

| 役割 | 場所 |
|---|---|
| 反映先テーブルの定義 | `backend/internal/infrastructure/distributorcatalog/model.go`(clinic-inventory側) |
| 取り込み用テーブル(履歴・ステージング)の定義 | `backend/internal/infrastructure/distributorcsvingestion/model.go`(clinic-inventory側) — 定義のみ。読み書きと`status`の意味は[design.md](design.md)5章 |
| テーブル作成 | `backend/cmd/api/main.go`(clinic-inventory側)のAutoMigrate |
| 卸商品・医院別単価の参照 | 各リポジトリ。医院別単価は参照のみ(登録・更新は取り込み側) |

**取り込み側リポジトリと同じ集約(`DistributorProduct` / `FacilityPrice`)が両方に存在する。**
業務ルール(必須項目・単価の扱い・廃盤)を変更する場合は、両方を揃える必要がある。
これはリポジトリを分けたことによるトレードオフとして受け入れている。

---

## 4. 未決(backend側の判断が要るもの)

S3権限・卸側の医院コード体系・処理済みCSVの退避など取り込み側の未決は[design.md](design.md)7章にある。

| 項目 | 現状 |
|---|---|
| CSVから消えた商品の扱い | 廃盤にしない(放置)。全件洗い替えの卸に対して自動廃盤にするかは未決 |
| 廃盤になった商品の発注 | **取り込みによって顕在化した未解決事項**。取り込みが`discontinued`を自動更新するため「登録後に廃盤になった商品」が発生するが、発注(カート追加・確定)では廃盤を検証していないため発注できてしまう。止める場所と強さは`docs/requirements.md`9章(clinic-inventory側)に記載 |

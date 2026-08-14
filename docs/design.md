# 商品マスタCSV取り込みの設計

卸業者から届く商品マスタCSVをS3経由で受け取り、clinic-inventory のDBへ反映するまでの設計。
決まっていない点は「未決」と明記する。

反映先のテーブル定義・商品マスタ側の業務ルールは clinic-inventory 側のドキュメントを参照。

- `docs/architecture/domain-rules.md`「卸連携CSV基盤」… 3種類のCSVと失敗時の扱い
- `docs/architecture/distributor-catalog-import.md` … 商品マスタ側から見た取り込み（単価の持ち方）
- `docs/architecture/database-schema.md` … 反映先テーブルの定義

最終更新: 2026-08-14

---

## 1. S3キーの規約

```
orders/{distributorId}/{facilityId}/{orderId}.csv   ← 発注CSV(clinic-inventoryが書く)
catalogs/{distributorId}/{任意のファイル名}.csv      ← 商品マスタCSV(卸が置き、このバッチが読む)
```

卸ごとにフォルダ(プレフィックス)を分けているため、**どの卸のCSVかは中身ではなく置かれた場所で決まる**。
CSVに卸を識別する列が無くても判別でき、卸側にS3の権限を渡す場合もプレフィックス単位で分離できる。

---

## 2. 全体の流れ(中間表現を挟む二段構成)

CSVを直接商品マスタに書かず、**いったん共通の中間表現に正規化してステージングテーブルに保存し、
それを元にDBへ反映する**。

```
S3 CSV(卸A形式) ┐
S3 CSV(卸B形式) ├→ [パース] → 中間表現CatalogRow → [ステージング保存] → [検証・反映] → distributor_products
S3 CSV(卸C形式) ┘    卸ごとの差はここだけ                                              + distributor_product_facility_prices
```

段を分ける理由:

- **失敗の切り分けができる**。「CSVが読めない(パース失敗)」のか「読めたが反映できない(コード不一致など)」のかが
  別フェーズになり、要確認をどちらの段で止まったかまで含めて記録できる。
- **反映前に差分を確認できる**。新規◯件・単価変更◯件をステージングの内容から出せるため、
  廃番の誤判定のような事故を反映前に止められる。
- **卸が増えても後段が共通**。前段(CSV→中間表現)だけ卸ごとに足せばよい。

中間表現は [internal/application/distributorcsvingestion/catalog_row.go](../internal/application/distributorcsvingestion/catalog_row.go)、
ステージング行は [internal/domain/distributorcsvingestion/ingestion_run.go](../internal/domain/distributorcsvingestion/ingestion_run.go)。
ステージングにはCSVの原文(`raw`)も残し、突合に失敗した行を人が追えるようにする。

---

## 3. 単価の3パターン

卸によって単価の出し方が違う。

| 卸のパターン | 中間表現 | 反映先 |
|---|---|---|
| 商品ごとの単価のみ公開 | `UnitPrice`に値 | `distributor_products.unit_price` |
| 医院ごとに単価を決めている | `FacilityPrices`に医院分の行 | `distributor_product_facility_prices` |
| 単価を公表していない | `UnitPrice`が`nil`、`FacilityPrices`が空 | どちらにも入れない(`unit_price`はNULL) |

「0円」と「非公表」を区別するため、単価はポインタ(`*int`)で持つ。
クリニック商品の仕入単価をどう決めるかは clinic-inventory 側の責務。

---

## 4. 卸ごとのフォーマット差の吸収

**列マッピング定義（JSON）で吸収し、定義で表せない構造の卸だけ専用パーサを足す**方針。
卸が増えるたびにコードを足す方式（実装コストが線形に増える）と、
マッピングを画面から設定できるようにする方式（UIまで作る必要がある）の中間を採っている。

定義は卸IDをキーにしたJSONファイル（`config/distributor-csv-mappings.json`、
パスは環境変数`CATALOG_CSV_MAPPINGS`で変更可）。卸IDは実行時に採番されるUUIDのため、
ソースに埋め込まず設定ファイルに外出しする。書式は
[config/distributor-csv-mappings.example.json](../config/distributor-csv-mappings.example.json) を参照。

対応している差分:

- 文字コード（Shift_JIS / UTF-8）
- ヘッダ行の有無
- 各項目の列番号（指定しない項目は取り込まない＝単価列を指定しなければ非公表になる）
- 桁区切りカンマ・「円」付きの単価
- ベンダー名の列が無い卸（`defaultVendorName`で補う）
- 医院別単価の縦持ち（1商品×1医院で1行のCSVを、商品単位にまとめる）

---

## 5. 冪等性と失敗時の扱い

- **同じキー・同じETagは取り込み済みとしてスキップ**する。ETagはS3オブジェクトの内容から決まるため、
  中身が更新されていれば再取り込みされる。判定は`distributor_catalog_ingestion_runs`テーブル。
- **反映はupsert**。突合キーは`(distributor_id, distributor_product_code)`。何度流しても結果が同じになる。
- **CSVに無い商品は削除しない**。送付漏れで既存マスタが消えるのを防ぐ。廃番は`discontinued`フラグで表現し、
  CSVに廃番列がある場合のみ反映する。
- **パースは行単位で継続、反映はファイル単位のトランザクション**。1行の不備で全体が見えなくなるのを避けつつ、
  1件でもエラーがあればロールバックして要確認で止める（中途半端な更新を残さない）。
- **失敗時は自動リトライしない**。要確認として記録し、生データはS3に残して人手対応に委ねる。

---

## 6. 取り込みの起動方法

S3イベント駆動にはしない。バッチ・DBはGCP側に置く想定で、AWSのS3イベントを直接受け取れないため、
プレフィックス配下を一覧するポーリング方式にしている。

- 現在: `./run.sh`（`cmd/csvsync`）をcron等から実行
- 将来: 同じユースケースをCloud Functionsに載せ、Cloud Schedulerから定刻起動

処理本体はユースケース層にあり、`cmd/csvsync`は配線だけなので、関数エントリポイントを足せば載せ替えられる。

---

## 7. 未決

| 項目 | 現状 |
|---|---|
| IAM権限 | 取り込みには`catalogs/`配下への`s3:ListBucket`と`s3:GetObject`が必要。clinic-inventory側の`docs/architecture/s3-storage.md`にポリシーを記載してあるが、**AWSコンソールでの反映は未実施** |
| 卸側の医院コード | 現状は「医院コード = クリニックID(UUID)」として扱っている。実際の卸は自社の医院コード体系を使うため、`(卸業者, 卸側医院コード) → クリニックID`の対応表が必要。変換は[facility_resolver.go](../internal/infrastructure/distributorcsvingestion/facility_resolver.go)1か所に閉じてある |
| 卸側の書き込み手段 | 卸に`catalogs/{distributorId}/`へのPut権限を渡すか、こちらが受領してアップロードするか。当面は後者 |
| 処理済みCSVの退避 | 実施しない（ETagで取り込み済みを判定する）。`processed/`へ移す運用にする場合は`s3:DeleteObject`の追加が必要 |
| 反映前の差分確認 | 未実装。ステージングに中間表現が残っているため、後から足せる |
| 受注確定CSV・売上明細CSV | 未着手 |
